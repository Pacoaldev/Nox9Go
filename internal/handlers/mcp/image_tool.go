package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"

	"nox9go/proxy/internal/providers"
)

// ImageLane is the single image-generation entry point this tool calls. It is
// the gateway's own /v1/images/generations handler, invoked in process: the
// tool needs no engine API key and opens no socket, and the lane keeps every
// routing, account-rotation and error decision it already makes for a real
// HTTP caller. Declared as an interface so a test can answer without a
// provider.
type ImageLane interface {
	HandleImages(w http.ResponseWriter, r *http.Request)
}

// imageProviders is the preference order of the providers that have a
// dedicated image executor (internal/handlers/media: antigravity, codex,
// cloudflare-ai, replicate), aliases included. Anything else answers image
// requests only through the generic forwarder, which an image model with no
// upstream OpenAI-compatible route cannot serve.
var imageProviders = []string{"antigravity", "ag", "codex", "cx", "cloudflare-ai", "cf", "replicate"}

// maxImageCandidates bounds one tool call. A gateway with ten usable accounts
// must not turn a single MCP request into ten upstream image jobs, each of
// which is billed.
const maxImageCandidates = 3

// imageCandidate is one provider/model/account the tool may ask for an image.
type imageCandidate struct {
	Provider     string
	Model        string
	ConnectionID string
	rank         int
}

// imageTool declares generate_image: the only tool that spends a provider's
// own quota, because it asks the upstream to generate an image.
func imageTool() Tool {
	return Tool{
		Name:        "generate_image",
		Description: "Generate an image from a text prompt using one of the gateway's active image-capable provider connections (antigravity, codex, cloudflare-ai, replicate). Falls through to the next capable connection when one fails. This spends the provider's own quota.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"prompt"},
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "Text description of the image to generate."},
				"model":  map[string]any{"type": "string", "description": "Model id to use. Omit to pick an image-capable model from the active connections."},
				"size":   map[string]any{"type": "string", "description": "Requested image size, e.g. 1024x1024."},
				"n":      map[string]any{"type": "integer", "description": "Number of images to request."},
			},
		},
	}
}

// generateImage resolves a candidate, calls the image lane in process and
// returns the image the provider produced.
func (h *Handler) generateImage(args map[string]any) CallToolResult {
	if h.imageGen == nil {
		return errorResult("image generation is not enabled in this build")
	}

	prompt := argumentString(args, "prompt")
	if prompt == "" {
		return errorResult("generate_image requires a prompt")
	}

	var candidates []imageCandidate
	if model := argumentString(args, "model"); model != "" {
		// An explicit model is the caller's choice: send exactly it, once, and
		// leave account selection to the lane.
		candidates = []imageCandidate{{Model: model}}
	} else {
		resolved, err := h.imageCandidates()
		if err != nil {
			return errorResult(fmt.Sprintf("failed to read connections: %v", err))
		}
		if len(resolved) == 0 {
			return errorResult("no active connection can generate an image; connect one of antigravity, codex, cloudflare-ai or replicate")
		}
		candidates = resolved
	}
	if len(candidates) > maxImageCandidates {
		candidates = candidates[:maxImageCandidates]
	}

	var lastErr error
	for _, candidate := range candidates {
		result, err := h.generateImageWith(candidate, prompt, args)
		if err == nil {
			return result
		}
		lastErr = err
	}
	return errorResult(fmt.Sprintf("every image candidate failed; last error: %v", lastErr))
}

// imageCandidates lists the active connections with a dedicated image executor,
// ordered by provider preference and then connection id, one candidate each.
func (h *Handler) imageCandidates() ([]imageCandidate, error) {
	conns, err := h.repo.GetProviderConnections("", true)
	if err != nil {
		return nil, err
	}

	rank := make(map[string]int, len(imageProviders))
	for i, provider := range imageProviders {
		rank[provider] = i
	}

	seen := map[string]struct{}{}
	out := make([]imageCandidate, 0, len(conns))
	for _, conn := range conns {
		if conn == nil {
			continue
		}
		providerRank, ok := rank[conn.Provider]
		if !ok {
			continue
		}
		model := firstImageModel(conn.Provider)
		if model == "" {
			continue
		}
		key := conn.ID + "|" + model
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, imageCandidate{Provider: conn.Provider, Model: model, ConnectionID: conn.ID, rank: providerRank})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].ConnectionID < out[j].ConnectionID
	})
	return out, nil
}

// firstImageModel returns the provider's first catalog model declared as an
// image model, or "" when the catalog lists none.
func firstImageModel(provider string) string {
	for _, model := range providers.ProviderModels[provider] {
		if providers.GetProviderModelKind(provider, model) == "image" {
			return model
		}
	}
	return ""
}

// imageLaneRequest is the body the lane receives: the OpenAI images shape with
// the optional fields omitted when the caller did not ask for them.
type imageLaneRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Size   string `json:"size,omitempty"`
	N      int    `json:"n,omitempty"`
}

// imageLaneAnswer is the part of an images response this tool reads.
type imageLaneAnswer struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
}

// generateImageWith asks one candidate for an image. The lane is called in
// process with the chosen connection pinned as a header, so the account the
// tool names in its answer is the account the lane used.
func (h *Handler) generateImageWith(candidate imageCandidate, prompt string, args map[string]any) (CallToolResult, error) {
	// The lane resolves a model through the same parser every media route uses,
	// and that parser splits on the first "/" to find the provider. A bare
	// catalog id such as Cloudflare's "@cf/black-forest-labs/flux-2-klein-9b"
	// is therefore read as provider "@cf", which then collides with the pinned
	// connection's real provider (chat/connections.go: "connection X belongs to
	// provider Y, not @cf"). Sending the wire syntax "provider/model" is what
	// makes the pin and the model agree.
	payload, err := json.Marshal(imageLaneRequest{
		Model:  laneModel(candidate),
		Prompt: prompt,
		Size:   argumentString(args, "size"),
		N:      argumentInt(args, "n"),
	})
	if err != nil {
		return CallToolResult{}, fmt.Errorf("encode request: %w", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if candidate.ConnectionID != "" {
		req.Header.Set("x-connection-id", candidate.ConnectionID)
	}
	rec := httptest.NewRecorder()
	h.imageGen.HandleImages(rec, req)

	subject := candidate.Model
	if candidate.Provider != "" {
		subject = candidate.Provider + "/" + candidate.Model
	}
	if rec.Code != http.StatusOK {
		return CallToolResult{}, fmt.Errorf("%s: %s", subject, laneMessage(rec.Body.Bytes()))
	}

	var answer imageLaneAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		return CallToolResult{}, fmt.Errorf("%s: unreadable lane answer: %w", subject, err)
	}
	if len(answer.Data) == 0 {
		return CallToolResult{}, fmt.Errorf("%s: lane returned no image", subject)
	}

	if b64 := answer.Data[0].B64JSON; b64 != "" {
		result := imageResult(imageMimeType(b64), b64)
		result.Content = append(result.Content, Content{Type: "text", Text: imageDescription(candidate)})
		return result, nil
	}
	if url := answer.Data[0].URL; url != "" {
		return textResult(imageDescription(candidate) + "\n" + url), nil
	}
	return CallToolResult{}, fmt.Errorf("%s: lane returned neither image data nor a URL", subject)
}

// imageMimeType names the format of a base64 image payload from its leading
// magic bytes. It exists because the lane does not tell us the format and the
// providers disagree: Cloudflare's FLUX answers JPEG while the Gemini and
// OpenAI lanes answer PNG. Claiming one of them unconditionally made the block
// lie about its own payload, which is the one thing an MCP client consults
// before handing the bytes to a renderer. Unrecognised bytes degrade to
// application/octet-stream rather than guessing an image type.
func imageMimeType(b64 string) string {
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// Some lanes emit raw base64 without padding.
		decoded, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			return "application/octet-stream"
		}
	}
	return http.DetectContentType(decoded)
}

// laneModel renders the candidate the way the media lane expects to receive a
// model: the wire syntax "provider/model". A candidate with no provider (the
// caller named a model explicitly and left account selection to the lane) is
// forwarded untouched, because that string is already the caller's wire form.
func laneModel(candidate imageCandidate) string {
	if candidate.Provider == "" {
		return candidate.Model
	}
	return candidate.Provider + "/" + candidate.Model
}

// imageDescription names what produced the image, which is what the caller
// needs to know when the fallback chain picked a different provider than the
// first one.
func imageDescription(candidate imageCandidate) string {
	subject := candidate.Model
	if candidate.Provider != "" {
		subject = candidate.Provider + "/" + candidate.Model
	}
	if candidate.ConnectionID == "" {
		return fmt.Sprintf("Generated an image with %s.", subject)
	}
	return fmt.Sprintf("Generated an image with %s on connection %s.", subject, candidate.ConnectionID)
}

// laneMessage extracts the human-readable failure from a lane error body,
// falling back to the raw body when it is not the gateway's JSON error shape.
func laneMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		return payload.Error.Message
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return text
	}
	return "the image lane refused the request"
}

// argumentString reads one string tool argument.
func argumentString(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

// argumentInt reads one integer tool argument, which JSON-RPC delivers as a
// float64.
func argumentInt(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	}
	return 0
}
