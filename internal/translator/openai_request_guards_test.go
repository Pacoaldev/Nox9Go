package translator

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMaxTokensLimitFromError(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		want  int
		found bool
	}{
		{
			name: "groq backticked phrasing",
			body: "{\"error\":{\"message\":\"`max_tokens` must be less than or equal to `16384`, the maximum value for `max_tokens` is less than the `context_window` for this model\",\"type\":\"invalid_request_error\",\"param\":\"max_tokens\"}}",
			want: 16384, found: true,
		},
		{
			name: "max_completion_tokens variant",
			body: `{"error":{"message":"max_completion_tokens must be less than or equal to 8192"}}`,
			want: 8192, found: true,
		},
		{
			name: "unrelated 400",
			body: `{"error":{"message":"invalid tool schema"}}`,
		},
		{
			name: "empty",
			body: "",
		},
		{
			name: "names the field but not a cap",
			body: `{"error":{"message":"max_tokens is required"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MaxTokensLimitFromError([]byte(tt.body))
			if ok != tt.found {
				t.Fatalf("found = %v, want %v", ok, tt.found)
			}
			if ok && got != tt.want {
				t.Errorf("limit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestClampMaxTokens(t *testing.T) {
	body := []byte(`{"model":"qwen/qwen3.8-27b","messages":[],"max_tokens":64000,"max_completion_tokens":64000}`)
	out := ClampMaxTokens(body, 16384)
	var req map[string]any
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("clamped body is not JSON: %v", err)
	}
	if req["max_tokens"] != float64(16384) {
		t.Errorf("max_tokens = %v, want 16384", req["max_tokens"])
	}
	if req["max_completion_tokens"] != float64(16384) {
		t.Errorf("max_completion_tokens = %v, want 16384", req["max_completion_tokens"])
	}
	if req["model"] != "qwen/qwen3.8-27b" {
		t.Errorf("model was dropped: %v", req["model"])
	}
}

func TestClampMaxTokens_LeavesCompliantBodyAlone(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":1000}`)
	if out := ClampMaxTokens(body, 16384); string(out) != string(body) {
		t.Errorf("a body already under the cap was rewritten: %s", out)
	}
	if out := ClampMaxTokens(body, 0); string(out) != string(body) {
		t.Errorf("a zero limit is not a cap and must not rewrite the body: %s", out)
	}
}

func TestBackfillOpenAIToolCallSignatures(t *testing.T) {
	body := []byte(`{
		"model":"gemini-3.6-flash",
		"messages":[
			{"role":"user","content":"read the file"},
			{"role":"assistant","tool_calls":[
				{"id":"call_a","type":"function","function":{"name":"read","arguments":"{}"}}
			]},
			{"role":"tool","tool_call_id":"call_a","content":"ok"}
		]
	}`)
	out := BackfillOpenAIToolCallSignatures(body, "", "gemini-3.6-flash")
	if !strings.Contains(string(out), `"extra_content"`) {
		t.Fatalf("no extra_content injected: %s", out)
	}
	var req map[string]any
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	msgs := req["messages"].([]any)
	assistant := msgs[1].(map[string]any)
	call := assistant["tool_calls"].([]any)[0].(map[string]any)
	sig := googleThoughtSignature(call)
	if sig == "" {
		t.Fatal("thought_signature not set on the tool call")
	}
	// The user and tool turns must be untouched.
	if msgs[0].(map[string]any)["tool_calls"] != nil {
		t.Error("a non-assistant turn gained tool_calls")
	}
}

func TestBackfillOpenAIToolCallSignatures_KeepsExistingSignature(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","tool_calls":[
		{"id":"c1","type":"function","function":{"name":"x","arguments":"{}"},
		 "extra_content":{"google":{"thought_signature":"REAL_SIG"}}}]}]}`)
	out := BackfillOpenAIToolCallSignatures(body, "", "gemini")
	if !strings.Contains(string(out), "REAL_SIG") {
		t.Fatalf("existing signature was overwritten: %s", out)
	}
	if strings.Contains(string(out), DefaultThinkingSignature) {
		t.Error("the default replaced a real signature")
	}
}

func TestBackfillOpenAIToolCallSignatures_NoToolCallsIsNoOp(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if out := BackfillOpenAIToolCallSignatures(body, "", "gemini"); string(out) != string(body) {
		t.Errorf("body without tool_calls was rewritten: %s", out)
	}
}

func TestBackfillOpenAIToolCallSignatures_UsesCachedSignature(t *testing.T) {
	const id = "call_cached_1"
	StoreGeminiThoughtSignature(id, "CACHED_SIG", "sess-x", "gemini-3.6-flash")
	body := []byte(`{"messages":[{"role":"assistant","tool_calls":[
		{"id":"` + id + `","type":"function","function":{"name":"read","arguments":"{}"}}]}]}`)
	out := BackfillOpenAIToolCallSignatures(body, "sess-x", "gemini-3.6-flash")
	if !strings.Contains(string(out), "CACHED_SIG") {
		t.Errorf("cached signature not replayed: %s", out)
	}
}

func TestHarvestOpenAIToolCallSignatures(t *testing.T) {
	const id = "call_harvest_1"
	resp := []byte(`{"choices":[{"message":{"tool_calls":[
		{"id":"` + id + `","type":"function","function":{"name":"read","arguments":"{}"},
		 "extra_content":{"google":{"thought_signature":"HARVESTED_SIG"}}}]}}]}`)
	if n := HarvestOpenAIToolCallSignatures(resp, "sess-h", "gemini-3.6-flash"); n != 1 {
		t.Fatalf("stored %d signatures, want 1", n)
	}
	got := GetGeminiThoughtSignature(id, "sess-h", "gemini-3.6-flash")
	if got != "HARVESTED_SIG" {
		t.Errorf("stored signature = %q, want HARVESTED_SIG", got)
	}
}

func TestSanitizeOpenAITools_CleansResponseFormatJsonSchema(t *testing.T) {
	body := []byte(`{"model":"@cf/meta/llama-3.1-8b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"response","strict":true,"schema":{"type":"object","additionalProperties":false,"properties":{"scores":{"type":"object","propertyNames":{"type":"string"},"additionalProperties":{"type":"number"}}},"required":["scores"]}}}}`)
	out, err := SanitizeOpenAITools(body)
	if err != nil {
		t.Fatalf("SanitizeOpenAITools error = %v", err)
	}
	s := string(out)
	if strings.Contains(s, `"propertyNames"`) {
		t.Fatalf("expected propertyNames to be stripped from response_format.json_schema.schema, got %s", s)
	}
	if !strings.Contains(s, `"additionalProperties":false`) || !strings.Contains(s, `"additionalProperties":{"type":"number"}`) {
		t.Fatalf("expected additionalProperties to be preserved in response_format.json_schema.schema, got %s", s)
	}
	if strings.Contains(s, `"reason"`) {
		t.Fatalf("expected tool placeholder reason NOT to be injected into response_format.json_schema.schema, got %s", s)
	}
}
