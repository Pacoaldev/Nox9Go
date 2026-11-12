package chat

import (
	"net/http"
	"net/http/httptest"
	"testing"

	json "encoding/json/v2"
)

// TestHandleOllamaTags_AnswersOllamaShape pins the response contract an Ollama
// client depends on.
//
// The gateway already answers POST /api/chat with the Ollama wire format, but a
// real Ollama client lists models through GET /api/tags before it chats. Without
// that route the client reports the gateway as having no models and never sends
// the request, which made the Ollama lane unusable even though the chat half
// worked.
func TestHandleOllamaTags_AnswersOllamaShape(t *testing.T) {
	h := NewChatHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)

	h.HandleOllamaTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var body struct {
		Models []struct {
			Name       string `json:"name"`
			ModifiedAt string `json:"modified_at"`
			Size       int64  `json:"size"`
			Digest     string `json:"digest"`
			Details    struct {
				Format            string `json:"format"`
				Family            string `json:"family"`
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the Ollama model list: %v (%s)", err, rec.Body.String())
	}
	if len(body.Models) == 0 {
		t.Fatal("models is empty, want at least one entry")
	}
	if body.Models[0].Name == "" || body.Models[0].Details.Format != "gguf" {
		t.Errorf("first entry = %+v, want a named gguf entry", body.Models[0])
	}
}

// TestHandleOllamaTags_AnswersPreflight pins the CORS preflight, which a
// browser-hosted Ollama client issues before the listing.
func TestHandleOllamaTags_AnswersPreflight(t *testing.T) {
	h := NewChatHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/tags", nil)

	h.HandleOllamaTags(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, want GET, OPTIONS", got)
	}
}
