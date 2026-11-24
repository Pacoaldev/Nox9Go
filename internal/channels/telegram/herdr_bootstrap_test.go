package telegram

import "testing"

func TestAgentNameForPaneFromAgents(t *testing.T) {
	raw := `{"result":{"agents":[{"name":"shell","pane_id":"wD:p1"},{"name":"nox","pane_id":"w0:p2"}]}}`
	if got := agentNameForPaneFromAgents(raw, "wD:p1"); got != "shell" {
		t.Fatalf("got %q, want shell", got)
	}
	if got := agentNameForPaneFromAgents(raw, "missing"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}