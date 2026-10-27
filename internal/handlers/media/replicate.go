package media

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nox9go/proxy/internal/handlers/chat"
	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/usagetracker"
)

// Replicate is not an OpenAI-shaped media provider, and pretending otherwise is
// what broke the first real connection attempt: KnownProviders carried a
// BaseURL ending in /chat/completions, the generic video forwarder POSTed an
// OpenAI body to it, and Replicate answered "no API key found" — an auth error
// for what was really a missing route.
//
// Its protocol is a prediction queue:
//
//	POST /v1/models/{owner}/{name}/predictions   {"input": {...}}
//	GET  /v1/predictions/{id}                    until status is terminal
//
// The model id carries the owner, so the endpoint cannot be a constant in
// KnownProviders, and "input" is each model's own schema rather than a
// messages array. That is why this lives in its own lane instead of a VideoURL
// override: a single URL field cannot express a per-model path.
//
// "Prefer: wait" makes the create call block for up to 60s, which covers most
// short video jobs and avoids a poll round-trip entirely. Anything still
// running is polled, because a caller that asked for a video should not be
// handed an id and told to come back.

const (
	replicateAPIRoot      = "https://api.replicate.com/v1"
	replicatePreferWait   = "wait=60"
	replicatePollMaxTries = 60
	replicatePollDelay    = 2 * time.Second
)

// replicateRequest is the OpenAI-ish body a caller posts to
// /v1/videos/generations. Only a few fields have a meaning Replicate shares;
// everything else is folded into "input" so a caller can pass model-specific
// parameters (num_frames, aspect_ratio, duration) without this lane growing a
// field for each one.
type replicateRequest struct {
	Model  string         `json:"model"`
	Prompt string         `json:"prompt"`
	Input  map[string]any `json:"input"`
}

// replicatePrediction is the subset of the prediction object this lane reads.
type replicatePrediction struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Output any      `json:"output"`
	URLs   struct {
		Get    string `json:"get"`
		Stream string `json:"stream"`
	} `json:"urls"`
	Error  any    `json:"error"`
	Detail string `json:"detail"`
}

// isVideoEndpoint matches the lane that starts a video job.
//
// Only generations. Replicate has no edit or extend endpoint of its own: its
// API is predictions, models, deployments and trainings, and "editing" a clip
// means running some video model with the clip as an input. Accepting
// /videos/edits here would promise a capability the upstream does not have and
// send the caller's body to a generation call that ignores it — worse than the
// 404 the generic lane gives, because it costs a prediction.
func isVideoEndpoint(endpoint string) bool {
	return endpoint == "/v1/videos/generations" || endpoint == "/videos/generations"
}

// isImageEndpoint matches the text-to-image lane.
func isImageEndpoint(endpoint string) bool {
	return endpoint == "/v1/images/generations" || endpoint == "/images/generations"
}

// isReplicateModel reports whether a resolved model belongs to Replicate.
// Matched on the provider id the catalog uses, which is also what the
// model prefix resolves to.
func isReplicateModel(provider string) bool {
	return provider == "replicate"
}

// replicatePredictionURL builds the create endpoint for a model id.
//
// Replicate has three ways to create a prediction and the id decides which one
// applies:
//
//	owner/name              -> /models/{owner}/{name}/predictions  (official model)
//	owner/name:version      -> /predictions with "version"          (pinned version)
//	deployments/owner/name  -> /deployments/{owner}/{name}/predictions
//
// The deployment form is spelled out because a fine-tuned or private model
// lives there, and sending it down either of the other paths is a 404 that
// reads like a bad credential.
func replicatePredictionURL(model string) (string, string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", "", errors.New("replicate: model id is empty")
	}
	if strings.HasPrefix(model, "deployments/") {
		rest := strings.TrimPrefix(model, "deployments/")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf(
				"replicate: deployment %q must be deployments/owner/name", model,
			)
		}
		return replicateAPIRoot + "/deployments/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/predictions", "", nil
	}
	if strings.Contains(model, ":") {
		ownerName, version, _ := strings.Cut(model, ":")
		if ownerName == "" || version == "" {
			return "", "", fmt.Errorf("replicate: malformed versioned model %q", model)
		}
		return replicateAPIRoot + "/predictions", version, nil
	}
	parts := strings.Split(model, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf(
			"replicate: model %q must be owner/name (e.g. black-forest-labs/flux-1.1-pro) or owner/name:version",
			model,
		)
	}
	return replicateAPIRoot + "/models/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/predictions", "", nil
}

// handleReplicateVideo runs a video generation through Replicate's prediction
// API and answers in the OpenAI video shape the caller expects.
//
// The returned error is a transport-level failure; an upstream refusal is
// written to w with the upstream status so a 402 (out of credit) or a 422
// (bad input for that model) reaches the caller as itself rather than as a
// generic 502.
func (h *MediaHandler) handleReplicateVideo(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo) error {
	succeeded, model, pred, err := h.replicateRun(w, r, body, modelInfo)
	if err != nil {
		return err
	}
	if !succeeded {
		return nil
	}
	writeReplicateVideoResult(w, model, pred)
	return nil
}

// handleReplicateImage runs a text-to-image job through the same prediction
// queue. The only difference from video is the response shape: an image
// endpoint answers with "data": [{"url": ...}], and a model that returns
// several images is reported as several entries.
//
// The request body may carry size/n/quality for compatibility with the OpenAI
// image shape; those are dropped unless the caller also passed an input object,
// because Replicate models name their own knobs (aspect_ratio, num_outputs,
// width/height) and forwarding "size" to a model that does not know it is a
// 422.
func (h *MediaHandler) handleReplicateImage(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo) error {
	var req replicateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("replicate: invalid request body: %w", err)
	}
	var extra struct {
		N int `json:"n"`
	}
	_ = json.Unmarshal(body, &extra)

	succeeded, model, pred, err := h.replicateRun(w, r, body, modelInfo)
	if err != nil {
		return err
	}
	if !succeeded {
		return nil
	}
	writeReplicateImageResult(w, model, pred, extra.N)
	return nil
}

// replicateRun is the shared half of the image and video lanes: resolve the
// model, build the prediction, select a connection, and drive it to a terminal
// state.
//
// It reports succeeded=false after writing an error response of its own, so a
// caller never formats the same failure twice. A non-nil error is a
// transport-level problem with no upstream answer to relay.
func (h *MediaHandler) replicateRun(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo) (bool, string, *replicatePrediction, error) {
	var req replicateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return false, "", nil, fmt.Errorf("replicate: invalid request body: %w", err)
	}

	model := strings.TrimSpace(modelInfo.Model)
	if model == "" {
		model = strings.TrimSpace(req.Model)
	}
	createURL, version, err := replicatePredictionURL(model)
	if err != nil {
		return false, "", nil, err
	}

	// The caller's prompt and any extra fields become "input". A caller that
	// already shaped an input object keeps it and the prompt is merged into it
	// only when it is absent, so neither form is silently dropped.
	input := map[string]any{}
	for k, v := range req.Input {
		input[k] = v
	}
	if p := strings.TrimSpace(req.Prompt); p != "" {
		if _, ok := input["prompt"]; !ok {
			input["prompt"] = p
		}
	}
	if len(input) == 0 {
		return false, "", nil, errors.New("replicate: a request needs at least a prompt or an input object")
	}
	payload := map[string]any{"input": input}
	if version != "" {
		payload["version"] = version
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return false, "", nil, fmt.Errorf("replicate: encode payload: %w", err)
	}

	conn, connData, err := h.ChatH.GetBestConnectionWithContext(r.Context(), "replicate", replicatePreferredConnID(r, modelInfo), nil, model)
	if err != nil || connData == nil {
		return false, "", nil, fmt.Errorf("no active replicate connection: %w", err)
	}
	apiKey := chat.ExtractAPIKey(connData)
	if apiKey == "" {
		return false, "", nil, fmt.Errorf("no API key found for replicate connection %s", conn.ID)
	}
	client, err := h.ChatH.GetClientForConnection(connData)
	if err != nil {
		return false, "", nil, err
	}

	connID := ""
	if conn != nil {
		connID = conn.ID
	}
	var fwdErr error
	usagetracker.GetTracker().TrackPending(model, "replicate", connID, true, false)
	defer func() {
		usagetracker.GetTracker().TrackPending(model, "replicate", connID, false, fwdErr != nil)
	}()

	pred, err := h.replicateCreate(r, client, createURL, apiKey, payloadBytes)
	if err != nil {
		fwdErr = err
		return false, "", nil, err
	}

	// "Prefer: wait" only blocks up to its budget. Anything still queued is
	// polled on the same client so the caller gets a finished job rather than
	// a bare prediction id.
	if !replicateTerminal(pred.Status) {
		pred, err = h.replicatePoll(r, client, pred, apiKey)
		if err != nil {
			fwdErr = err
			return false, "", nil, err
		}
	}

	if pred.Status != "succeeded" {
		fwdErr = fmt.Errorf("replicate prediction %s ended as %s", pred.ID, pred.Status)
		handlerutil.WriteJSONError(w, http.StatusBadGateway, replicateFailureReason(pred))
		return false, model, nil, nil
	}
	return true, model, pred, nil
}

func replicatePreferredConnID(r *http.Request, modelInfo *chat.ModelInfo) string {
	if id := r.Header.Get("x-connection-id"); id != "" {
		return id
	}
	if id := r.Header.Get("x-provider-connection-id"); id != "" {
		return id
	}
	return modelInfo.ConnectionID
}

func (h *MediaHandler) replicateCreate(r *http.Request, client *http.Client, createURL, apiKey string, payload []byte) (*replicatePrediction, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, createURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", replicatePreferWait)
	handlerutil.SetAuthHeader(req, apiKey, "Authorization", "bearer")

	resp, err := doDirectOrClient(r.Context(), client, req)
	if err != nil {
		return nil, fmt.Errorf("replicate: create prediction: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("replicate: create prediction: HTTP %d: %s", resp.StatusCode, replicateUpstreamMessage(respBody))
	}
	var pred replicatePrediction
	if err := json.Unmarshal(respBody, &pred); err != nil {
		return nil, fmt.Errorf("replicate: decode prediction: %w", err)
	}
	return &pred, nil
}

func (h *MediaHandler) replicatePoll(r *http.Request, client *http.Client, pred *replicatePrediction, apiKey string) (*replicatePrediction, error) {
	getURL := pred.URLs.Get
	if getURL == "" && pred.ID != "" {
		getURL = replicateAPIRoot + "/predictions/" + url.PathEscape(pred.ID)
	}
	if getURL == "" {
		return nil, errors.New("replicate: prediction has neither an id nor a get url")
	}
	if u, err := url.Parse(getURL); err != nil || u.Scheme != "https" {
		// The URL comes from the upstream response; refusing a non-https one
		// keeps a compromised answer from steering the poll elsewhere.
		return nil, fmt.Errorf("replicate: refusing non-https poll url %q", getURL)
	}

	for i := 0; i < replicatePollMaxTries; i++ {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(replicatePollDelay):
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, getURL, nil)
		if err != nil {
			return nil, err
		}
		handlerutil.SetAuthHeader(req, apiKey, "Authorization", "bearer")
		resp, err := doDirectOrClient(r.Context(), client, req)
		if err != nil {
			return nil, fmt.Errorf("replicate: poll prediction: %w", err)
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("replicate: poll prediction: HTTP %d: %s", resp.StatusCode, replicateUpstreamMessage(respBody))
		}
		var next replicatePrediction
		if err := json.Unmarshal(respBody, &next); err != nil {
			return nil, fmt.Errorf("replicate: decode polled prediction: %w", err)
		}
		if replicateTerminal(next.Status) {
			return &next, nil
		}
	}
	return nil, fmt.Errorf("replicate: prediction %s still %s after %d polls", pred.ID, pred.Status, replicatePollMaxTries)
}

func replicateTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled":
		return true
	}
	return false
}

// replicateFailureReason prefers the prediction's own error text: "failed" on
// its own tells the caller nothing about whether the prompt, the model or the
// account is at fault.
func replicateFailureReason(pred *replicatePrediction) string {
	if pred.Error != nil {
		if s, ok := pred.Error.(string); ok && s != "" {
			return "replicate: " + s
		}
		if b, err := json.Marshal(pred.Error); err == nil && len(b) > 2 {
			return "replicate: " + string(b)
		}
	}
	if pred.Detail != "" {
		return "replicate: " + pred.Detail
	}
	return fmt.Sprintf("replicate: prediction %s ended as %s", pred.ID, pred.Status)
}

// replicateUpstreamMessage digs the message out of an error body. Replicate
// answers {"detail": "..."} for its own refusals and
// {"error": "...", "message": "..."} for auth, and showing the raw JSON of
// either is how "no API key found" got mistaken for a bad key earlier.
func replicateUpstreamMessage(body []byte) string {
	var e struct {
		Title   string `json:"title"`
		Detail  string `json:"detail"`
		Error   any    `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil {
		return strings.TrimSpace(string(body))
	}
	parts := make([]string, 0, 4)
	if e.Error != nil {
		if s, ok := e.Error.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	if e.Detail != "" {
		parts = append(parts, e.Detail)
	}
	// DRF's "title" is what a wrong path comes back as ("Not Found"), which is
	// the exact signal this lane exists to surface instead of an auth error.
	if e.Title != "" {
		parts = append(parts, e.Title)
	}
	if len(parts) == 0 {
		return strings.TrimSpace(string(body))
	}
	return strings.Join(parts, ": ")
}

// writeReplicateVideoResult renders a succeeded prediction as the OpenAI video
// object the endpoint's callers already parse.
//
// Replicate's output is a model-defined shape: a bare URL string, a list of
// URLs, or an object with a url/video field. All three are read rather than
// assuming one, because a video model that answers with a list would otherwise
// reach the caller as an empty result.
func writeReplicateVideoResult(w http.ResponseWriter, model string, pred *replicatePrediction) {
	outURL := replicateOutputURL(pred.Output)
	body := map[string]any{
		"id":         pred.ID,
		"object":     "video",
		"model":      model,
		"status":     "completed",
		"created_at": time.Now().Unix(),
	}
	if outURL != "" {
		body["url"] = outURL
		body["data"] = []map[string]any{{"url": outURL}}
	}
	handlerutil.WriteJSON(w, http.StatusOK, body)
}

// writeReplicateImageResult renders a succeeded prediction as the OpenAI image
// object. Replicate models answer with one URL, a list of them, or a data URI,
// and an image endpoint returns "data" entries rather than a single url.
func writeReplicateImageResult(w http.ResponseWriter, model string, pred *replicatePrediction, n int) {
	urls := replicateOutputURLs(pred.Output)
	if n > 0 && len(urls) > n {
		urls = urls[:n]
	}
	data := make([]map[string]any, 0, len(urls))
	for _, u := range urls {
		if strings.HasPrefix(u, "data:image/") {
			// Some Flux builds return a data URI instead of a delivery URL.
			// Splitting it fills the OpenAI b64_json field the callers already
			// read, and the full URI stays in url so nothing is lost.
			if _, b64, ok := strings.Cut(u, ","); ok {
				data = append(data, map[string]any{"b64_json": b64, "url": u})
				continue
			}
		}
		data = append(data, map[string]any{"url": u})
	}
	body := map[string]any{
		"id":         pred.ID,
		"object":     "image",
		"model":      model,
		"status":     "completed",
		"created_at": time.Now().Unix(),
		"data":       data,
	}
	if len(data) > 0 {
		if first, ok := data[0]["url"].(string); ok {
			body["url"] = first
		}
	}
	handlerutil.WriteJSON(w, http.StatusOK, body)
}

// replicateOutputURLs flattens an output into every URL it carries, keeping
// order. Image models routinely return several; video models return one.
func replicateOutputURLs(output any) []string {
	switch v := output.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, replicateOutputURLs(item)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"url", "image", "output", "mp4", "video"} {
			if raw, ok := v[key]; ok {
				if found := replicateOutputURLs(raw); len(found) > 0 {
					return found
				}
			}
		}
	}
	return nil
}

// replicateOutputURL is the first URL an output carries. Delegating to
// replicateOutputURLs keeps one definition of the shapes: a video model that
// answers with a list and an image model that answers with a map are the same
// parser, and the two call sites cannot drift apart.
func replicateOutputURL(output any) string {
	urls := replicateOutputURLs(output)
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}
