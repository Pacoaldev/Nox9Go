package executor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nox9go/proxy/internal/providers"
)

func TestMinimaxCodeHeadersAgentIdDefaultsToMain(t *testing.T) {
	req := &Request{
		Ctx:       t.Context(),
		Client:    &http.Client{Timeout: 10 * time.Second},
		Config:    &providers.ProviderConfig{BaseURL: "https://api.minimax.io", Format: "claude"},
		Provider:  "minimax-code",
		ModelName: "MiniMax-M2",
		APIKey:    "sk-test",
		Body:      []byte(`{"model":"MiniMax-M2","messages":[{"role":"user","content":"hi"}]}`),
		ConnData:  map[string]any{"authType": "oauth"},
	}
	h := minimaxCodeHeaders(req)
	if got := h["X-Mavis-Agent-Id"]; got != "main" {
		t.Errorf("X-Mavis-Agent-Id = %q, want %q", got, "main")
	}
}

func TestForwardMinimaxCode_TranslatesClaudeStreamForOpenAIClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"MiniMax-M3.1-Flash\",\"content\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	rec := httptest.NewRecorder()
	req := &Request{
		Ctx:            t.Context(),
		Client:         srv.Client(),
		Config:         &providers.ProviderConfig{BaseURL: srv.URL, Format: "claude"},
		Provider:       "minimax-code",
		ModelName:      "MiniMax-M3.1-Flash",
		APIKey:         "sk-test",
		Body:           []byte(`{"model":"MiniMax-M3.1-Flash","messages":[{"role":"user","content":"hi"}],"stream":true}`),
		IsStream:       true,
		ClaudeClient:   false,
		TranslateResp:  false,
		UpstreamClaude: true,
	}

	if err := ForwardMinimaxCode(rec, req); err != nil {
		t.Fatalf("ForwardMinimaxCode: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, `"object":"chat.completion.chunk"`) {
		t.Errorf("expected OpenAI chat.completion.chunk in stream, got:\n%s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected finish_reason:stop in stream, got:\n%s", out)
	}
}

func TestForwardMinimaxCode_TranslatesClaudeNonStreamForOpenAIClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"MiniMax-M3.1-Flash","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	defer srv.Close()

	rec := httptest.NewRecorder()
	req := &Request{
		Ctx:            t.Context(),
		Client:         srv.Client(),
		Config:         &providers.ProviderConfig{BaseURL: srv.URL, Format: "claude"},
		Provider:       "minimax-code",
		ModelName:      "MiniMax-M3.1-Flash",
		APIKey:         "sk-test",
		Body:           []byte(`{"model":"MiniMax-M3.1-Flash","messages":[{"role":"user","content":"hi"}],"stream":false}`),
		IsStream:       false,
		ClaudeClient:   false,
		TranslateResp:  false,
		UpstreamClaude: true,
	}

	if err := ForwardMinimaxCode(rec, req); err != nil {
		t.Fatalf("ForwardMinimaxCode: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, `"choices"`) || !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected OpenAI choices with finish_reason:stop, got:\n%s", out)
	}
}

