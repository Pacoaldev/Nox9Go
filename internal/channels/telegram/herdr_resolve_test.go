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

func TestPickFocusedHerdrAgentFromJSON_prefersShellOverNox(t *testing.T) {
	raw := `{"result":{"agents":[
		{"name":"nox","focused":true,"cwd":"C:\\NOX9GO"},
		{"name":"shell","focused":true,"cwd":"C:\\Ouroboros"}
	]}}`
	got, err := pickFocusedHerdrAgentFromJSON(raw)
	if err != nil || got != "shell" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestPickHerdrAgentFromJSON_workdir(t *testing.T) {
	raw := `{"result":{"agents":[
		{"name":"shell","focused":false,"cwd":"C:\\Ouroboros"},
		{"name":"nox","focused":true,"cwd":"C:\\NOX9GO"}
	]}}`
	got, err := pickHerdrAgentFromJSON(raw, true, `C:\Ouroboros`, "")
	if err != nil || got != "shell" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestResolveHerdrAgentName_literal(t *testing.T) {
	got, err := ResolveHerdrAgentName(context.Background(), "nox")
	if err != nil || got != "nox" {
		t.Fatalf("got %q err=%v", got, err)
	}
}
