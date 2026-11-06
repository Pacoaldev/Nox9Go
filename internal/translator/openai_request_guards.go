package translator

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Upstream request guards for request fields a provider rejects.
//
// Groq (and some other OpenAI-compatible gateways) reject a `max_tokens` above
// the model's output cap with a 400 that names the cap:
//
//	`max_tokens` must be less than or equal to `16384`, the maximum value for
//	`max_tokens` is less than the `context_window` for this model
//
// The model catalog cannot be the source of truth here: our table describes the
// model, not the gateway's per-model cap (Groq caps qwen3.8-27b at 16384 while
// the model itself declares far more), so clamping from the catalog would still
// overshoot. The upstream's own message is authoritative, so the fix parses the
// limit it states and retries once with the request clamped to it.

// maxTokensLimitPattern matches the cap an upstream names in a max_tokens 400.
// Tolerates backticks, single/double quotes and no quoting at all.
var maxTokensLimitPattern = regexp.MustCompile(`(?i)max(?:_completion)?_tokens[^0-9]{0,60}?less than or equal to[^0-9]{0,20}?(\d{2,9})`)

// MaxTokensLimitFromError extracts the output-token cap an upstream states in a
// 400 body, or (0, false) when the body is not that error. Only a request whose
// error names a max_tokens cap qualifies; any other 400 is left alone.
func MaxTokensLimitFromError(body []byte) (int, bool) {
	if len(body) == 0 {
		return 0, false
	}
	if !bytes.Contains(body, []byte("max_tokens")) && !bytes.Contains(body, []byte("max_completion_tokens")) {
		return 0, false
	}
	m := maxTokensLimitPattern.FindSubmatch(body)
	if len(m) != 2 {
		return 0, false
	}
	limit, err := strconv.Atoi(string(m[1]))
	if err != nil || limit <= 0 {
		return 0, false
	}
	return limit, true
}

// ClampMaxTokens returns body with max_tokens (and max_completion_tokens) lowered
// to limit when it currently exceeds it. A body that does not parse, carries no
// such field, or is already at or below the limit is returned unchanged.
func ClampMaxTokens(body []byte, limit int) []byte {
	if limit <= 0 || len(body) == 0 {
		return body
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	changed := false
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		cur, present := req[key]
		if !present {
			continue
		}
		n, ok := numericValue(cur)
		if !ok || n <= float64(limit) {
			continue
		}
		req[key] = limit
		changed = true
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// GoogleThoughtSignatureKey / the field path Gemini's OpenAI-compat endpoint
// reads a function-call signature from:
//
//	"tool_calls":[{"id":"…","type":"function","function":{…},
//	               "extra_content":{"google":{"thought_signature":"<sig>"}}}]
//
// Gemini 3 rejects a current-turn functionCall with no signature (400
// INVALID_ARGUMENT "Function call is missing a thought_signature in functionCall
// parts"). Our `gemini` provider talks to the OpenAI-compat endpoint, which never
// runs the native Antigravity backfill, so a client that drops a prefill or a
// combo that rebuilt the history reached upstream unsigned.

// DefaultThinkingSignature is declared in gemini.go; this file reuses it.

// BackfillOpenAIToolCallSignatures injects a thought_signature into every
// assistant tool_call of body that lacks one, using the signature cached for that
// tool_call id (session-scoped) or DefaultThinkingSignature as the fallback.
//
// scope names the assistant turn that owns the call. Gemini requires the
// signature on the function calls of the turn being answered, and it accepts a
// known-valid signature in place of a lost one (the same substitution the native
// backfill performs). A body whose tool_calls already carry a signature is
// returned unchanged, so a well-behaved client pays no risk.
func BackfillOpenAIToolCallSignatures(body []byte, sessionID, model string) []byte {
	if len(body) == 0 {
		return body
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	msgs, ok := req["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return body
	}
	changed := false
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			continue
		}
		calls, ok := msg["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, rawCall := range calls {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			if googleThoughtSignature(call) != "" {
				continue
			}
			id, _ := call["id"].(string)
			sig := ""
			if strings.TrimSpace(id) != "" {
				sig = GetGeminiThoughtSignature(id, sessionID, model)
			}
			if sig == "" {
				sig = DefaultThinkingSignature
			}
			extra, _ := call["extra_content"].(map[string]any)
			if extra == nil {
				extra = map[string]any{}
			}
			google, _ := extra["google"].(map[string]any)
			if google == nil {
				google = map[string]any{}
			}
			google["thought_signature"] = sig
			extra["google"] = google
			call["extra_content"] = extra
			changed = true
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// googleThoughtSignature reads a signature already present on an OpenAI tool_call.
func googleThoughtSignature(call map[string]any) string {
	extra, ok := call["extra_content"].(map[string]any)
	if !ok {
		return ""
	}
	google, ok := extra["google"].(map[string]any)
	if !ok {
		return ""
	}
	sig, _ := google["thought_signature"].(string)
	return sig
}

// HarvestOpenAIToolCallSignatures stores the signatures a Gemini OpenAI-compat
// response attached to its tool_calls, so a later turn can replay the exact
// signature instead of the default one. Returns the number stored.
func HarvestOpenAIToolCallSignatures(responseBody []byte, sessionID, model string) int {
	if len(responseBody) == 0 {
		return 0
	}
	var resp map[string]any
	if err := json.Unmarshal(responseBody, &resp); err != nil {
		return 0
	}
	choices, ok := resp["choices"].([]any)
	if !ok {
		return 0
	}
	stored := 0
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		message, ok := choice["message"].(map[string]any)
		if !ok {
			continue
		}
		calls, ok := message["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, rawCall := range calls {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			sig := googleThoughtSignature(call)
			id, _ := call["id"].(string)
			if sig == "" || id == "" {
				continue
			}
			StoreGeminiThoughtSignature(id, sig, sessionID, model)
			stored++
		}
	}
	return stored
}
