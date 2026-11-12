//go:build integration

package integration

// The Gemini-native inbound surface, proved against the real router and a real
// database: a Gemini client reaches the same routing, connection and usage path
// every other client uses, and the answer comes back in Gemini wire format.

import (
	"net/http"
	"strings"
	"testing"

	json "encoding/json/v2"
)

// TestGeminiGenerateContentReachesUpstreamAsOpenAI pins the conversion in both
// directions: the Gemini request arrives as an OpenAI Chat Completions call with
// the bare upstream model, and the answer is a Gemini GenerateContentResponse.
func TestGeminiGenerateContentReachesUpstreamAsOpenAI(t *testing.T) {
	env, upstream := newProviderEnv(t)

	body := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]any{{"text": "be terse"}},
		},
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hello gemini"}}},
		},
		"generationConfig": map[string]any{"maxOutputTokens": 32},
	}

	res := env.Post(t, "/v1beta/models/deepseek/deepseek-chat:generateContent", body)
	if res.Status != http.StatusOK {
		t.Fatalf("POST :generateContent = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	var got struct {
		Candidates []struct {
			Content struct {
				Role  string           `json:"role"`
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	res.Decode(t, &got)
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1 (body: %s)", len(got.Candidates), truncate(res.Body))
	}
	if got.Candidates[0].Content.Role != "model" {
		t.Errorf("role = %q, want model", got.Candidates[0].Content.Role)
	}
	if got.Candidates[0].FinishReason != "STOP" {
		t.Errorf("finishReason = %q, want STOP", got.Candidates[0].FinishReason)
	}
	found := false
	for _, part := range got.Candidates[0].Content.Parts {
		if text, _ := part["text"].(string); text == "upstream reply" {
			found = true
		}
	}
	if !found {
		t.Errorf("parts = %v, want the upstream text", got.Candidates[0].Content.Parts)
	}

	sent := upstream.Last(t)
	if sent.Method != http.MethodPost {
		t.Errorf("upstream method = %s, want POST", sent.Method)
	}
	if sent.Path != "/chat/completions" {
		t.Errorf("upstream path = %q, want the connection's chat route", sent.Path)
	}
	if got := sent.Model(t); got != "deepseek-chat" {
		t.Errorf("upstream model = %q, want the bare model id", got)
	}
	var envelope struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.Unmarshal(sent.Body, &envelope); err != nil {
		t.Fatalf("upstream body is not JSON: %v", err)
	}
	if len(envelope.Messages) < 2 || envelope.Messages[0].Role != "system" {
		t.Errorf("messages = %+v, want a system message carrying systemInstruction", envelope.Messages)
	}
	if envelope.MaxTokens != 32 {
		t.Errorf("max_tokens = %d, want 32 from generationConfig.maxOutputTokens", envelope.MaxTokens)
	}
}

// TestGeminiStreamGenerateContentEmitsGeminiSSE pins the streaming action: the
// client receives Gemini frames and no OpenAI [DONE] sentinel.
func TestGeminiStreamGenerateContentEmitsGeminiSSE(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, streamResponder())
	env.AddConnection(t, "conn-ds-stream", "deepseek", "DeepSeek Stream", upstream, "sk-upstream")

	body := map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "hi"}}},
		},
	}
	res := env.Post(t, "/v1beta/models/deepseek/deepseek-chat:streamGenerateContent", body)
	if res.Status != http.StatusOK {
		t.Fatalf("POST :streamGenerateContent = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	if strings.Contains(string(res.Body), "[DONE]") {
		t.Error("body contains the OpenAI [DONE] sentinel; Gemini SSE ends by closing the stream")
	}
	if !strings.Contains(string(res.Body), `"role":"model"`) {
		t.Errorf("body carries no Gemini candidate (body: %s)", truncate(res.Body))
	}
	if !strings.Contains(string(res.Body), `"finishReason":"STOP"`) {
		t.Errorf("body carries no Gemini finishReason (body: %s)", truncate(res.Body))
	}
	if strings.Contains(string(res.Body), `"choices"`) {
		t.Errorf("body still carries OpenAI choices (body: %s)", truncate(res.Body))
	}
}

// TestGeminiModelsListUsesGeminiShape pins the discovery document a Gemini SDK
// reads before it sends anything.
func TestGeminiModelsListUsesGeminiShape(t *testing.T) {
	env, _ := newProviderEnv(t)

	res := env.Get(t, "/v1beta/models")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /v1beta/models = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var got struct {
		Models []struct {
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	res.Decode(t, &got)
	if len(got.Models) == 0 {
		t.Fatal("models is empty, want the connected provider's catalog")
	}
	for _, model := range got.Models {
		if !strings.HasPrefix(model.Name, "models/") {
			t.Fatalf("model name = %q, want the models/ prefix", model.Name)
		}
		if len(model.SupportedGenerationMethods) == 0 {
			t.Fatalf("model %q lists no supportedGenerationMethods", model.Name)
		}
	}
}
