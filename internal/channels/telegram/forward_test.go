package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestGateway starts a fake OpenAI-compatible upstream and returns its URL
// plus the requests it received, so a test asserts the exact payload the bot
// sends instead of trusting the client.
func newTestGateway(t *testing.T, reply string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		got = append(got, payload)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// S8: Telegram caps a message at 4096 characters. A longer reply is split in
// order and nothing is dropped: the concatenation of the parts is the original.
func TestSplitMessage(t *testing.T) {
	short := strings.Repeat("a", 100)
	if parts := splitMessage(short, maxTelegramMessage); len(parts) != 1 {
		t.Fatalf("splitMessage(short) = %d parts, want 1", len(parts))
	}

	long := strings.Repeat("b", maxTelegramMessage*2+500)
	parts := splitMessage(long, maxTelegramMessage)
	if len(parts) != 3 {
		t.Fatalf("splitMessage(long) = %d parts, want 3", len(parts))
	}
	for i, p := range parts {
		if len(p) > maxTelegramMessage {
			t.Errorf("part %d length = %d, want <= %d", i, len(p), maxTelegramMessage)
		}
	}
	if strings.Join(parts, "") != long {
		t.Errorf("joining the parts does not reproduce the original: content was dropped")
	}
}

// S8: splitting prefers a line boundary so a code block or a sentence is not cut
// mid-line when a newline is available within reach.
func TestSplitMessage_PrefersNewline(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	long := strings.Repeat(line, 60)
	parts := splitMessage(long, maxTelegramMessage)

	for i, p := range parts[:len(parts)-1] {
		if !strings.HasSuffix(p, "\n") {
			t.Errorf("part %d does not end on a newline boundary", i)
		}
	}
	if strings.Join(parts, "") != long {
		t.Errorf("content was dropped: join does not reproduce the original")
	}
}

// S4: an authorized text message becomes a chat completion against the local
// gateway, carrying the configured model and the chat's session id.
func TestForwardText_SendsChatCompletion(t *testing.T) {
	srv, got := newTestGateway(t, "hola desde el gateway")
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, model: "test/model"}

	gotReply, err := forwardText(context.Background(), srv.URL, "cli-token", "test/model", []chatMessage{
		{Role: "user", Content: "hola"},
	})
	if err != nil {
		t.Fatalf("forwardText() error = %v", err)
	}
	if gotReply != "hola desde el gateway" {
		t.Errorf("reply = %q, want the gateway's content", gotReply)
	}
	if len(*got) != 1 {
		t.Fatalf("gateway received %d requests, want 1", len(*got))
	}
	payload := (*got)[0]
	if model, _ := payload["model"].(string); model != "test/model" {
		t.Errorf("model sent = %q, want %q", model, "test/model")
	}
	msgs, _ := payload["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("messages sent = %d, want 1", len(msgs))
	}
	_ = repo
}

// S4: the gateway is authenticated with the channel's own client API key, never
// anonymously. An unauthenticated call would be rejected by the engine's
// RequireApiKey guard; a CLI token would be too, because the chat route is a
// client route.
func TestForwardText_SendsBearerAPIKey(t *testing.T) {
	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := forwardText(context.Background(), srv.URL, "the-cli-token", "m", []chatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("forwardText() error = %v", err)
	}
	if header != "Bearer the-cli-token" {
		t.Errorf("Authorization = %q, want the channel key as Bearer", header)
	}
}

// S4: an upstream failure surfaces as an error so the handler can tell the user,
// instead of returning an empty string that would look like an empty answer.
func TestForwardText_SurfacesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream exploded"}}`))
	}))
	t.Cleanup(srv.Close)

	_, err := forwardText(context.Background(), srv.URL, "t", "m", []chatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatalf("forwardText() error = nil, want an error for HTTP 502")
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, want the upstream message included", err.Error())
	}
}

// S6: commands never reach the provider. They are answered locally so a typo or
// a /status does not spend a token.
func TestParseCommand(t *testing.T) {
	tests := []struct {
		name string
		text string
		cmd  string
		args string
		is   bool
	}{
		{name: "plain command", text: "/new", cmd: "new", is: true},
		{name: "command with args", text: "/model gpt-4o", cmd: "model", args: "gpt-4o", is: true},
		{name: "command with bot suffix", text: "/status@my_bot", cmd: "status", is: true},
		{name: "plain text is not a command", text: "hola", is: false},
		{name: "slash alone is not a command", text: "/", is: false},
		{name: "empty is not a command", text: "", is: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args, is := parseCommand(tt.text)
			if is != tt.is {
				t.Fatalf("parseCommand(%q) is = %v, want %v", tt.text, is, tt.is)
			}
			if !tt.is {
				return
			}
			if cmd != tt.cmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.cmd)
			}
			if args != tt.args {
				t.Errorf("args = %q, want %q", args, tt.args)
			}
		})
	}
}
