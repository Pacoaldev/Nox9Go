package headroom

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/proxy"
)

// DefaultTimeoutMs is the default timeout in milliseconds for compression requests,
// matching the default HeadroomTimeoutMs from settings.
const DefaultTimeoutMs = 3000

// HeadroomClient handles in-flight request compression via the Headroom proxy.
type HeadroomClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new HeadroomClient. If baseURL is empty, DefaultURL is used.
// If httpClient is nil, a client configured with proxy.NewSafeTransport is used.
func NewClient(baseURL string, apiKey string, httpClient *http.Client) *HeadroomClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultURL
	}
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: proxy.NewSafeTransport(),
		}
	}
	return &HeadroomClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: httpClient,
	}
}

// NewHeadroomClient is an alias for NewClient.
func NewHeadroomClient(baseURL string, apiKey string, httpClient *http.Client) *HeadroomClient {
	return NewClient(baseURL, apiKey, httpClient)
}

// Compress sends messages to Headroom's /v1/compress endpoint to perform in-flight compression.
// It strictly validates structural integrity (message count, role order, tool call IDs, functions,
// thinking blocks, images), guards against CCR and phantom savings, and safely fails open
// on network/timeout errors while respecting downstream context cancellation (G03).
func (c *HeadroomClient) Compress(ctx context.Context, body []byte, model string, timeoutMs int) ([]byte, bool, error) {
	// Downstream cancellation stops dispatch immediately (G03)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}

	if len(body) == 0 {
		return body, false, nil
	}

	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return body, false, nil // fail-open
	}

	rawMessages, ok := reqMap["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return body, false, nil
	}

	// A body carrying an error result or a tool the compressor was never cleared
	// for is not compressed at all. Checked here, before any network call, so the
	// proxy never reads content the gateway decided is not its to shorten.
	if unsafeForCompression(reqMap) {
		return body, false, nil
	}

	if model == "" {
		if m, ok := reqMap["model"].(string); ok {
			model = m
		}
	}

	compressReq := map[string]any{
		"messages": rawMessages,
		"model":    model,
		"config": map[string]any{
			"mode": "lossy_inline",
		},
	}

	payloadBytes, err := json.Marshal(compressReq)
	if err != nil {
		return body, false, nil
	}

	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	endpoint := c.baseURL + "/v1/compress"

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	if IsLoopbackHeadroomUrl(c.baseURL) {
		reqCtx = proxy.WithAllowLoopback(reqCtx)
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return body, false, nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		// Compressor timeout or network error fails open safely
		return body, false, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return body, false, nil
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return body, false, nil
	}

	// Deep byte scan: CCR markers are strictly forbidden
	if bytes.Contains(respBytes, []byte("<<ccr:")) {
		return body, false, nil
	}

	type compressResponse struct {
		Messages           []any    `json:"messages"`
		TokensBefore       any      `json:"tokens_before,omitempty"`
		TokensAfter        any      `json:"tokens_after,omitempty"`
		TokensSaved        any      `json:"tokens_saved,omitempty"`
		CompressionSkipped bool     `json:"compression_skipped,omitempty"`
		SkipReason         string   `json:"skip_reason,omitempty"`
		CcrHashes          []string `json:"ccr_hashes,omitempty"`
	}

	var compResp compressResponse
	if err := json.Unmarshal(respBytes, &compResp); err != nil {
		return body, false, nil
	}

	if compResp.CompressionSkipped {
		return body, false, nil
	}

	if len(compResp.CcrHashes) > 0 {
		return body, false, nil
	}

	if compResp.TokensSaved != nil {
		if saved, ok := toFloat64(compResp.TokensSaved); ok && saved <= 0 {
			return body, false, nil
		}
	}

	if len(compResp.Messages) == 0 {
		return body, false, nil
	}

	// Permitted-region diff validation
	if !validateMessagesPreserved(rawMessages, compResp.Messages) {
		return body, false, nil
	}

	reqMap["messages"] = compResp.Messages
	newBody, err := json.Marshal(reqMap)
	if err != nil {
		return body, false, nil
	}

	// Byte-gain guard: a result that is not meaningfully smaller is not worth the
	// risk of a lossy round trip. The proxy reports its own token counts, but
	// this compares the bytes actually about to be sent, so a proxy that reports
	// phantom savings cannot talk the gateway into committing them.
	//
	// Upstream db81a8c9 (#4855): check both messageBytes and bodyBytes so that
	// large unchanged tool schemas dominating the outbound body do not cause
	// false phantom-savings rejections when messages shrank meaningfully.
	rawMsgBytes, _ := json.Marshal(rawMessages)
	compMsgBytes, _ := json.Marshal(compResp.Messages)
	savedTokens := 0
	if compResp.TokensSaved != nil {
		if s, ok := toFloat64(compResp.TokensSaved); ok {
			savedTokens = int(s)
		}
	}
	diag := &HeadroomDiagnostics{
		Before: HeadroomSizes{
			BodyBytes:    len(body),
			MessageBytes: len(rawMsgBytes),
		},
		After: HeadroomSizes{
			BodyBytes:    len(newBody),
			MessageBytes: len(compMsgBytes),
		},
	}
	stats := &HeadroomStats{TokensSaved: savedTokens}

	if IsHeadroomPhantomSavings(stats, diag) {
		log.Warn("headroom", FormatHeadroomPhantomWarning(diag))
	}

	// Byte-gain guard: a result that is not meaningfully smaller is not worth the
	// risk of a lossy round trip. The proxy reports its own token counts, but
	// this compares the bytes actually about to be sent, so a proxy that reports
	// phantom savings cannot talk the gateway into committing them.
	if len(newBody) >= len(body)*95/100 {
		return body, false, nil
	}

	return newBody, true, nil
}

// validateMessagesPreserved verifies that candidate messages preserve all inviolable structural
// regions: message count, role ordering, tool call IDs, function signatures, thinking blocks, and images.
func validateMessagesPreserved(srcMsgs, candMsgs []any) bool {
	if len(candMsgs) != len(srcMsgs) {
		return false
	}

	for i := range srcMsgs {
		src, ok1 := srcMsgs[i].(map[string]any)
		cand, ok2 := candMsgs[i].(map[string]any)
		if !ok1 || !ok2 {
			return false
		}

		// 1. Role preservation
		if fmt.Sprintf("%v", src["role"]) != fmt.Sprintf("%v", cand["role"]) {
			return false
		}

		// 2. Tool call ID preservation
		if src["tool_call_id"] != nil || cand["tool_call_id"] != nil {
			if fmt.Sprintf("%v", src["tool_call_id"]) != fmt.Sprintf("%v", cand["tool_call_id"]) {
				return false
			}
		}

		// 3. Tool calls preservation
		srcTC, srcHasTC := src["tool_calls"].([]any)
		candTC, candHasTC := cand["tool_calls"].([]any)
		if srcHasTC != candHasTC {
			return false
		}
		if srcHasTC {
			if len(srcTC) != len(candTC) {
				return false
			}
			for j := range srcTC {
				sCall, sOk := srcTC[j].(map[string]any)
				cCall, cOk := candTC[j].(map[string]any)
				if !sOk || !cOk {
					return false
				}
				if fmt.Sprintf("%v", sCall["id"]) != fmt.Sprintf("%v", cCall["id"]) {
					return false
				}
				if fmt.Sprintf("%v", sCall["type"]) != fmt.Sprintf("%v", cCall["type"]) {
					return false
				}
				sFn, _ := sCall["function"].(map[string]any)
				cFn, _ := cCall["function"].(map[string]any)
				if sFn == nil || cFn == nil {
					return false
				}
				if fmt.Sprintf("%v", sFn["name"]) != fmt.Sprintf("%v", cFn["name"]) {
					return false
				}
				if fmt.Sprintf("%v", sFn["arguments"]) != fmt.Sprintf("%v", cFn["arguments"]) {
					return false
				}
			}
		}

		// 4. Reasoning & Thinking fields preservation
		for _, key := range []string{"reasoning_content", "thought", "thought_signature", "thoughtSignature"} {
			if src[key] != nil || cand[key] != nil {
				if fmt.Sprintf("%v", src[key]) != fmt.Sprintf("%v", cand[key]) {
					return false
				}
			}
		}

		// 5. Content structure and protected blocks preservation
		if !validateContentBlocks(src["content"], cand["content"]) {
			return false
		}
	}

	return true
}

// validateContentBlocks ensures non-text blocks (thinking, images, tools) are preserved verbatim.
func validateContentBlocks(srcContent, candContent any) bool {
	if srcContent == nil && candContent == nil {
		return true
	}
	if srcContent == nil {
		return candContent == "" || candContent == nil
	}

	// Case A: string content
	if _, ok := srcContent.(string); ok {
		_, cOk := candContent.(string)
		return cOk
	}

	// Case B: array of content blocks
	srcArr, ok := srcContent.([]any)
	if !ok {
		// Non-array and non-string: fallback to exact equality
		return reflect.DeepEqual(srcContent, candContent)
	}

	candArr, cOk := candContent.([]any)
	if !cOk || len(srcArr) != len(candArr) {
		return false
	}

	for k := range srcArr {
		sBlock, sOk := srcArr[k].(map[string]any)
		cBlock, cOk := candArr[k].(map[string]any)
		if !sOk || !cOk {
			if !reflect.DeepEqual(srcArr[k], candArr[k]) {
				return false
			}
			continue
		}

		sType := fmt.Sprintf("%v", sBlock["type"])
		cType := fmt.Sprintf("%v", cBlock["type"])
		if sType != cType {
			return false
		}

		switch sType {
		case "thinking", "redacted_thinking":
			if !reflect.DeepEqual(sBlock, cBlock) {
				return false
			}
		case "image", "image_url", "document", "inlineData":
			if !reflect.DeepEqual(sBlock, cBlock) {
				return false
			}
		case "tool_use":
			if fmt.Sprintf("%v", sBlock["id"]) != fmt.Sprintf("%v", cBlock["id"]) ||
				fmt.Sprintf("%v", sBlock["name"]) != fmt.Sprintf("%v", cBlock["name"]) {
				return false
			}
			if !reflect.DeepEqual(sBlock["input"], cBlock["input"]) {
				return false
			}
		case "tool_result":
			if fmt.Sprintf("%v", sBlock["tool_use_id"]) != fmt.Sprintf("%v", cBlock["tool_use_id"]) {
				return false
			}
			if fmt.Sprintf("%v", sBlock["is_error"]) != fmt.Sprintf("%v", cBlock["is_error"]) {
				return false
			}
			if !validateContentBlocks(sBlock["content"], cBlock["content"]) {
				return false
			}
		case "text":
			sText, _ := sBlock["text"].(string)
			cText, _ := cBlock["text"].(string)
			// A block that had text and comes back empty is a deletion dressed as
			// a compression. Checking only for nil let "" through.
			if sText != "" && cText == "" {
				return false
			}
		default:
			if !reflect.DeepEqual(sBlock, cBlock) {
				return false
			}
		}
	}

	return true
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}
