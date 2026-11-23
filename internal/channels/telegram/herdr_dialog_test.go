package telegram

import "testing"

func TestParseHerdrDialog_piCursor(t *testing.T) {
	screen := `
 Herdr bridge self-test
 Continue with the bridge test?

 → Yes
   No

 ↑↓ navigate  enter select  escape/ctrl+c cancel
`
	_, ch := ParseHerdrDialog(screen)
	if len(ch) != 2 {
		t.Fatalf("choices = %d, want 2", len(ch))
	}
	if ch[0].Label != "Yes" || len(ch[0].Keys) != 1 || ch[0].Keys[0] != "enter" {
		t.Errorf("first choice = %+v, want enter on Yes", ch[0])
	}
	if ch[1].Label != "No" || len(ch[1].Keys) != 2 || ch[1].Keys[0] != "down" || ch[1].Keys[1] != "enter" {
		t.Errorf("second choice = %+v, want down+enter on No", ch[1])
	}
}

func TestParseHerdrDialog_confirmPrompt(t *testing.T) {
	screen := `
¿Seguimos con la prueba de Telegram?

→ Sí, continuar con la prueba de Telegram
  No continuar por ahora
Confirmar
Cancelar
━━━━━━━━━
 ↑↓ navigate  enter select  escape/ctrl+c cancel
`
	title, ch := ParseHerdrDialog(screen)
	if title != "¿Seguimos con la prueba de Telegram?" {
		t.Errorf("title = %q", title)
	}
	if len(ch) != 2 {
		t.Fatalf("choices = %d, want 2: %+v", len(ch), ch)
	}
	if ch[0].Label != "Sí, continuar con la prueba de Telegram" {
		t.Errorf("first = %q", ch[0].Label)
	}
}

func TestParseHerdrDialog_piConfirmSubtitles(t *testing.T) {
	screen := `────────────────────────────────────────────────────────────────────────────────────────
 ¿Seguimos con la prueba de Telegram?
 → Confirmar
    Sí, continuar con la prueba de Telegram
   Cancelar
    No continuar por ahora
 ↑↓ navigate • Enter select • Esc cancel
────────────────────────────────────────────────────────────────────────────────────────`
	title, ch := ParseHerdrDialog(screen)
	if title != "¿Seguimos con la prueba de Telegram?" {
		t.Errorf("title = %q", title)
	}
	if len(ch) != 2 {
		t.Fatalf("choices = %d, want 2: %+v", len(ch), ch)
	}
	if ch[0].Label != "Sí, continuar con la prueba de Telegram" || ch[0].Keys[0] != "enter" {
		t.Errorf("confirm = %+v", ch[0])
	}
	if ch[1].Label != "No continuar por ahora" || ch[1].Keys[0] != "down" || ch[1].Keys[1] != "enter" {
		t.Errorf("cancel = %+v", ch[1])
	}
}

func TestParseHerdrDialog_numbered(t *testing.T) {
	screen := `
Pick one:

1. Toast
2. Pane text
`
	_, ch := ParseHerdrDialog(screen)
	if len(ch) != 2 || ch[0].Keys[0] != "1" || ch[1].Keys[0] != "2" {
		t.Fatalf("choices = %+v", ch)
	}
}