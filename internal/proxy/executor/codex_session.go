package executor

import (
	"strings"

	json "encoding/json/v2"
)

// codexBodyKeys extracts the identifiers from a Responses API body:
// the conversation key (session_id, prompt_cache_key, conversation_id)
// and the first user message text (fallback for stable id generation).
// Mirrors codebuddyBodyKeys logic but for Responses format.
func codexBodyKeys(body []byte) (key, userText string) {
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return "", ""
	}
	// Priority: session_id → prompt_cache_key → conversation_id
	for _, name := range []string{"session_id", "prompt_cache_key", "conversation_id"} {
		if v, ok := reqMap[name].(string); ok && v != "" {
			key = v
			break
		}
	}

	// Extract first user message text as fallback
	inputs, _ := reqMap["input"].([]any)
	for _, raw := range inputs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "user" {
			continue
		}
		content, _ := msg["content"].([]any)
		for _, blk := range content {
			bm, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := bm["type"].(string); ok && t == "input_text" {
				if txt, ok := bm["text"].(string); ok {
					return key, txt
				}
			}
		}
		return key, ""
	}
	return key, ""
}

// resolveCodexSessionID derives a stable conversation identifier from
// req.SessionID (header) or explicit body identifiers. Message text is not a
// safe identity: separate conversations can start with identical prompts.
func resolveCodexSessionID(sessionID string, body []byte) string {
	// Client session from header (highest priority)
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		return sessionID
	}

	// Extract from body
	key, _ := codexBodyKeys(body)
	return key
}

// injectCodexPromptCacheKey sets prompt_cache_key from the resolved
// conversation id when the body carries none, mirroring upstream
// open-sse/executors/codex.js ("Inject prompt_cache_key for stable Codex
// prompt caching"). A client-supplied key is preserved: it is the caller's
// own conversation identity.
func injectCodexPromptCacheKey(body []byte, sessionID string) []byte {
	if sessionID == "" {
		return body
	}
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return body
	}
	if existing, ok := reqMap["prompt_cache_key"].(string); ok && existing != "" {
		return body
	}
	reqMap["prompt_cache_key"] = stableConversationUUID("codex:" + sessionID)
	out, err := json.Marshal(reqMap)
	if err != nil {
		return body
	}
	return out
}
