package executor

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nox9go/proxy/internal/providers"
)

// decodeBody is a small test helper: the executors work on raw JSON bytes, so
// the assertions below read the body back as a map rather than comparing
// serialised strings (key order is not stable).
func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return parsed
}

// The ChatGPT Codex OAuth endpoint answers 400 "Unsupported parameter:
// max_output_tokens" for a body that carries one, so the value must not reach
// it. Clients (OpenCode, Cline, Orca) always send max_tokens on chat
// completions, and buildResponsesBody folds that into max_output_tokens.
func TestStripCodexUnsupportedTokenParams_FromChatCompletions(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"max_tokens": 4096,
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	body := stripCodexUnsupportedTokenParams(raw)
	parsed := decodeBody(t, body)

	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if _, ok := parsed[key]; ok {
			t.Errorf("expected %q to be stripped for the Codex backend, got %v", key, parsed[key])
		}
	}
	if parsed["model"] != "gpt-5.6-terra" {
		t.Errorf("model must survive the strip, got %v", parsed["model"])
	}
}

// A client that already speaks the Responses API sends max_output_tokens
// directly. That one is stripped too — it is the exact field the backend
// rejects, so passing it through unchanged is the reported bug.
func TestStripCodexUnsupportedTokenParams_FromResponsesInput(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]}],
		"max_output_tokens": 2048
	}`)

	body := stripCodexUnsupportedTokenParams(raw)
	parsed := decodeBody(t, body)

	if _, ok := parsed["max_output_tokens"]; ok {
		t.Errorf("a client-supplied max_output_tokens must not reach the Codex backend, got %v", parsed["max_output_tokens"])
	}
	if _, ok := parsed["input"]; !ok {
		t.Error("input must survive the strip")
	}
}

// Stripping is destructive, so a body that cannot be parsed must be returned
// byte-for-byte: a malformed payload should fail at the upstream with a useful
// error, not be silently rewritten into a valid-looking one.
func TestStripCodexUnsupportedTokenParams_LeavesUnparsableBodyAlone(t *testing.T) {
	raw := []byte(`{"model": "gpt-5.6-terra", "max_tokens":`)

	if body := stripCodexUnsupportedTokenParams(raw); string(body) != string(raw) {
		t.Errorf("an unparsable body must be passed through unchanged, got %s", body)
	}
}

// grok-cli speaks the same Responses dialect over a different backend that does
// accept the parameter, so the strip must stay scoped to the Codex path rather
// than living inside buildResponsesBody.
func TestStripCodexUnsupportedTokenParams_LeavesBuildResponsesBodyAlone(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"max_tokens": 4096,
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	transformed, _, err := buildResponsesBody(raw)
	if err != nil {
		t.Fatalf("buildResponsesBody failed: %v", err)
	}

	parsed := decodeBody(t, transformed)
	if _, ok := parsed["max_output_tokens"]; !ok {
		t.Error("buildResponsesBody must keep emitting max_output_tokens; the strip is a Codex-backend concern")
	}
}

// The compaction marker and every other field must survive: ForwardCodex runs
// the strip and applyCodexCompact over the same body, and a compaction request
// is still a request the backend validates.
func TestStripCodexUnsupportedTokenParams_KeepsOtherFields(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"max_output_tokens": 100,
		"_compact": true,
		"stream": true,
		"store": false,
		"reasoning": {"effort": "medium"}
	}`)

	parsed := decodeBody(t, stripCodexUnsupportedTokenParams(raw))

	if parsed["_compact"] != true {
		t.Error("the _compact marker must survive so applyCodexCompact still sees it")
	}
	if parsed["stream"] != true || parsed["store"] != false {
		t.Error("stream/store must survive the strip")
	}
	if _, ok := parsed["reasoning"]; !ok {
		t.Error("reasoning must survive the strip")
	}
}

// The unit tests above pin the helper; this one pins the wiring, because a
// helper that ForwardCodex never calls would leave the 400 exactly as
// reported. The fake upstream rejects the body the way ChatGPT does, so a
// parameter missing from this list is one the backend would still 400 on.
func TestForwardCodex_UpstreamNeverSeesTokenParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var m map[string]any
		if r.Header.Get("session_id") != "session-1" {
			t.Errorf("upstream session_id = %q, want session-1", r.Header.Get("session_id"))
		}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Errorf("upstream got non-JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p"} {
			if _, ok := m[key]; ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"detail":"Unsupported parameter: ` + key + `"}`))
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"}
	rec := httptest.NewRecorder()
	body := []byte(`{"model":"cx/gpt-5.6-terra","max_tokens":4096,"temperature":0.7,"top_p":0.9,"messages":[{"role":"user","content":"hi"}],"stream":true}`)
	// A 400 here means the parameter still reached the backend — the reported
	// bug, and the only thing this test exists to catch.
	if err := ForwardCodex(rec, &Request{
		Client:    srv.Client(),
		Config:    cfg,
		APIKey:    "test-token",
		Body:      body,
		IsStream:  true,
		SessionID: "session-1",
	}); err != nil {
		t.Fatalf("ForwardCodex errored: %v", err)
	}
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("upstream rejected the request as unsupported-parameter: %s", rec.Body.String())
	}
}

// The Codex backend's reasoning models answer 400 "Unsupported parameter:
// temperature" (and the same for top_p). Zed sends temperature on every turn,
// so both sampling knobs must be dropped on the Codex path.
func TestResolveCodexSessionID(t *testing.T) {
	tests := []struct {
		name    string
		session string
		body    string
		want    string
	}{
		{name: "request session wins", session: " header-session ", body: `{"session_id":"body-session"}`, want: "header-session"},
		{name: "body session id", body: `{"session_id":"body-session"}`, want: "body-session"},
		{name: "body prompt cache key", body: `{"prompt_cache_key":"cache-key"}`, want: "cache-key"},
		{name: "body conversation id", body: `{"conversation_id":"conversation"}`, want: "conversation"},
		{name: "message text alone is not a conversation identity", body: `{"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveCodexSessionID(tt.session, []byte(tt.body)); got != tt.want {
				t.Fatalf("resolveCodexSessionID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A conversation the client did not label still needs one stable
// prompt_cache_key, or every turn opens a new cache shard. A key the client
// already sent is the caller's identity and must survive untouched.
func TestInjectCodexPromptCacheKey(t *testing.T) {
	const native = `{"model":"gpt-6-luna","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	withClientKey := `{"model":"gpt-6-luna","prompt_cache_key":"client-key","input":[]}`

	tests := []struct {
		name     string
		session  string
		body     string
		wantKey  string
		wantSame bool
	}{
		{name: "injects a key from the session", session: "sess-1", body: native, wantKey: stableConversationUUID("codex:sess-1")},
		{name: "same session yields the same key", session: "sess-1", body: native, wantKey: stableConversationUUID("codex:sess-1")},
		{name: "other session yields another key", session: "sess-2", body: native, wantKey: stableConversationUUID("codex:sess-2")},
		{name: "client key is preserved", session: "sess-1", body: withClientKey, wantKey: "client-key"},
		{name: "no session leaves the body untouched", session: "", body: native, wantSame: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := injectCodexPromptCacheKey([]byte(tt.body), tt.session)
			if tt.wantSame {
				if string(out) != tt.body {
					t.Fatalf("body changed without a session: %s", out)
				}
				return
			}
			if got := decodeBody(t, out)["prompt_cache_key"]; got != tt.wantKey {
				t.Fatalf("prompt_cache_key = %v, want %q", got, tt.wantKey)
			}
		})
	}
}

func TestStripCodexUnsupportedTokenParams_DropsSamplingParams(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-6-astra",
		"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]}],
		"temperature": 0.7,
		"top_p": 0.9,
		"reasoning": {"effort": "high"}
	}`)

	parsed := decodeBody(t, stripCodexUnsupportedTokenParams(raw))

	for _, key := range []string{"temperature", "top_p"} {
		if _, ok := parsed[key]; ok {
			t.Errorf("expected %q to be stripped for the Codex backend, got %v", key, parsed[key])
		}
	}
	if _, ok := parsed["reasoning"]; !ok {
		t.Error("reasoning must survive the strip")
	}
}
