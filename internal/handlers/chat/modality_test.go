package chat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "encoding/json/v2"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/providers"
)

// stripProbeImage is the 1x1 PNG the vision probes already use.
const stripProbeImage = visionProbeImage

func textOnly() providers.Capabilities { return providers.Capabilities{} }
func fullVision() providers.Capabilities {
	return providers.Capabilities{Vision: true, PDF: true, AudioInput: true}
}

// An OpenAI request carrying an image must lose the image_url block when the
// target model has no vision, and gain a text placeholder in its place. This is
// the step whose absence sent the image to kiro/glm-5 and drew Bedrock's
// 400 IMAGE_FORMAT_UNSUPPORTED.
func TestStripUnsupportedModalities_OpenAIImageRemoved(t *testing.T) {
	body := []byte(`{"model":"glm-5","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is this?"},` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + stripProbeImage + `"}}` +
		`]}]}`)

	out := stripUnsupportedModalities(body, textOnly())

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal stripped body: %v", err)
	}
	msgs := payload["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)

	for _, b := range content {
		block := b.(map[string]any)
		if block["type"] == "image_url" {
			t.Fatalf("image_url survived the strip: %s", out)
		}
	}
	if !hasPlaceholder(content, "image omitted") {
		t.Errorf("expected an image placeholder, got %s", out)
	}
}

// A model that reads every modality gets the body back byte-for-byte.
func TestStripUnsupportedModalities_VisionModelUntouched(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + stripProbeImage + `"}}` +
		`]}]}`)

	if out := stripUnsupportedModalities(body, fullVision()); string(out) != string(body) {
		t.Errorf("body changed for a fully capable model:\n%s", out)
	}
}

// Claude-format image and document blocks map to vision/pdf.
func TestStripUnsupportedModalities_ClaudeImageAndDocument(t *testing.T) {
	body := []byte(`{"model":"glm-5","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"hi"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + stripProbeImage + `"}},` +
		`{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"eA=="}}` +
		`]}]}`)

	out := stripUnsupportedModalities(body, textOnly())

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	content := payload["messages"].([]any)[0].(map[string]any)["content"].([]any)
	for _, b := range content {
		block := b.(map[string]any)
		if block["type"] == "image" || block["type"] == "document" {
			t.Fatalf("multimodal block survived: %s", out)
		}
	}
	if !hasPlaceholder(content, "image omitted") {
		t.Errorf("expected an image placeholder, got %s", out)
	}
	if !hasPlaceholder(content, "file omitted") {
		t.Errorf("expected a file placeholder, got %s", out)
	}
}

// The Ollama/Hermes images array is a message-level carrier.
func TestStripUnsupportedModalities_OllamaImagesArray(t *testing.T) {
	body := []byte(`{"model":"glm-5","messages":[{"role":"user","content":"see this",` +
		`"images":["` + stripProbeImage + `"]}]}`)

	out := stripUnsupportedModalities(body, textOnly())

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg := payload["messages"].([]any)[0].(map[string]any)
	if _, ok := msg["images"]; ok {
		t.Fatalf("images array survived: %s", out)
	}
}

// Gemini inlineData carries its modality in the mime type.
func TestStripUnsupportedModalities_GeminiInlineData(t *testing.T) {
	body := []byte(`{"model":"glm-5","contents":[{"role":"user","parts":[` +
		`{"text":"hi"},` +
		`{"inlineData":{"mimeType":"image/png","data":"` + stripProbeImage + `"}}` +
		`]}]}`)

	out := stripUnsupportedModalities(body, textOnly())

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	parts := payload["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	for _, p := range parts {
		if _, ok := p.(map[string]any)["inlineData"]; ok {
			t.Fatalf("inlineData survived: %s", out)
		}
	}
	if !hasPlaceholder(parts, "image omitted") {
		t.Errorf("expected a placeholder part, got %s", out)
	}
}

// A body with no media at all is returned unchanged.
func TestStripUnsupportedModalities_TextOnlyBodyUntouched(t *testing.T) {
	body := []byte(`{"model":"glm-5","messages":[{"role":"user","content":"hi"}]}`)
	if out := stripUnsupportedModalities(body, textOnly()); string(out) != string(body) {
		t.Errorf("text-only body changed:\n%s", out)
	}
}

func hasPlaceholder(blocks []any, needle string) bool {
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			if s, _ := m["text"].(string); strings.Contains(s, needle) {
				return true
			}
		}
	}
	return false
}

// End-to-end: a request carrying an image, aimed at a text-only provider, must
// reach the upstream with the image already replaced by the placeholder. This
// is the live incident this port fixes — the upstream must never be asked to
// read a block its model cannot support.
func TestStripUnsupportedModalities_EndToEnd_TextOnlyUpstreamSeesNoImage(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	// The seeded default connection has no baseUrl and would reach the real
	// provider; replace it with the fake.
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear connections: %v", err)
	}
	seedConnDB(t, database, "deepseek", "conn-strip", "sk-test", upstream.URL)
	handler := NewChatHandler(db.NewRepo(database))

	reqBody := `{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is this?"},` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + stripProbeImage + `"}}]}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(gotBody, "image_url") {
		t.Fatalf("a text-only upstream received an image block:\n%s", gotBody)
	}
	if !strings.Contains(gotBody, "image omitted") {
		t.Fatalf("the upstream did not receive the image placeholder:\n%s", gotBody)
	}
}
