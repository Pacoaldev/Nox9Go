package executor

import (
	"bytes"
	"crypto/sha1"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"nox9go/proxy/internal/constants"
	"nox9go/proxy/internal/providers"
	"nox9go/proxy/internal/proxy"

	"github.com/google/uuid"
)

const codebuddySystemPrompt = "You are CodeBuddy Code."

var channelMarkers = []string{
	"You are Claude Code, Anthropic's official CLI for Claude.",
	"x-anthropic-billing-header",
	"Main branch (you will usually use this for PRs):",
}

var channelMarkerRegex = regexp.MustCompile(`(?i)you are claude code|claude.?code.+official.+cli|anthropic.+official.+cli|anxthxropic.+official.+cli|you are (?:cursor|windsurf|cline|aider|continue|copilot|cody)|cc_entrypoint\s*=\s*(?:cli|vscode|jetbrains|gui)|claude.?code.+issues|give feedback.+claude.?code|OhMyOpenCode`)

var codebuddyRateLimitRegex = regexp.MustCompile(`(?i)超出频率限制|frequency limit|限额`)

// codebuddyChannelGateRegex matches the channel-verification variants of code
// 11128. The code is overloaded upstream: it also carries "first message is not
// system prompt", a body-shape problem, so the message decides and never the
// code alone.
var codebuddyChannelGateRegex = regexp.MustCompile(`(?i)conversation id missing|request illegal`)

// readHeaderCI reads a header by name case-insensitively. Header names are
// case-insensitive on the wire and Go canonicalizes them when they are written
// to the request, so a caller's "x-conversation-id" and a generated
// "X-Conversation-ID" are one header: whichever is set last wins and the other
// is silently lost. Finding the caller's key first is what keeps its id.
func readHeaderCI(headers map[string]string, name string) string {
	if headers == nil {
		return ""
	}
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// writeHeaderCI stores a header under its canonical name, dropping a
// differently-cased key that would otherwise be a duplicate.
func writeHeaderCI(headers map[string]string, name, value string) {
	for k := range headers {
		if k != name && strings.EqualFold(k, name) {
			delete(headers, k)
		}
	}
	headers[name] = value
}

// ForwardCodebuddyCN forwards to Tencent CodeBuddy with force-stream
// and reasoning_summary injection.
//
// CodeBuddy is OpenAI-compatible but rejects non-stream chat requests
// (HTTP 400, code 11101 "Non-stream chat request is currently not supported").
// This executor forces stream=true in the request body, mirroring the
// JS version's CodeBuddyExecutor.transformRequest behaviour.
//
// It also handles reasoning_effort:
//   - "none"/"off" → stripped (CodeBuddy gateway has no "none")
//   - any other value → injects reasoning_summary="auto" so CodeBuddy
//     surfaces the model's reasoning output.
func ForwardCodebuddyCN(w http.ResponseWriter, req *Request) error {
	body, err := transformCodebuddyBody(req.Body)
	if err != nil {
		return fmt.Errorf("transform codebuddy body: %w", err)
	}

	ctx := req.Ctx
	cfg := req.Config
	if cfg != nil {
		merged := make(map[string]string, len(cfg.StaticHeaders)+25)
		for k, v := range cfg.StaticHeaders {
			merged[k] = v
		}
		delete(merged, "x-requested-with")
		delete(merged, "x-codebuddy-request")

		generated := codebuddyHeaders(req, cfg, body)
		for k, v := range generated {
			if codebuddyIsConversationIdentityHeader(k) {
				continue
			}
			merged[k] = v
		}
		// The conversation identity belongs to the caller when it already sent
		// one; the generated ids fill only what is absent.
		mergeCodebuddyConversationHeaders(merged, generated)
		cfgCopy := *cfg
		cfgCopy.StaticHeaders = merged
		cfg = &cfgCopy
	}

	// Always forward as stream — CodeBuddy rejects stream=false
	resp, err := proxy.ForwardOpenAI(ctx, req.Client, cfg, req.APIKey, body, true)
	if err != nil {
		if msg, ok := codebuddyChannelGateMessage(upstreamErrorBody(err)); ok {
			return proxy.UpstreamFailure(http.StatusBadRequest, msg)
		}
		var ue *proxy.UpstreamError
		if errors.As(err, &ue) && isCodebuddyRateLimit(ue.Body) {
			ue.StatusCode = http.StatusTooManyRequests
		}
		return fmt.Errorf("ForwardCodebuddyCN upstream: %w", err)
	}
	defer resp.Body.Close()

	forwardUpstreamResponseHeaders(w, resp.Header)

	if req.IsStream {
		if !isEventStreamResponse(resp.Header) {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, constants.MaxUpstreamBodyBytes))
			if rerr != nil {
				return fmt.Errorf("read codebuddy upstream body: %w", rerr)
			}
			if !proxy.LooksLikeSSE(data) {
				if isCodebuddyRateLimit(data) {
					return proxy.UpstreamFailure(http.StatusTooManyRequests, "CodeBuddy frequency limit (6004)")
				}
				return jsonResponse(req.Ctx, w, bytes.NewReader(data), req.TranslateResp, req.ResponseBuf)
			}
			resp.Body = io.NopCloser(bytes.NewReader(data))
		}
		stallReader := proxy.NewStallReaderWithContext(req.Ctx, resp.Body, 0, "codebuddy-cn")
		defer stallReader.Close() // stops the shutdown watcher + stall timer
		return execSSEStream(w, stallReader, req)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, constants.MaxUpstreamBodyBytes))
	if err != nil {
		return fmt.Errorf("read codebuddy response: %w", err)
	}
	if converted, ok := sseToOpenAIJSON(data); ok {
		data = converted
	} else if isCodebuddyRateLimit(data) {
		return proxy.UpstreamFailure(http.StatusTooManyRequests, "CodeBuddy frequency limit (6004)")
	}
	return jsonResponse(req.Ctx, w, bytes.NewReader(data), req.TranslateResp, req.ResponseBuf)
}

func sanitizeChannelMarkers(text string) string {
	if text == "" {
		return text
	}
	result := text
	for _, marker := range channelMarkers {
		if strings.Contains(result, marker) {
			result = strings.ReplaceAll(result, marker, "[external-client-identity]")
		}
	}
	return channelMarkerRegex.ReplaceAllString(result, "[external-client-identity]")
}

func extractMessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, b := range v {
			if s, ok := b.(string); ok {
				parts = append(parts, s)
			} else if bm, ok := b.(map[string]any); ok {
				if t, ok := bm["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func copyMap(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func isCodebuddyRateLimit(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var data struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return false
	}
	msg := data.Msg
	if msg == "" {
		msg = data.Message
	}
	if msg == "" {
		msg = data.Error.Message
	}
	return data.Code == 6004 || codebuddyRateLimitRegex.MatchString(msg)
}

// transformCodebuddyBody forces stream=true, normalizes system prompt and caller
// instructions, sanitizes external client markers, and handles reasoning params.
func transformCodebuddyBody(body []byte) ([]byte, error) {
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}

	// Force stream — CodeBuddy rejects non-stream (HTTP 400, code 11101)
	reqMap["stream"] = true

	// Handle reasoning_effort / reasoning_summary
	if eff, ok := reqMap["reasoning_effort"].(string); ok {
		switch eff {
		case "none", "off":
			delete(reqMap, "reasoning_effort")
		default:
			reqMap["reasoning_summary"] = "auto"
		}
	}

	var inputMessages []any
	if arr, ok := reqMap["messages"].([]any); ok {
		inputMessages = arr
	}

	systemParts := []string{codebuddySystemPrompt}
	var otherMessages []any

	for _, raw := range inputMessages {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role == "system" || role == "developer" {
			rawText := extractMessageText(m["content"])
			cleaned := strings.TrimSpace(sanitizeChannelMarkers(rawText))
			if cleaned != "" && cleaned != codebuddySystemPrompt {
				systemParts = append(systemParts, cleaned)
			}
			continue
		}
		if role == "user" {
			if s, ok := m["content"].(string); ok {
				cp := copyMap(m)
				cp["content"] = []any{map[string]any{"type": "text", "text": s}}
				otherMessages = append(otherMessages, cp)
				continue
			}
		}
		if role == "assistant" {
			cp := copyMap(m)
			switch v := cp["content"].(type) {
			case string:
				cp["content"] = sanitizeChannelMarkers(v)
			case []any:
				var newBlocks []any
				for _, b := range v {
					if bm, ok := b.(map[string]any); ok {
						if t, ok := bm["text"].(string); ok {
							cpBlock := copyMap(bm)
							cpBlock["text"] = sanitizeChannelMarkers(t)
							newBlocks = append(newBlocks, cpBlock)
							continue
						}
					}
					newBlocks = append(newBlocks, b)
				}
				cp["content"] = newBlocks
			}
			otherMessages = append(otherMessages, cp)
			continue
		}
		otherMessages = append(otherMessages, m)
	}

	leadingSystem := map[string]any{
		"role":    "system",
		"content": strings.Join(systemParts, "\n\n"),
	}
	messages := make([]any, 0, len(otherMessages)+1)
	messages = append(messages, leadingSystem)
	messages = append(messages, otherMessages...)
	reqMap["messages"] = messages

	return json.Marshal(reqMap)
}

func codebuddyHeaders(req *Request, cfg *providers.ProviderConfig, body []byte) map[string]string {
	reqID := strings.ReplaceAll(uuid.NewString(), "-", "")
	convReqID := strings.ReplaceAll(uuid.NewString(), "-", "")

	sessionKey := req.SessionID
	if sessionKey == "" && req.ConnData != nil {
		if m, ok := req.ConnData["sessionId"].(string); ok {
			sessionKey = m
		}
	}
	// The identifiers the client already tracks — `session_id`,
	// `prompt_cache_key`, `conversation_id` — keep a conversation's id stable
	// even when the client sends no session header. The opening message is the
	// last resort: two different conversations that start with the same words
	// would otherwise share one id.
	bodyKey, bodyUserText := codebuddyBodyKeys(body)
	if sessionKey == "" {
		sessionKey = bodyKey
	}
	if sessionKey == "" {
		sessionKey = bodyUserText
	}
	var convID string
	if sessionKey != "" {
		convID = stableConversationUUID("codebuddy:" + sessionKey)
	} else {
		convID = uuid.NewString()
	}

	domain := "www.codebuddy.ai"
	if req.ConnData != nil {
		if d, ok := req.ConnData["domain"].(string); ok && d != "" {
			domain = d
		}
	}
	if domain == "www.codebuddy.ai" && cfg != nil && (strings.Contains(cfg.BaseURL, "tencent.com") || strings.Contains(cfg.BaseURL, "copilot.")) {
		domain = "copilot.tencent.com"
	}

	return map[string]string{
		"Content-Type":                "application/json; charset=utf-8",
		"User-Agent":                  "CLI/2.108.1 CodeBuddy/2.108.1",
		"X-Product":                   "SaaS",
		"X-App":                       "cli",
		"X-IDE-Type":                  "CLI",
		"X-IDE-Name":                  "CLI",
		"X-IDE-Version":               "2.108.1",
		"X-Private-Data":              "false",
		"X-Requested-With":            "XMLHttpRequest",
		"X-CodeBuddy-Request":         "1",
		"X-Domain":                    domain,
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Runtime-Version": "v24.11.1",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Arch":            "x64",
		"X-Stainless-Os":              "Windows",
		"X-Stainless-Package-Version": "6.25.0",
		"X-Stainless-Helper-Method":   "stream",
		"X-Stainless-Retry-Count":     "0",
		"X-Request-ID":                reqID,
		"X-Conversation-ID":           convID,
		"X-Conversation-Request-ID":   convReqID,
		"X-Conversation-Message-ID":   reqID,
		"X-Agent-Intent":              "craft",
		"X-Agent-Purpose":             "conversation",
	}
}

func codebuddyConversationHeaders(req *Request, body []byte) map[string]string {
	return codebuddyHeaders(req, nil, body)
}

// codebuddyConversationHeaderNames are the identity headers the gateway's
// channel verification demands, in the order the official client sends them.
var codebuddyConversationHeaderNames = []string{
	"X-Conversation-ID",
	"X-Conversation-Request-ID",
	"X-Conversation-Message-ID",
	"X-Request-ID",
}

// codebuddyIsConversationIdentityHeader reports whether name belongs to the
// caller-owned conversation identity family. Those are merged separately, so a
// caller tracking its own conversation keeps it.
func codebuddyIsConversationIdentityHeader(name string) bool {
	for _, n := range codebuddyConversationHeaderNames {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// upstreamErrorBody returns the response body an upstream failure carries, or
// nil when the error did not come from a response.
func upstreamErrorBody(err error) []byte {
	var ue *proxy.UpstreamError
	if errors.As(err, &ue) {
		return ue.Body
	}
	return nil
}

// mergeCodebuddyConversationHeaders writes the generated identity headers into
// merged without discarding what the caller already sent. A connection whose
// stored headers carry `X-Conversation-ID` is tracking its own conversation,
// and overwriting it would restart that conversation on every turn.
//
// Matching is case-insensitive and the canonical casing is restored, so the
// caller's value is found before anything writes over it.
//
// `X-Conversation-Message-ID` and `X-Request-ID` mirror each other, so when the
// caller supplied only one of them both are filled from it.
func mergeCodebuddyConversationHeaders(merged, generated map[string]string) {
	messageID := readHeaderCI(merged, "X-Conversation-Message-ID")
	if messageID == "" {
		messageID = readHeaderCI(merged, "X-Request-ID")
	}
	if messageID == "" {
		messageID = generated["X-Conversation-Message-ID"]
	}

	for _, name := range codebuddyConversationHeaderNames {
		value := readHeaderCI(merged, name)
		if value == "" {
			switch name {
			case "X-Conversation-Message-ID", "X-Request-ID":
				value = messageID
			default:
				value = generated[name]
			}
		}
		writeHeaderCI(merged, name, value)
	}
}

// codebuddyChannelGateMessage recognises the channel-verification variants of
// code 11128 and returns a message naming the cause. The code alone cannot
// decide: it also carries "first message is not system prompt", a body-shape
// problem, so a caller branching on the code would report the wrong fix.
func codebuddyChannelGateMessage(body []byte) (string, bool) {
	var data struct {
		Code        int    `json:"code"`
		Msg         string `json:"msg"`
		DisplayTips struct {
			En string `json:"en"`
		} `json:"displayTips"`
	}
	if err := json.Unmarshal(body, &data); err != nil || data.Code != 11128 {
		return "", false
	}
	if !codebuddyChannelGateRegex.MatchString(data.Msg) {
		return "", false
	}
	variant := "request illegal"
	if strings.Contains(strings.ToLower(data.Msg), "conversation id missing") {
		variant = "conversation id missing"
	}
	msg := "CodeBuddy channel verification failed (11128 \"" + variant + "\"): the upstream " +
		"gateway requires the X-Conversation-* header family the official IDE sends, which this " +
		"executor supplies. If this appears the gate changed, e.g. a new required header."
	if data.DisplayTips.En != "" {
		msg += " Upstream said: " + data.DisplayTips.En
	}
	return msg, true
}

// stableConversationUUID derives an RFC-4122-v5-shaped UUID from key via
// SHA-1, matching the JS executor's derivation so the same session key maps
// to the same conversation id across implementations.
func stableConversationUUID(key string) string {
	sum := sha1.Sum([]byte(key))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	u, err := uuid.FromBytes(b[:])
	if err != nil {
		return uuid.NewString()
	}
	return u.String()
}

// firstUserText extracts the opening user message text from an OpenAI-shaped
// body (string or typed blocks) to use as a conversation-key fallback.
func firstUserText(body []byte) string {
	_, userText := codebuddyBodyKeys(body)
	return userText
}

// codebuddyBodyKeys extracts, in one parse, the identifiers the request body
// already carries: the conversation key (`session_id`, then
// `prompt_cache_key`, then `conversation_id`) and the opening user message.
// The key comes from the body because that is what the official client sends;
// only when the body carries none does the caller fall back to the message
// text, which is weaker — two conversations opened with the same words map to
// one id. Parsing once keeps this off the allocation path of a hot request.
func codebuddyBodyKeys(body []byte) (key, userText string) {
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return "", ""
	}
	for _, name := range []string{"session_id", "prompt_cache_key", "conversation_id"} {
		if v, ok := reqMap[name].(string); ok && v != "" {
			key = v
			break
		}
	}

	msgs, _ := reqMap["messages"].([]any)
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != "user" {
			continue
		}
		switch content := m["content"].(type) {
		case string:
			return key, content
		case []any:
			var sb strings.Builder
			for _, blk := range content {
				if bm, ok := blk.(map[string]any); ok {
					if t, ok := bm["text"].(string); ok {
						sb.WriteString(t)
					}
				}
			}
			return key, sb.String()
		}
		return key, ""
	}
	return key, ""
}

// sseToOpenAIJSON re-aggregates OpenAI chat-completions SSE chunks into a
// single chat.completion JSON object. Returns (nil, false) when raw is not
// SSE-shaped (e.g. an upstream error JSON that should pass through).
func sseToOpenAIJSON(raw []byte) ([]byte, bool) {
	var chunks []map[string]any
	var streamErr map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		payload := strings.TrimSpace(trimmed[5:])
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if e, ok := chunk["error"]; ok {
			streamErr, _ = e.(map[string]any)
			continue
		}
		chunks = append(chunks, chunk)
	}
	if streamErr != nil {
		b, _ := json.Marshal(map[string]any{"error": streamErr})
		return b, true
	}
	if len(chunks) == 0 {
		return nil, false
	}

	var contentParts, reasoningParts []string
	toolCallIdx := map[int]map[string]any{}
	var toolCalls []map[string]any
	finishReason := "stop"
	var usage any
	var first map[string]any

	for _, chunk := range chunks {
		if first == nil {
			first = chunk
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if c, ok := delta["content"].(string); ok && c != "" {
			contentParts = append(contentParts, c)
		}
		if r, ok := delta["reasoning_content"].(string); ok && r != "" {
			reasoningParts = append(reasoningParts, r)
		}
		if fr, ok := choice["finish_reason"].(string); ok {
			finishReason = fr
		}
		if u, ok := chunk["usage"]; ok {
			usage = u
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, tcAny := range tcs {
				tc, _ := tcAny.(map[string]any)
				idx, _ := tc["index"].(float64)
				entry, ok := toolCallIdx[int(idx)]
				if !ok {
					entry = map[string]any{
						"id":       "",
						"type":     "function",
						"function": map[string]any{"name": "", "arguments": ""},
					}
					toolCallIdx[int(idx)] = entry
					toolCalls = append(toolCalls, entry)
				}
				if id, ok := tc["id"].(string); ok && id != "" {
					entry["id"] = id
				}
				if fn, ok := tc["function"].(map[string]any); ok {
					fEntry, _ := entry["function"].(map[string]any)
					if n, ok := fn["name"].(string); ok && n != "" {
						if existing, _ := fEntry["name"].(string); existing == "" {
							fEntry["name"] = n
						} else {
							fEntry["name"] = existing + n
						}
					}
					if a, ok := fn["arguments"].(string); ok && a != "" {
						existing, _ := fEntry["arguments"].(string)
						fEntry["arguments"] = existing + a
					}
				}
			}
		}
	}

	msg := map[string]any{"role": "assistant"}
	content := strings.Join(contentParts, "")
	if content == "" {
		if len(toolCalls) > 0 {
			msg["content"] = nil
		} else {
			msg["content"] = ""
		}
	} else {
		msg["content"] = content
	}
	var validToolCalls []map[string]any
	for _, tc := range toolCalls {
		if fn, ok := tc["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok && strings.TrimSpace(n) != "" {
				validToolCalls = append(validToolCalls, tc)
			}
		}
	}
	if len(validToolCalls) > 0 {
		msg["tool_calls"] = validToolCalls
	}
	if len(reasoningParts) > 0 {
		msg["reasoning_content"] = strings.Join(reasoningParts, "")
	}

	result := map[string]any{
		"id":      first["id"],
		"object":  "chat.completion",
		"created": first["created"],
		"model":   first["model"],
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason,
		}},
	}
	if usage != nil {
		result["usage"] = usage
	}
	if result["id"] == nil {
		result["id"] = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	if result["created"] == nil {
		result["created"] = time.Now().Unix()
	}
	if result["model"] == nil {
		result["model"] = ""
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, false
	}
	return b, true
}
