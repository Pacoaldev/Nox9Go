package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestIsHerdrFocusedTarget(t *testing.T) {
	for _, spec := range []string{"focused", "FOCUSED", "@focused", "*", " focused "} {
		if !IsHerdrFocusedTarget(spec) {
			t.Errorf("IsHerdrFocusedTarget(%q) = false, want true", spec)
		}
	}
	if IsHerdrFocusedTarget("nox") {
		t.Error("literal name should not be focused target")
	}
}

func TestPickFocusedHerdrAgentFromJSON(t *testing.T) {
	raw := `{"result":{"agents":[{"name":"pepe","focused":false},{"name":"maria","focused":true}]}}`
	got, err := pickFocusedHerdrAgentFromJSON(raw)
	if err != nil || got != "maria" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestPickFocusedHerdrAgentFromJSON_none(t *testing.T) {
	_, err := pickFocusedHerdrAgentFromJSON(`{"result":{"agents":[{"name":"a","focused":false}]}}`)
	if err == nil || !strings.Contains(err.Error(), "no focused") {
		t.Fatalf("err = %v", err)
	}
}

func TestPickFocusedHerdrAgentFromJSON_many(t *testing.T) {
	raw := `{"result":{"agents":[{"name":"a","focused":true},{"name":"b","focused":true}]}}`
	_, err := pickFocusedHerdrAgentFromJSON(raw)
	if err == nil || !strings.Contains(err.Error(), "multiple focused") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveHerdrAgentName_literal(t *testing.T) {
	got, err := ResolveHerdrAgentName(context.Background(), "nox")
	if err != nil || got != "nox" {
		t.Fatalf("got %q err=%v", got, err)
	}
}
