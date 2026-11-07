package translator

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

func decodeKiro(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal kiro envelope: %v", err)
	}
	return m
}

func TestOpenAIToKiro_BuildsConversationState(t *testing.T) {
	body := []byte(`{"model":"kr/claude-sonnet-4.5","messages":[{"role":"user","content":"hi"}],"max_tokens":1024,"stream":false}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "claude-sonnet-4.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := decodeKiro(t, out)
	if _, ok := m["systemPrompt"]; ok {
		t.Error("top-level systemPrompt must not be present (gateway 400s on it)")
	}
	cs, ok := m["conversationState"].(map[string]any)
	if !ok {
		t.Fatalf("missing conversationState, got keys %v", m)
	}
	if cs["chatTriggerType"] != "MANUAL" {
		t.Errorf("chatTriggerType = %v, want MANUAL", cs["chatTriggerType"])
	}
	if _, ok := cs["conversationId"].(string); !ok {
		t.Error("missing conversationId")
	}
	cur, ok := cs["currentMessage"].(map[string]any)
	if !ok {
		t.Fatal("missing currentMessage")
	}
	uim, ok := cur["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("missing currentMessage.userInputMessage")
	}
	if uim["modelId"] != "claude-sonnet-4.5" {
		t.Errorf("modelId = %v, want claude-sonnet-4.5", uim["modelId"])
	}
	if uim["origin"] != "AI_EDITOR" {
		t.Errorf("origin = %v, want AI_EDITOR", uim["origin"])
	}
	content, _ := uim["content"].(string)
	if !strings.Contains(content, "hi") {
		t.Errorf("current content should carry the user text, got %q", content)
	}
	// system/time prefix must be inside the user turn, not top-level.
	if !strings.Contains(content, "[Context: Current time is") {
		t.Errorf("expected time context inside user turn, got %q", content)
	}
	ic, ok := m["inferenceConfig"].(map[string]any)
	if !ok {
		t.Fatal("missing inferenceConfig")
	}
	mt, _ := ic["maxTokens"].(float64)
	if mt != kiroDefaultMaxTokens {
		t.Errorf("maxTokens = %v, want %d", ic["maxTokens"], kiroDefaultMaxTokens)
	}
}

func TestOpenAIToKiro_SystemBecomesInstructionsInUserTurn(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"You are terse."},{"role":"user","content":"hello"}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "claude-sonnet-4.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	uim := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	content, _ := uim["content"].(string)
	if !strings.Contains(content, "<instructions>") || !strings.Contains(content, "You are terse.") {
		t.Errorf("system prompt must be wrapped in <instructions> inside the user turn, got %q", content)
	}
}

func TestOpenAIToKiro_MultiTurnHistoryAlternates(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"system","content":"sys"},
		{"role":"user","content":"first"},
		{"role":"assistant","content":"answer1"},
		{"role":"user","content":"second"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)
	if len(hist) == 0 {
		t.Fatal("expected history turns")
	}
	// The trailing user turn is the current message; history holds earlier turns.
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	if c, _ := cur["content"].(string); !strings.Contains(c, "second") {
		t.Errorf("current turn should be the last user message, got %q", c)
	}
	// No two adjacent user turns (Kiro requires alternation).
	prevUser := false
	for _, h := range hist {
		turn := h.(map[string]any)
		_, isUser := turn["userInputMessage"]
		if isUser && prevUser {
			t.Error("history has consecutive user turns")
		}
		prevUser = isUser
		if isUser {
			uim := turn["userInputMessage"].(map[string]any)
			if uim["modelId"] != "m" {
				t.Errorf("history user turn modelId = %v, want m", uim["modelId"])
			}
		}
	}
}

func TestOpenAIToKiro_ToolCallsAndResults(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"sunny"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)
	var toolUseID string
	for _, raw := range hist {
		arm, ok := raw.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		uses, _ := arm["toolUses"].([]any)
		if len(uses) == 0 {
			continue
		}
		use := uses[0].(map[string]any)
		toolUseID, _ = use["toolUseId"].(string)
		if use["name"] != "get_weather" || !isKiroUUID(toolUseID) {
			t.Fatalf("tool use ID should be a UUID: %v", use)
		}
	}
	if toolUseID == "" {
		t.Fatal("assistant history must include toolUses")
	}
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	ctx := cur["userInputMessageContext"].(map[string]any)
	results := ctx["toolResults"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["toolUseId"] != toolUseID {
		t.Fatalf("tool result must use matching normalized UUID, got %v; use ID %q", results, toolUseID)
	}
}

func TestOpenAIToKiro_MapsToolResultsWhenToolCallUsesClaudeBlock(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_source","name":"bash","input":{"cmd":"pwd"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_source","content":"/workspace"}]}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cs := decodeKiro(t, out)["conversationState"].(map[string]any)
	history := cs["history"].([]any)
	use := history[0].(map[string]any)["assistantResponseMessage"].(map[string]any)["toolUses"].([]any)[0].(map[string]any)
	useID := use["toolUseId"].(string)
	if !isKiroUUID(useID) {
		t.Fatalf("tool use id = %q, want UUID", useID)
	}
	current := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	result := current["userInputMessageContext"].(map[string]any)["toolResults"].([]any)[0].(map[string]any)
	if result["toolUseId"] != useID {
		t.Fatalf("tool result id = %v, want matching tool use id %q", result["toolUseId"], useID)
	}
}

func TestOpenAIToKiro_PreservesUUIDToolID(t *testing.T) {
	const id = "123e4567-e89b-42d3-a456-426614174000"
	if !isKiroUUID(id) {
		t.Fatal("test UUID is invalid")
	}
	got := normalizeKiroToolID(id, make(map[string]string))
	if got != id {
		t.Fatalf("valid UUID changed: got %q, want %q", got, id)
	}
}

func TestOpenAIToKiro_ImageBecomesKiroBlock(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"text","text":"what is this?"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
	]}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	uim := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	imgs, _ := uim["images"].([]any)
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image, got %v", uim["images"])
	}
	img := imgs[0].(map[string]any)
	if img["format"] != "png" {
		t.Errorf("format = %v, want png", img["format"])
	}
	src := img["source"].(map[string]any)
	if src["bytes"] != "AAAA" {
		t.Errorf("source.bytes = %v, want AAAA", src["bytes"])
	}
}

func TestOpenAIToKiro_ProfileArnOnlyWhenProvided(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m", ProfileArn: "arn:aws:codewhisperer:us-east-1:1:profile/p"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decodeKiro(t, out)["profileArn"] != "arn:aws:codewhisperer:us-east-1:1:profile/p" {
		t.Error("profileArn should be forwarded when provided")
	}

	out2, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := decodeKiro(t, out2)["profileArn"]; ok {
		t.Error("profileArn must be omitted when empty (shared placeholder belongs to another account)")
	}
}

func TestOpenAIToKiro_EmptyUserTurnGetsPlaceholder(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":""}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	uim := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	c, _ := uim["content"].(string)
	if !strings.Contains(c, kiroEmptyUserPlaceholder) {
		t.Errorf("empty user turn should get %q placeholder, got %q", kiroEmptyUserPlaceholder, c)
	}
}
