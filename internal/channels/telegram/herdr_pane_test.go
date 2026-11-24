package telegram

import "testing"

func TestPickFocusedHerdrPaneFromJSON_one(t *testing.T) {
	raw := `{"result":{"panes":[{"pane_id":"wD:p1","focused":true,"cwd":"C:\\Ouroboros"}]}}`
	got, err := pickFocusedHerdrPaneFromJSON(raw, "")
	if err != nil || got != "wD:p1" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestPickFocusedHerdrPaneFromJSON_excludeCwd(t *testing.T) {
	raw := `{"result":{"panes":[
		{"pane_id":"wA:p1","focused":true,"cwd":"C:\\NOX9GO"},
		{"pane_id":"wD:p1","focused":true,"cwd":"C:\\Ouroboros"}
	]}}`
	got, err := pickFocusedHerdrPaneFromJSON(raw, `C:\NOX9GO`)
	if err != nil || got != "wD:p1" {
		t.Fatalf("got %q err=%v", got, err)
	}
}