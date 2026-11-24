package media

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/dbtest"
	"nox9go/proxy/internal/handlers/chat"
)

// newPiApplyEnv prepares a temporary home whose Pi models.json already carries
// a NOX9GO provider block, plus a ChatHandler over a real SQLite database so the
// post-apply catalogue sync has a catalogue to write.
func newPiApplyEnv(t *testing.T) (*chat.ChatHandler, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	seed := "{\n  \"providers\": {\n    \"nox9go\": {\n      \"name\": \"NOX9GO\",\n" +
		"      \"baseUrl\": \"http://127.0.0.1:20130/v1\",\n      \"api\": \"openai-completions\",\n" +
		"      \"models\": [ { \"id\": \"stale-model\" } ]\n    }\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	tmp, err := os.CreateTemp("", "test_cli_settings_*.sqlite")
	if err != nil {
		t.Fatalf("temp db: %v", err)
	}
	tmp.Close()
	t.Cleanup(func() { os.Remove(tmp.Name()) })

	database, err := db.OpenDatabase(tmp.Name())
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := dbtest.CreateTables(database); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("delete connections: %v", err)
	}
	return chat.NewChatHandler(db.NewRepo(database)), filepath.Join(dir, "models.json")
}

// TestHandleApply_PiRewritesTheCatalogueAfterWritingTheProvider proves applying
// Pi settings also refreshes the model array. The adapter writes baseUrl, api
// and apiKey; without the follow-up sync the client kept the array it was
// installed with, so a model added in the dashboard never reached Pi until the
// operator re-applied the tool settings by hand.
func TestHandleApply_PiRewritesTheCatalogueAfterWritingTheProvider(t *testing.T) {
	chatHandler, modelsFile := newPiApplyEnv(t)

	h := NewCLIToolHandler(chatHandler)
	body := `{"baseUrl":"http://127.0.0.1:20130","apiKey":"sk-test","model":"MinionsPRO"}`
	req := httptest.NewRequest(http.MethodPost, "/api/cli-tools/pi-settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleApply(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(modelsFile)
	if err != nil {
		t.Fatalf("read %s: %v", modelsFile, err)
	}
	got := string(raw)
	if strings.Contains(got, "stale-model") {
		t.Errorf("the apply left the stale model array in place:\n%s", got)
	}
	if !strings.Contains(got, `"id"`) {
		t.Errorf("the apply did not write a model catalogue:\n%s", got)
	}
	if !strings.Contains(rec.Body.String(), `"changed":true`) {
		t.Errorf("a rewrite must report changed:true, body = %s", rec.Body.String())
	}
}

// TestHandleApply_PiWithoutChatHandlerStillApplies proves the optional
// ChatHandler is genuinely optional: the handler built by the route table when
// no chat engine is wired must still write the provider block instead of
// panicking on a nil sync.
func TestHandleApply_PiWithoutChatHandlerStillApplies(t *testing.T) {
	_, modelsFile := newPiApplyEnv(t)

	h := NewCLIToolHandler()
	body := `{"baseUrl":"http://127.0.0.1:20130","apiKey":"sk-test","model":"MinionsPRO"}`
	req := httptest.NewRequest(http.MethodPost, "/api/cli-tools/pi-settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleApply(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(modelsFile)
	if err != nil {
		t.Fatalf("read %s: %v", modelsFile, err)
	}
	if !strings.Contains(string(raw), "sk-test") {
		t.Errorf("the provider block was not written:\n%s", string(raw))
	}
}
