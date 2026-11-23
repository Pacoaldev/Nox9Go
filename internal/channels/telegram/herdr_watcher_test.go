package telegram

import "testing"

func TestHerdrBlockerSignature_stable(t *testing.T) {
	ch := []HerdrChoice{{Label: "Sí", Keys: []string{"enter"}}, {Label: "No", Keys: []string{"down", "enter"}}}
	a := herdrBlockerSignature("shell", "¿Seguimos?", ch)
	b := herdrBlockerSignature("shell", "¿Seguimos?", ch)
	if a != b || a == "" {
		t.Fatalf("sig = %q", a)
	}
}

func TestHerdrBlockerSignature_changesWithAgent(t *testing.T) {
	ch := []HerdrChoice{{Label: "Ok", Keys: []string{"enter"}}}
	if herdrBlockerSignature("a", "t", ch) == herdrBlockerSignature("b", "t", ch) {
		t.Fatal("expected different agents to produce different signatures")
	}
}