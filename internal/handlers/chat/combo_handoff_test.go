package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInjectContextHandoffNote_NoChange(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	out := injectContextHandoffNote(body, "", "gpt-4")
	if string(out) != string(body) {
		t.Errorf("expected no change when prevModel is empty, got: %s", string(out))
	}

	out = injectContextHandoffNote(body, "gpt-4", "gpt-4")
	if string(out) != string(body) {
		t.Errorf("expected no change when models are identical, got: %s", string(out))
	}
}

func TestInjectContextHandoffNote_ModelSwitched(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"turn 1"},{"role":"assistant","content":"ans 1"},{"role":"user","content":"turn 2"}]}`)
	out := injectContextHandoffNote(body, "openai/gpt-4o", "anthropic/claude-3-5-sonnet")

	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if len(parsed.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(parsed.Messages))
	}

	// Handoff note should be inserted before the last message
	noteMsg := parsed.Messages[2]
	if noteMsg.Role != "system" {
		t.Errorf("note message role = %q, want system", noteMsg.Role)
	}
	if !strings.Contains(noteMsg.Content, "Context handoff") {
		t.Errorf("note content missing 'Context handoff': %s", noteMsg.Content)
	}
	if !strings.Contains(noteMsg.Content, "openai/gpt-4o") || !strings.Contains(noteMsg.Content, "anthropic/claude-3-5-sonnet") {
		t.Errorf("note content missing model names: %s", noteMsg.Content)
	}

	// Last message should still be turn 2
	lastMsg := parsed.Messages[3]
	if lastMsg.Role != "user" || lastMsg.Content != "turn 2" {
		t.Errorf("last message altered: %+v", lastMsg)
	}
}

func TestRecordComboTurn_Tracking(t *testing.T) {
	h := &ChatHandler{}

	// First turn on model A: no switch
	prev, switched := h.recordComboTurn("combo1", "modelA")
	if switched {
		t.Errorf("first turn should not report switched, got prev=%q", prev)
	}

	// Second turn on model A: no switch
	prev, switched = h.recordComboTurn("combo1", "modelA")
	if switched {
		t.Errorf("same model should not report switched, got prev=%q", prev)
	}

	// Third turn on model B: switch reported!
	prev, switched = h.recordComboTurn("combo1", "modelB")
	if !switched || prev != "modelA" {
		t.Errorf("expected switch from modelA, got switched=%v prev=%q", switched, prev)
	}

	// Fourth turn on model B: no switch
	prev, switched = h.recordComboTurn("combo1", "modelB")
	if switched {
		t.Errorf("staying on modelB should not report switched, got prev=%q", prev)
	}
}

func TestInjectHandoffNoteMap_ModelSwitched(t *testing.T) {
	req := map[string]any{
		"model": "claude-sonnet-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "turn 1"},
			map[string]any{"role": "assistant", "content": "ans 1"},
			map[string]any{"role": "user", "content": "turn 2"},
		},
	}

	out := injectHandoffNoteMap(req, "openai/gpt-4o", "anthropic/claude-3-5-sonnet")

	msgs, ok := out["messages"].([]any)
	if !ok {
		t.Fatalf("messages is %T, want []any", out["messages"])
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	note, ok := msgs[2].(map[string]any)
	if !ok {
		t.Fatalf("handoff message is %T, want map", msgs[2])
	}
	if note["role"] != "system" {
		t.Errorf("note role = %v, want system", note["role"])
	}
	text, _ := note["content"].(string)
	if !strings.Contains(text, "openai/gpt-4o") || !strings.Contains(text, "anthropic/claude-3-5-sonnet") {
		t.Errorf("note content missing model names: %q", text)
	}

	last, _ := msgs[3].(map[string]any)
	if last["content"] != "turn 2" {
		t.Errorf("last message altered: %+v", last)
	}
}

func TestInjectHandoffNoteMap_NoSwitchIsUntouched(t *testing.T) {
	req := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "only"}},
	}

	for _, tc := range []struct{ prev, next string }{
		{"", "modelA"},
		{"modelA", "modelA"},
	} {
		out := injectHandoffNoteMap(req, tc.prev, tc.next)
		msgs, _ := out["messages"].([]any)
		if len(msgs) != 1 {
			t.Errorf("prev=%q next=%q: messages = %d, want 1 untouched", tc.prev, tc.next, len(msgs))
		}
	}
}
