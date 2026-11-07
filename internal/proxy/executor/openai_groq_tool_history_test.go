package executor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nox9go/proxy/internal/providers"
)

func TestForwardOpenAI_FlattensGroqToolHistory(t *testing.T) {
	var forwarded map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&forwarded); err != nil {
			t.Errorf("decode forwarded request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	body := []byte(`{"model":"qwen/qwen3.8-27b","messages":[
		{"role":"user","content":"run command"},
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"pwd\"}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"/workspace"},
		{"role":"user","content":"thanks"}
	]}`)
	if err := ForwardOpenAI(httptest.NewRecorder(), &Request{
		Ctx: t.Context(), Client: srv.Client(),
		Config:   &providers.ProviderConfig{BaseURL: srv.URL},
		Provider: "groq", Body: body,
	}); err != nil {
		t.Fatalf("ForwardOpenAI: %v", err)
	}

	messages := forwarded["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("message count = %d, want 4: %v", len(messages), messages)
	}
	for i, raw := range messages {
		message := raw.(map[string]any)
		if message["role"] == "tool" || message["tool_calls"] != nil {
			t.Fatalf("message %d retained unsupported tool structure: %v", i, message)
		}
	}
	assistant := messages[1].(map[string]any)
	if assistant["role"] != "assistant" || assistant["content"] != "[Call tool bash({\"cmd\":\"pwd\"})]" {
		t.Fatalf("assistant call was not represented as prose: %v", assistant)
	}
	toolResult := messages[2].(map[string]any)
	if toolResult["role"] != "user" || toolResult["content"] != "[Tool result]\n/workspace" {
		t.Fatalf("tool result was not represented as user prose: %v", toolResult)
	}
}
