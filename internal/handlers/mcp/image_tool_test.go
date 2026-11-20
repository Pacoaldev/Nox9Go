package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
)

// laneCall is one request the fake lane received, decoded.
type laneCall struct {
	Method       string
	Path         string
	ContentType  string
	ConnectionID string
	Body         map[string]any
	Raw          string
}

// laneAnswer is one canned upstream answer: the fake lane replays answers in
// order and keeps answering the last one once the list runs out.
type laneAnswer struct {
	Status int
	Body   string
}

// fakeImageLane stands in for the media handler's image conjunction: it records
// what the tool sent and answers a canned recorder, no provider involved.
type fakeImageLane struct {
	answers []laneAnswer
	calls   []laneCall
}

func (f *fakeImageLane) HandleImages(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	f.calls = append(f.calls, laneCall{
		Method:       r.Method,
		Path:         r.URL.Path,
		ContentType:  r.Header.Get("Content-Type"),
		ConnectionID: r.Header.Get("x-connection-id"),
		Body:         decoded,
		Raw:          string(raw),
	})

	answer := laneAnswer{Status: http.StatusOK, Body: `{"data":[{"b64_json":"iVBORw0KGgo="}]}`}
	if len(f.answers) > 0 {
		idx := len(f.calls) - 1
		if idx >= len(f.answers) {
			idx = len(f.answers) - 1
		}
		answer = f.answers[idx]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(answer.Status)
	_, _ = io.WriteString(w, answer.Body)
}

// seedConnection stores one active connection, the way the dashboard does.
func seedConnection(t *testing.T, repo *db.Repo, id, provider string) {
	t.Helper()
	if err := repo.CreateProviderConnection(id, provider, "apikey", id, "sk-test-value"); err != nil {
		t.Fatalf("seed connection %s/%s: %v", provider, id, err)
	}
}

// callToolResult drives one tools/call through the JSON-RPC surface and returns
// the tool result, so every assertion goes through the interface a client uses.
func callToolResult(t *testing.T, h *Handler, name, arguments string) *CallToolResult {
	t.Helper()
	params := `{"name":"` + name + `"`
	if arguments != "" {
		params += `,"arguments":` + arguments
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}}`

	rec := post(t, h, body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Error  *Error          `json:"error"`
		Result *CallToolResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, rec.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatalf("no result in %s", rec.Body.String())
	}
	return resp.Result
}

// TestGenerateImageAnswerShapes covers what the tool does with each answer the
// lane can give, and that the optional arguments travel only when supplied.
func TestGenerateImageAnswerShapes(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		answers   []laneAnswer
		// wantImage is the base64 payload an image block must carry; empty
		// means the answer is expected to be text only.
		wantImage string
		wantText  []string
		wantBody  map[string]any
	}{
		{
			name:      "base64 answer becomes an image block with a naming text block",
			arguments: `{"prompt":"a lighthouse at dusk"}`,
			answers:   []laneAnswer{{Status: http.StatusOK, Body: `{"data":[{"b64_json":"iVBORw0KGgo="}]}`}},
			wantImage: "iVBORw0KGgo=",
			wantText:  []string{"antigravity", "gemini-3.1-flash-image", "conn-ag"},
			wantBody:  map[string]any{"model": "antigravity/gemini-3.1-flash-image", "prompt": "a lighthouse at dusk"},
		},
		{
			name:      "url-only answer is returned as text",
			arguments: `{"prompt":"a lighthouse at dawn"}`,
			answers:   []laneAnswer{{Status: http.StatusOK, Body: `{"data":[{"url":"https://images.example.test/lh.png"}]}`}},
			wantText:  []string{"https://images.example.test/lh.png"},
			wantBody:  map[string]any{"model": "antigravity/gemini-3.1-flash-image"},
		},
		{
			name:      "size and n are forwarded only when the caller supplied them",
			arguments: `{"prompt":"a lighthouse","size":"1024x1024","n":2}`,
			answers:   []laneAnswer{{Status: http.StatusOK, Body: `{"data":[{"b64_json":"iVBORw0KGgo="}]}`}},
			wantImage: "iVBORw0KGgo=",
			wantText:  []string{"antigravity"},
			wantBody:  map[string]any{"model": "antigravity/gemini-3.1-flash-image", "size": "1024x1024", "n": float64(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			seedConnection(t, repo, "conn-ag", "antigravity")
			lane := &fakeImageLane{answers: tt.answers}
			h := NewHandler(repo, nil, lane)

			res := callToolResult(t, h, "generate_image", tt.arguments)
			if res.IsError {
				t.Fatalf("result is a tool error: %+v", res.Content)
			}
			if len(lane.calls) != 1 {
				t.Fatalf("lane saw %d calls, want 1", len(lane.calls))
			}
			call := lane.calls[0]
			if call.Method != http.MethodPost || call.Path != "/v1/images/generations" {
				t.Errorf("lane got %s %s, want POST /v1/images/generations", call.Method, call.Path)
			}
			if call.ContentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", call.ContentType)
			}
			if call.ConnectionID != "conn-ag" {
				t.Errorf("x-connection-id = %q, want conn-ag", call.ConnectionID)
			}
			for key, want := range tt.wantBody {
				if got := call.Body[key]; got != want {
					t.Errorf("request body %s = %v, want %v (%s)", key, got, want, call.Raw)
				}
			}
			if _, ok := tt.wantBody["size"]; !ok {
				if _, present := call.Body["size"]; present {
					t.Errorf("request body carries size the caller never sent: %s", call.Raw)
				}
			}
			if _, ok := tt.wantBody["n"]; !ok {
				if _, present := call.Body["n"]; present {
					t.Errorf("request body carries n the caller never sent: %s", call.Raw)
				}
			}

			if tt.wantImage != "" {
				if len(res.Content) < 2 {
					t.Fatalf("result has %d blocks, want an image block and a text block: %+v", len(res.Content), res.Content)
				}
				img := res.Content[0]
				if img.Type != "image" {
					t.Fatalf("first block type = %q, want image", img.Type)
				}
				if img.Data != tt.wantImage {
					t.Errorf("image data = %q, want %q", img.Data, tt.wantImage)
				}
				if img.MimeType != "image/png" {
					t.Errorf("image mimeType = %q, want image/png", img.MimeType)
				}
				if img.Text != "" {
					t.Errorf("image block carries text %q", img.Text)
				}
			}
			if len(res.Content) == 0 {
				t.Fatalf("result has no content")
			}
			all := ""
			for _, block := range res.Content {
				all += block.Text + "\n"
			}
			for _, want := range tt.wantText {
				if !strings.Contains(all, want) {
					t.Errorf("result text %q is missing %q", all, want)
				}
			}
		})
	}
}

// TestGenerateImageFallsThroughToNextCandidate is S4: a candidate that fails
// does not end the call.
func TestGenerateImageFallsThroughToNextCandidate(t *testing.T) {
	repo := newTestRepo(t)
	seedConnection(t, repo, "conn-ag", "antigravity")
	seedConnection(t, repo, "conn-cx", "codex")
	lane := &fakeImageLane{answers: []laneAnswer{
		{Status: http.StatusBadGateway, Body: `{"error":{"message":"upstream said no"}}`},
		{Status: http.StatusOK, Body: `{"data":[{"b64_json":"ZmFsbGJhY2s="}]}`},
	}}
	h := NewHandler(repo, nil, lane)

	res := callToolResult(t, h, "generate_image", `{"prompt":"a lighthouse"}`)
	if res.IsError {
		t.Fatalf("result is a tool error after the fallback: %+v", res.Content)
	}
	if len(lane.calls) != 2 {
		t.Fatalf("lane saw %d calls, want 2 (one per candidate): %+v", len(lane.calls), lane.calls)
	}
	if lane.calls[0].ConnectionID != "conn-ag" || lane.calls[1].ConnectionID != "conn-cx" {
		t.Fatalf("candidate order = %q then %q, want conn-ag then conn-cx",
			lane.calls[0].ConnectionID, lane.calls[1].ConnectionID)
	}

	all := ""
	for _, block := range res.Content {
		all += block.Text + "\n"
	}
	if !strings.Contains(all, "conn-cx") {
		t.Errorf("success text %q does not name the connection that produced the image", all)
	}
	for _, block := range res.Content {
		if block.Type == "image" && block.Data != "ZmFsbGJhY2s=" {
			t.Errorf("image data = %q, want the second candidate's payload", block.Data)
		}
	}
}

// TestGenerateImageExplicitModelSendsExactlyThatModel pins the model argument:
// one candidate, that model, and no connection pinned by the tool.
func TestGenerateImageExplicitModelSendsExactlyThatModel(t *testing.T) {
	repo := newTestRepo(t)
	seedConnection(t, repo, "conn-ag", "antigravity")
	lane := &fakeImageLane{answers: []laneAnswer{{Status: http.StatusOK, Body: `{"data":[{"b64_json":"iVBORw0KGgo="}]}`}}}
	h := NewHandler(repo, nil, lane)

	res := callToolResult(t, h, "generate_image", `{"prompt":"a lighthouse","model":"dall-e-3"}`)
	if res.IsError {
		t.Fatalf("result is a tool error: %+v", res.Content)
	}
	if len(lane.calls) != 1 {
		t.Fatalf("lane saw %d calls, want exactly 1 for an explicit model", len(lane.calls))
	}
	if got := lane.calls[0].Body["model"]; got != "dall-e-3" {
		t.Errorf("request model = %v, want dall-e-3", got)
	}
	if got := lane.calls[0].ConnectionID; got != "" {
		t.Errorf("x-connection-id = %q, want none when the caller named the model", got)
	}
	for _, block := range res.Content {
		if !strings.Contains(block.Text, "dall-e-3") {
			continue
		}
		return
	}
	t.Errorf("result %+v never names the requested model", res.Content)
}

// TestGenerateImageToolErrors covers every way the tool must refuse without
// claiming success (S5).
func TestGenerateImageToolErrors(t *testing.T) {
	tests := []struct {
		name      string
		providers []struct{ id, provider string }
		answers   []laneAnswer
		arguments string
		// nilLane removes the lane, as a build without the media handler wires it.
		nilLane     bool
		wantText    string
		wantCalls   int
		wantNoCalls bool
	}{
		{
			name:        "missing prompt",
			arguments:   `{}`,
			wantText:    "prompt",
			wantNoCalls: true,
		},
		{
			name:        "blank prompt",
			arguments:   `{"prompt":"   "}`,
			wantText:    "prompt",
			wantNoCalls: true,
		},
		{
			name:        "no connection with an image executor",
			providers:   []struct{ id, provider string }{{"conn-oa", "openai"}},
			arguments:   `{"prompt":"a lighthouse"}`,
			wantText:    "no active connection",
			wantNoCalls: true,
		},
		{
			name: "every candidate fails",
			providers: []struct{ id, provider string }{
				{"conn-ag", "antigravity"},
				{"conn-cx", "codex"},
			},
			answers: []laneAnswer{
				{Status: http.StatusBadGateway, Body: `{"error":{"message":"first candidate down"}}`},
				{Status: http.StatusInternalServerError, Body: `{"error":{"message":"second candidate down"}}`},
			},
			arguments: `{"prompt":"a lighthouse"}`,
			wantText:  "second candidate down",
			wantCalls: 2,
		},
		{
			name:        "lane absent from this build",
			nilLane:     true,
			arguments:   `{"prompt":"a lighthouse"}`,
			wantText:    "not enabled",
			wantNoCalls: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepo(t)
			for _, conn := range tt.providers {
				seedConnection(t, repo, conn.id, conn.provider)
			}
			lane := &fakeImageLane{answers: tt.answers}
			var h *Handler
			if tt.nilLane {
				h = NewHandler(repo, nil, nil)
			} else {
				h = NewHandler(repo, nil, lane)
			}

			res := callToolResult(t, h, "generate_image", tt.arguments)
			if !res.IsError {
				t.Fatalf("result = %+v, want a tool-level error", res)
			}
			if len(res.Content) == 0 {
				t.Fatalf("error result carries no message")
			}
			for _, block := range res.Content {
				if block.Type == "image" {
					t.Errorf("error result carries an image block: %+v", block)
				}
			}
			if !strings.Contains(res.Content[0].Text, tt.wantText) {
				t.Errorf("error message %q is missing %q", res.Content[0].Text, tt.wantText)
			}
			if tt.wantNoCalls && len(lane.calls) != 0 {
				t.Errorf("lane saw %d calls, want none: %+v", len(lane.calls), lane.calls)
			}
			if got := len(lane.calls); got != tt.wantCalls && !tt.wantNoCalls {
				t.Errorf("lane saw %d calls, want %d", got, tt.wantCalls)
			}
		})
	}
}

// TestGenerateImageSendsWireSyntaxForSlashBearingModelIDs pins the regression
// found against the live gateway: Cloudflare's catalog ids already carry a
// slash ("@cf/black-forest-labs/flux-2-klein-9b"), so forwarding the bare id
// made the lane's "first segment is the provider" parser read "@cf" as the
// provider and reject the pinned connection with "belongs to provider
// cloudflare-ai, not @cf". The lane must receive the provider-qualified wire
// form instead.
func TestGenerateImageSendsWireSyntaxForSlashBearingModelIDs(t *testing.T) {
	for _, provider := range []string{"cloudflare-ai", "cf"} {
		t.Run(provider, func(t *testing.T) {
			repo := newTestRepo(t)
			seedConnection(t, repo, "conn-cf", provider)
			lane := &fakeImageLane{answers: []laneAnswer{{Status: http.StatusOK, Body: `{"data":[{"b64_json":"iVBORw0KGgo="}]}`}}}
			h := NewHandler(repo, nil, lane)

			res := callToolResult(t, h, "generate_image", `{"prompt":"a red cube"}`)
			if res.IsError {
				t.Fatalf("result is a tool error: %+v", res.Content)
			}
			if len(lane.calls) != 1 {
				t.Fatalf("lane saw %d calls, want 1", len(lane.calls))
			}

			got, _ := lane.calls[0].Body["model"].(string)
			wantPrefix := provider + "/@cf/"
			if !strings.HasPrefix(got, wantPrefix) {
				t.Errorf("request model = %q, want the wire form %q… (%s)", got, wantPrefix, lane.calls[0].Raw)
			}
			// The lane splits on the first slash to find the provider; pin that
			// the first segment is the canonical provider, never a Cloudflare
			// model fragment.
			if first := strings.SplitN(got, "/", 2)[0]; first != provider {
				t.Errorf("first model segment = %q, want %q", first, provider)
			}
		})
	}
}

// TestImageMimeTypeDetectedFromPayload pins that the block reports what the
// payload actually is. The live Cloudflare lane answered a JPEG while the tool
// claimed image/png, so an MCP client that trusts mimeType would mislabel it.
// The b64 strings here are the real magic-byte prefixes, not synthetic values.
func TestImageMimeTypeDetectedFromPayload(t *testing.T) {
	tests := []struct {
		name string
		b64  string
		want string
	}{
		{name: "png", b64: "iVBORw0KGgo=", want: "image/png"},
		{name: "jpeg", b64: "/9j/4AAQSkZJRgABAQEBLAEsAAD/", want: "image/jpeg"},
		{name: "webp", b64: "UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoBAAEAAwA0JaQAA3AA/vuUAAA=", want: "image/webp"},
		{name: "gif", b64: "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7", want: "image/gif"},
		{name: "unknown bytes fall back to octet-stream", b64: "AAAAAAAAAAAA", want: "application/octet-stream"},
		{name: "not base64 at all", b64: "!!!not-base64!!!", want: "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageMimeType(tt.b64); got != tt.want {
				t.Errorf("imageMimeType(%q) = %q, want %q", tt.b64, got, tt.want)
			}
		})
	}
}

// TestGenerateImageReportsJPEGEndToEnd proves the block's mimeType follows the
// payload the lane returned, through the whole tool, not just the helper.
func TestGenerateImageReportsJPEGEndToEnd(t *testing.T) {
	repo := newTestRepo(t)
	seedConnection(t, repo, "conn-cf", "cloudflare-ai")
	lane := &fakeImageLane{answers: []laneAnswer{{Status: http.StatusOK, Body: `{"data":[{"b64_json":"/9j/4AAQSkZJRgABAQEBLAEsAAD/"}]}`}}}
	h := NewHandler(repo, nil, lane)

	res := callToolResult(t, h, "generate_image", `{"prompt":"a red cube"}`)
	if res.IsError {
		t.Fatalf("result is a tool error: %+v", res.Content)
	}
	for _, block := range res.Content {
		if block.Type != "image" {
			continue
		}
		if block.MimeType != "image/jpeg" {
			t.Errorf("image mimeType = %q, want image/jpeg for a JPEG payload", block.MimeType)
		}
		return
	}
	t.Fatalf("result carries no image block: %+v", res.Content)
}
