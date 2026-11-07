package media

// Cloudflare Workers AI image lane (upstream parity: decolua/9router
// open-sse/handlers/imageProviders/cloudflareAi.js). Every cloudflare-ai/cf
// image model answers POST /accounts/<accountId>/ai/run/<model>, never the
// OpenAI-shaped /ai/v1/images/generations the generic forwarder dials — that
// was the "No route for that URI" 7000/7003 regression on flux-2-klein-9b
// (issue #864). FLUX.2 models require multipart/form-data even for text-only
// prompts; older text-to-image models take a JSON body. Response is normalized
// to the OpenAI images shape the client already expects.

import (
	"bytes"
	"context"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nox9go/proxy/internal/constants"
	"nox9go/proxy/internal/handlers/chat"
	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/providers"
	"nox9go/proxy/internal/proxy"
)

// cloudflareMultipartModels are the FLUX.2 ids that reject JSON and require a
// multipart/form-data body (even when no reference image is supplied).
var cloudflareMultipartModels = map[string]bool{
	"@cf/black-forest-labs/flux-2-dev":      true,
	"@cf/black-forest-labs/flux-2-klein-4b": true,
	"@cf/black-forest-labs/flux-2-klein-9b": true,
}

// cloudflareOptionalFields ride through to the run endpoint verbatim when
// present and non-empty, mirroring upstream's OPTIONAL_FIELDS list.
var cloudflareOptionalFields = []string{
	"negative_prompt",
	"guidance",
	"seed",
	"num_steps",
	"steps",
	"strength",
}

// isCloudflareImageModel reports whether info resolves to the cloudflare-ai
// provider on an image endpoint.
func isCloudflareImageModel(info *chat.ModelInfo) bool {
	if info == nil {
		return false
	}
	return providers.ResolveAlias(info.Provider) == "cloudflare-ai" ||
		providers.ResolveAlias(info.Provider) == "cf"
}

type cloudflareImageReq struct {
	Prompt    string   `json:"prompt"`
	Size      string   `json:"size"`
	Width     any      `json:"width"`
	Height    any      `json:"height"`
	Image     string   `json:"image"`
	Images    []string `json:"images"`
	MaskImage string   `json:"mask_image"`
	Mask      string   `json:"mask"`
}

// handleCloudflareImage serves /v1/images/generations (and /images/edits) for
// cloudflare-ai/cf, posting to /ai/run/<model> with the model-appropriate body
// shape and returning a normalized OpenAI images response.
func (h *MediaHandler) handleCloudflareImage(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo) error {
	var req cloudflareImageReq
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("parse cloudflare image body: %w", err)
	}

	conn, connData, providerCfg, apiKey, err := h.resolveCloudflareConnection(r.Context(), modelInfo)
	if err != nil {
		return err
	}

	targetURL := cloudflareRunURL(providerCfg.BaseURL, connData, modelInfo.Model)
	payload, contentType, err := h.buildCloudflarePayload(r.Context(), &req, modelInfo.Model, body)
	if err != nil {
		return err
	}

	upstreamReq, err := buildCloudflareUpstreamRequest(r.Context(), targetURL, payload, contentType, apiKey, providerCfg)
	if err != nil {
		return err
	}

	client, clientErr := h.ChatH.GetClientForConnection(connData)
	if clientErr != nil {
		return clientErr
	}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream cloudflare image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("read cloudflare image response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		w.Header().Set(constants.HeaderContentType, resp.Header.Get(constants.HeaderContentType))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(raw)
		return nil
	}

	normalized, err := normalizeCloudflareImageResponse(raw)
	if err != nil {
		return err
	}
	w.Header().Set(constants.HeaderContentType, constants.ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(normalized)
	if conn != nil {
		_ = h.Repo.UpdateConnectionLastUsed(conn.ID)
	}
	return nil
}

func (h *MediaHandler) resolveCloudflareConnection(ctx context.Context, modelInfo *chat.ModelInfo) (*chat.ProviderConnection, *chat.ConnectionData, *providers.ProviderConfig, string, error) {
	conn, connData, err := h.ChatH.GetBestConnectionWithContext(ctx, modelInfo.Provider, modelInfo.ConnectionID, nil, modelInfo.Model)
	if err != nil || connData == nil {
		return nil, nil, nil, "", fmt.Errorf("%s", mediaConnectionError(err, modelInfo.Provider))
	}
	providerCfg, err := h.ChatH.GetProviderConfig(modelInfo.Provider, connData)
	if err != nil {
		return nil, nil, nil, "", err
	}
	apiKey := chat.ExtractAPIKey(connData)
	if apiKey == "" && providerCfg != nil && providerCfg.DefaultAPIKey != "" {
		apiKey = providerCfg.DefaultAPIKey
	}
	if apiKey == "" {
		return nil, nil, nil, "", errors.New("no API key found")
	}
	return conn, connData, providerCfg, apiKey, nil
}

// cloudflareRunURL strips the chat completions suffix back to the account root
// and appends /ai/run/<model>.
func cloudflareRunURL(providerBaseURL string, connData *chat.ConnectionData, model string) string {
	base := strings.TrimRight(providerBaseURL, "/")
	if connData != nil && connData.BaseURL != "" {
		base = strings.TrimRight(connData.BaseURL, "/")
	}
	base = strings.TrimSuffix(base, "/chat/completions")
	base = strings.TrimSuffix(base, "/v1")
	base = strings.TrimSuffix(base, "/ai")
	return base + "/ai/run/" + model
}

func (h *MediaHandler) buildCloudflarePayload(ctx context.Context, req *cloudflareImageReq, model string, body []byte) ([]byte, string, error) {
	reference, err := h.resolveOptionalImage(ctx, pickImage(req.Image, req.Images))
	if err != nil {
		return nil, "", err
	}
	maskRaw := pickImage(req.MaskImage, nil)
	if req.Mask != "" {
		maskRaw = req.Mask
	}
	maskStr, err := h.resolveOptionalImage(ctx, maskRaw)
	if err != nil {
		return nil, "", err
	}

	width, height := cloudflareDimensions(req.Size, req.Width, req.Height)
	// Cloudflare's FLUX.1 Schnell schema is closed: width/height/size reject
	// the request (upstream #5373). FLUX.2 and Stable Diffusion accept them.
	if strings.HasSuffix(model, "flux-1-schnell") {
		width, height = 0, 0
	}

	if cloudflareMultipartModels[model] {
		return buildCloudflareMultipart(req.Prompt, width, height, reference, maskStr, body)
	}
	payload, err := buildCloudflareJSON(req.Prompt, width, height, reference, maskStr, body)
	return payload, constants.ContentTypeJSON, err
}

func (h *MediaHandler) resolveOptionalImage(ctx context.Context, input string) (string, error) {
	if input != "" && (strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://")) {
		return h.resolveImageInput(ctx, input)
	}
	return input, nil
}

func buildCloudflareUpstreamRequest(ctx context.Context, targetURL string, payload []byte, contentType, apiKey string, providerCfg *providers.ProviderConfig) (*http.Request, error) {
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create cloudflare image request: %w", err)
	}
	upstreamReq.Header.Set(constants.HeaderContentType, contentType)
	handlerutil.SetAuthHeader(upstreamReq, apiKey, providerCfg.AuthHeader, providerCfg.AuthScheme)
	for k, v := range providerCfg.StaticHeaders {
		if upstreamReq.Header.Get(k) == "" {
			upstreamReq.Header.Set(k, v)
		}
	}
	return upstreamReq, nil
}

// pickImage returns the first non-empty image string from the options.
func pickImage(image string, images []string) string {
	if strings.TrimSpace(image) != "" {
		return strings.TrimSpace(image)
	}
	for _, im := range images {
		if strings.TrimSpace(im) != "" {
			return strings.TrimSpace(im)
		}
	}
	return ""
}

// cloudflareDimensions extracts width/height: first from "size" ("WxH"), then
// from explicit width/height fields, mirroring upstream's getDimensions.
func cloudflareDimensions(size string, w, h any) (int, int) {
	if wi, hi, ok := parseSizeDimensions(size); ok {
		return wi, hi
	}
	if wi, ok := toInt(w); ok {
		if hi, ok := toInt(h); ok {
			return wi, hi
		}
	}
	return 0, 0
}

func parseSizeDimensions(size string) (int, int, bool) {
	if size == "" {
		return 0, 0, false
	}
	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	wi, errW := strconv.Atoi(parts[0])
	hi, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil {
		return 0, 0, false
	}
	return wi, hi, true
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil
	}
	return 0, false
}

// buildCloudflareJSON builds a JSON body for the older (non-FLUX.2) models,
// matching upstream's buildJsonBody: {prompt, width?, height?, image_b64?,
// mask_b64?, optional fields...}.
func buildCloudflareJSON(prompt string, width, height int, reference, mask string, originalBody []byte) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(originalBody, &raw); err != nil {
		return nil, err
	}

	req := map[string]any{"prompt": prompt}
	if width > 0 {
		req["width"] = width
	}
	if height > 0 {
		req["height"] = height
	}
	if reference != "" {
		req["image_b64"] = reference
	}
	if mask != "" {
		req["mask_b64"] = mask
	}
	for _, key := range cloudflareOptionalFields {
		if v, ok := raw[key]; ok && v != nil && v != "" {
			req[key] = v
		}
	}
	return json.Marshal(req)
}

// buildCloudflareMultipart builds a multipart/form-data body for FLUX.2 models,
// matching upstream's buildMultipartBody: prompt + dimensions + optional fields
// as named parts; image and mask as base64 parts.
func buildCloudflareMultipart(prompt string, width, height int, reference, mask string, originalBody []byte) ([]byte, string, error) {
	var raw map[string]any
	if err := json.Unmarshal(originalBody, &raw); err != nil {
		return nil, "", err
	}

	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)

	_ = mw.WriteField("prompt", prompt)
	if width > 0 {
		_ = mw.WriteField("width", strconv.Itoa(width))
	}
	if height > 0 {
		_ = mw.WriteField("height", strconv.Itoa(height))
	}
	for _, key := range cloudflareOptionalFields {
		if v, ok := raw[key]; ok && v != nil && v != "" {
			_ = mw.WriteField(key, fmt.Sprintf("%v", v))
		}
	}
	if reference != "" {
		_ = mw.WriteField("image_b64", reference)
	}
	if mask != "" {
		_ = mw.WriteField("mask_b64", mask)
	}
	_ = mw.Close()
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// normalizeCloudflareImageResponse converts Cloudflare's /ai/run/<model> response
// to the OpenAI images shape {created, data:[{b64_json}|{url}]}.
//
// Cloudflare can return:
//   - raw binary image (Content-Type: image/png) from some models
//   - JSON {result:{image:"<b64>"}} (the standard run endpoint shape)
//   - JSON {result:{data:[{b64_json}]}}  (nested data array)
//   - JSON {result:"<url>"}              (already a URL)
func normalizeCloudflareImageResponse(raw []byte) ([]byte, error) {
	if isOpenAIImageShape(raw) {
		return raw, nil
	}

	var wrapper struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return openAIImageFromBinary(raw)
	}

	result := unwrapCloudflareResult(wrapper.Result)
	created := int(time.Now().Unix())

	switch r := result.(type) {
	case map[string]any:
		if out, ok, err := marshalCloudflareResultMap(r, created); ok {
			return out, err
		}
	case string:
		return json.Marshal(map[string]any{
			"created": created,
			"data":    []map[string]string{{"url": r}},
		})
	}

	return openAIImageFromBinary(raw)
}

func isOpenAIImageShape(raw []byte) bool {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return false
	}
	if _, ok := top["created"]; !ok {
		return false
	}
	_, ok := top["data"].([]any)
	return ok
}

func unwrapCloudflareResult(result any) any {
	for {
		m, ok := result.(map[string]any)
		if !ok {
			return result
		}
		if inner, hasResult := m["result"].(map[string]any); hasResult {
			result = inner
			continue
		}
		if arr, ok := m["result"].([]any); ok && len(arr) > 0 {
			result = arr
			continue
		}
		return result
	}
}

func marshalCloudflareResultMap(r map[string]any, created int) ([]byte, bool, error) {
	if b64, ok := r["image"].(string); ok && b64 != "" {
		b, err := json.Marshal(map[string]any{
			"created": created,
			"data":    []map[string]string{{"b64_json": b64}},
		})
		return b, true, err
	}
	data, ok := r["data"].([]any)
	if !ok {
		return nil, false, nil
	}
	items := make([]map[string]string, 0, len(data))
	for _, item := range data {
		if m, ok := item.(map[string]any); ok {
			if entry := extractImageEntry(m); len(entry) > 0 {
				items = append(items, entry)
			}
		}
	}
	b, err := json.Marshal(map[string]any{"created": created, "data": items})
	return b, true, err
}

func extractImageEntry(m map[string]any) map[string]string {
	entry := map[string]string{}
	if b64, ok := m["b64_json"].(string); ok {
		entry["b64_json"] = b64
	}
	if u, ok := m["url"].(string); ok {
		entry["url"] = u
	}
	return entry
}

// openAIImageFromBinary treats raw bytes as a PNG image and returns a
// normalized OpenAI images response wrapping it as b64_json.
func openAIImageFromBinary(data []byte) ([]byte, error) {
	b64 := base64.StdEncoding.EncodeToString(data)
	return json.Marshal(map[string]any{
		"created": int(time.Now().Unix()),
		"data":    []map[string]string{{"b64_json": b64}},
	})
}

// resolveImageInput downloads a reference image URL and returns it as a
// base64-encoded string. It uses r.Context(), requires a 200 response, and
// refuses private/resolved IPs via proxy.IsPrivateIP (SSRF protection).
func (h *MediaHandler) resolveImageInput(ctx context.Context, imageURL string) (string, error) {
	u, err := url.Parse(imageURL)
	if err != nil {
		return "", fmt.Errorf("parse image url: %w", err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return "", fmt.Errorf("resolve image host %q: %w", host, err)
		}
		ip = ips[0]
	}
	if !proxy.IsPrivateNetworkingAllowed() && proxy.IsPrivateIP(ip) {
		return "", fmt.Errorf("image host %q resolves to private IP %s", host, ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", fmt.Errorf("create image request: %w", err)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image fetch returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
