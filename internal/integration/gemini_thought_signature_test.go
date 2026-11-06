//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
)

// Gemini 3 rejects a current-turn functionCall that carries no thought_signature
// (400 INVALID_ARGUMENT "Function call is missing a thought_signature in
// functionCall parts"). The `gemini` provider talks to the OpenAI-compat endpoint,
// which never runs the native Antigravity backfill, so the gateway must inject
// the signature into each assistant tool_call before dispatch. This pins that the
// backfilled `extra_content.google.thought_signature` reaches the upstream.
func TestGeminiOpenAICompatibleBackfillsThoughtSignature(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-gemini", "gemini", "Gemini Integration", upstream, "sk-gemini")

	res := env.Post(t, "/v1/chat/completions", map[string]any{
		"model":  "gemini/gemini-3.6-flash",
		"stream": false,
		"messages": []map[string]any{
			{"role": "user", "content": "read the file"},
			{"role": "assistant", "content": nil, "tool_calls": []map[string]any{
				{"id": "call_1", "type": "function", "function": map[string]any{"name": "read", "arguments": "{}"}},
			}},
			{"role": "tool", "tool_call_id": "call_1", "content": "file contents"},
		},
	})
	if res.Status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	sent := upstream.Last(t)
	body := string(sent.Body)
	if !strings.Contains(body, `"extra_content"`) {
		t.Fatalf("upstream body carries no extra_content: %s", truncate(sent.Body))
	}
	if !strings.Contains(body, `"thought_signature"`) {
		t.Fatalf("upstream body carries no thought_signature: %s", truncate(sent.Body))
	}
}

// A request with no assistant tool_calls must be forwarded byte-for-byte in
// shape: the backfill only touches turns that would otherwise draw the 400.
func TestGeminiOpenAICompatibleLeavesToolFreeRequestAlone(t *testing.T) {
	env, upstream := newProviderEnv(t)

	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusOK {
		t.Fatalf("POST = %d, want 200", res.Status)
	}
	if got := string(upstream.Last(t).Body); strings.Contains(got, "extra_content") {
		t.Errorf("a tool-free request gained extra_content: %s", got)
	}
}
