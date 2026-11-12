package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "encoding/json/v2"
)

func TestParseGeminiModelPath(t *testing.T) {
	tests := []struct {
		path       string
		wantModel  string
		wantStream bool
		wantOK     bool
	}{
		{
			path:      "/v1beta/models/openai/gpt-4o:generateContent",
			wantModel: "openai/gpt-4o",
			wantOK:    true,
		},
		{
			path:       "/v1beta/models/openai/gpt-4o:streamGenerateContent",
			wantModel:  "openai/gpt-4o",
			wantStream: true,
			wantOK:     true,
		},
		{
			path:      "/v1beta/models/gemini-2.5-pro:generateContent",
			wantModel: "gemini-2.5-pro",
			wantOK:    true,
		},
		{
			path:   "/v1beta/models/openai/gpt-4o",
			wantOK: false,
		},
		{
			path:   "/v1beta/models/:generateContent",
			wantOK: false,
		},
		{
			path:   "/v1beta/models",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		model, stream, ok := parseGeminiModelPath(tt.path)
		if ok != tt.wantOK {
			t.Errorf("%s: ok = %v, want %v", tt.path, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if model != tt.wantModel || stream != tt.wantStream {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", tt.path, model, stream, tt.wantModel, tt.wantStream)
		}
	}
}

// TestGeminiRequestToOpenAI_CarriesSystemContentsAndTools pins the conversion a
// Gemini client's request goes through before it reaches the shared chat lane.
func TestGeminiRequestToOpenAI_CarriesSystemContentsAndTools(t *testing.T) {
	temp := 0.4
	req := &geminiNativeRequest{
		SystemInstruction: &geminiNativeContent{
			Parts: []geminiNativePart{{Text: "be terse"}},
		},
		Contents: []geminiNativeContent{
			{Role: "user", Parts: []geminiNativePart{{Text: "hi"}}},
			{Role: "model", Parts: []geminiNativePart{{FunctionCall: &geminiNativeCall{
				Name: "lookup", Args: map[string]any{"q": "x"}, ID: "call_1",
			}}}},
			{Role: "user", Parts: []geminiNativePart{{FunctionResponse: &geminiNativeCallRes{
				Name: "lookup", ID: "call_1",
				Response: &geminiNativeCallResResult{Result: map[string]any{"ok": true}},
			}}}},
		},
		Tools: []geminiNativeTool{{FunctionDeclarations: []geminiNativeFunctionDecl{{
			Name: "lookup", Description: "find things", Parameters: map[string]any{"type": "object"},
		}}}},
		GenerationConfig: &geminiNativeConfig{MaxOutputTokens: 64, Temperature: &temp},
	}

	body, err := geminiRequestToOpenAI(req, "openai/gpt-4o", true)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	var got struct {
		Model     string `json:"model"`
		Stream    bool   `json:"stream"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("converted body is not JSON: %v", err)
	}
	if got.Model != "openai/gpt-4o" || !got.Stream || got.MaxTokens != 64 {
		t.Errorf("envelope = (%q,%v,%d), want (openai/gpt-4o,true,64)", got.Model, got.Stream, got.MaxTokens)
	}
	if len(got.Messages) < 4 {
		t.Fatalf("got %d messages, want system + user + assistant tool call + tool result", len(got.Messages))
	}
	if got.Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want system", got.Messages[0].Role)
	}
	var assistantIdx = -1
	for i, m := range got.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			assistantIdx = i
			break
		}
	}
	if assistantIdx < 0 {
		t.Fatal("no assistant message carried the tool call")
	}
	if got.Messages[assistantIdx].ToolCalls[0].Function.Name != "lookup" {
		t.Errorf("tool call name = %q, want lookup", got.Messages[assistantIdx].ToolCalls[0].Function.Name)
	}
	var toolIdx = -1
	for i, m := range got.Messages {
		if m.Role == "tool" {
			toolIdx = i
			break
		}
	}
	if toolIdx < 0 {
		t.Fatal("no tool result message")
	}
	if got.Messages[toolIdx].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q, want call_1", got.Messages[toolIdx].ToolCallID)
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "lookup" {
		t.Errorf("tools = %+v, want one lookup declaration", got.Tools)
	}
}

// TestConvertOpenAIResponseToGemini_MapsContentToolsAndUsage pins the non-stream
// answer shape a Gemini SDK parses.
func TestConvertOpenAIResponseToGemini_MapsContentToolsAndUsage(t *testing.T) {
	openAI := []byte(`{
		"model":"gpt-4o",
		"choices":[{"message":{"content":"hello","reasoning_content":"thought",
			"tool_calls":[{"id":"c1","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
			"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,
			"completion_tokens_details":{"reasoning_tokens":3}}
	}`)

	rec := httptest.NewRecorder()
	convertOpenAIResponseToGemini(rec, http.StatusOK, openAI, "openai/gpt-4o")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Candidates []struct {
			Content struct {
				Role  string           `json:"role"`
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
			ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a Gemini response: %v (%s)", err, rec.Body.String())
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got.Candidates))
	}
	if got.Candidates[0].FinishReason != "STOP" {
		t.Errorf("finishReason = %q, want STOP", got.Candidates[0].FinishReason)
	}
	if got.Candidates[0].Content.Role != "model" {
		t.Errorf("role = %q, want model", got.Candidates[0].Content.Role)
	}
	parts := got.Candidates[0].Content.Parts
	if len(parts) != 3 {
		t.Fatalf("got %d parts (%v), want thought + text + functionCall", len(parts), parts)
	}
	if parts[0]["text"] != "thought" || parts[0]["thought"] != true {
		t.Errorf("first part = %v, want a thought part", parts[0])
	}
	if parts[1]["text"] != "hello" {
		t.Errorf("second part = %v, want the text", parts[1])
	}
	call, _ := parts[2]["functionCall"].(map[string]any)
	if call == nil || call["name"] != "lookup" {
		t.Errorf("third part = %v, want a functionCall named lookup", parts[2])
	}
	if got.UsageMetadata.PromptTokenCount != 11 || got.UsageMetadata.ThoughtsTokenCount != 3 {
		t.Errorf("usage = %+v, want prompt 11 and thoughts 3", got.UsageMetadata)
	}
	if got.ModelVersion != "gpt-4o" {
		t.Errorf("modelVersion = %q, want gpt-4o", got.ModelVersion)
	}
}

// TestGeminiStreamWriter_EmitsGeminiFramesAndDropsDone pins the streamed answer
// shape: Gemini SSE frames, no [DONE], usage attached to the final frame.
func TestGeminiStreamWriter_EmitsGeminiFramesAndDropsDone(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := newGeminiStreamWriter(rec, "openai/gpt-4o")

	_, _ = sw.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
	_, _ = sw.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	_, _ = sw.Write([]byte("data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"))
	_, _ = sw.Write([]byte("data: [DONE]\n\n"))
	sw.Finish()

	body := rec.Body.String()
	if strings.Contains(body, "[DONE]") {
		t.Error("body contains the OpenAI [DONE] sentinel; Gemini SSE ends by closing the stream")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	frames := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		frames++
		var chunk struct {
			Candidates []struct {
				Content struct {
					Role  string           `json:"role"`
					Parts []map[string]any `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata map[string]any `json:"usageMetadata"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatalf("frame is not a Gemini chunk: %v (%s)", err, line)
		}
		if len(chunk.Candidates) != 1 || chunk.Candidates[0].Content.Role != "model" {
			t.Fatalf("frame = %s, want one model candidate", line)
		}
		if chunk.Candidates[0].FinishReason == "STOP" && chunk.UsageMetadata == nil {
			t.Error("final frame carries no usageMetadata")
		}
	}
	if frames != 2 {
		t.Errorf("got %d frames, want 2 (content + final): the role-only preamble must be dropped", frames)
	}
}

// TestGeminiStreamWriter_ForwardsErrorBody pins that a non-200 from the chat
// lane reaches the client untouched, so the failure is not lost inside the
// SSE converter.
func TestGeminiStreamWriter_ForwardsErrorBody(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := newGeminiStreamWriter(rec, "openai/gpt-4o")
	sw.WriteHeader(http.StatusBadRequest)
	_, _ = sw.Write([]byte(`{"error":{"message":"missing model"}}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing model") {
		t.Errorf("body = %s, want the upstream error text", rec.Body.String())
	}
}
