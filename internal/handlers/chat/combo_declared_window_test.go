package chat

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"nox9go/proxy/internal/db"
)

// comboDeclaredWindowLeaf is the /v1/models entry shape this file reads: the
// top-level limits plus the capability block, because a declared window has to
// show up in both places for a client that reads either one.
type comboDeclaredWindowLeaf struct {
	ID                  string `json:"id"`
	ContextLength       *int   `json:"context_length"`
	MaxCompletionTokens *int   `json:"max_completion_tokens"`
	Capabilities        *struct {
		ContextWindow *int `json:"contextWindow"`
	} `json:"capabilities"`
}

// declaredWindowResponse reads the advertised entries of one /v1/models call.
func declaredWindowResponse(t *testing.T, h *ChatHandler) []comboDeclaredWindowLeaf {
	t.Helper()
	w := httptest.NewRecorder()
	h.HandleModels(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Data []comboDeclaredWindowLeaf `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal /v1/models: %v (body: %s)", err, w.Body.String())
	}
	return resp.Data
}

// declaredWindowEntry returns the advertised entry for a combo name, failing
// when the handler dropped it.
func declaredWindowEntry(t *testing.T, entries []comboDeclaredWindowLeaf, name string) comboDeclaredWindowLeaf {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == name {
			return entry
		}
	}
	t.Fatalf("combo %q missing from /v1/models", name)
	return comboDeclaredWindowLeaf{}
}

// An operator-declared window is what the combo publishes, in the top-level
// keys and in the capability block, while max_completion_tokens stays the
// smallest its seats can answer with: a seat that can serve a 1M prompt can
// still be the smallest max output in the tree.
func TestHandleModels_DeclaredComboWindowOverridesSeatMinimum(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	// deepseek/deepseek-chat caps the tree at 131072/8192; the operator declares
	// the 1M the combo's other seats actually serve.
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, contextWindow, createdAt, updatedAt) VALUES
		('c-declared', 'declared-combo', 'llm', '["deepseek/deepseek-chat","anthropic/claude-sonnet-4-6"]', 1000000, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed combo: %v", err)
	}
	entry := declaredWindowEntry(t, declaredWindowResponse(t, NewChatHandler(db.NewRepo(database))), "declared-combo")
	wantLimit(t, "context_length", entry.ContextLength, 1000000)
	wantLimit(t, "max_completion_tokens", entry.MaxCompletionTokens, 8192)
	if entry.Capabilities == nil {
		t.Fatalf("capabilities absent, want the declared contextWindow beside the limits")
	}
	wantLimit(t, "capabilities.contextWindow", entry.Capabilities.ContextWindow, 1000000)
}

// A combo with no declared window publishes the seat minimum exactly as before.
// The row omits the column entirely, which is what every combo written before
// the field existed looks like once the additive migration has run.
func TestHandleModels_ComboWithoutDeclaredWindowKeepsSeatMinimum(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
		('c-underived', 'underived-combo', 'llm', '["deepseek/deepseek-chat","anthropic/claude-sonnet-4-6"]', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed combo: %v", err)
	}
	entry := declaredWindowEntry(t, declaredWindowResponse(t, NewChatHandler(db.NewRepo(database))), "underived-combo")
	wantLimit(t, "context_length", entry.ContextLength, 131072)
	wantLimit(t, "max_completion_tokens", entry.MaxCompletionTokens, 8192)
	if entry.Capabilities == nil {
		t.Fatalf("capabilities absent, want the derived contextWindow beside the limits")
	}
	wantLimit(t, "capabilities.contextWindow", entry.Capabilities.ContextWindow, 131072)
}

// A declared window of zero is the derived one, so a row that explicitly
// carries 0 must behave like a row that omits the column.
func TestHandleModels_ZeroDeclaredWindowFallsBackToSeatMinimum(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, contextWindow, createdAt, updatedAt) VALUES
		('c-zero', 'zero-combo', 'llm', '["deepseek/deepseek-chat","anthropic/claude-sonnet-4-6"]', 0, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed combo: %v", err)
	}
	entry := declaredWindowEntry(t, declaredWindowResponse(t, NewChatHandler(db.NewRepo(database))), "zero-combo")
	wantLimit(t, "context_length", entry.ContextLength, 131072)
}

// The declared window has to survive the whole way to the file the client
// reads. Pi never queries /v1/models: it reads the catalogue the sync mirrors
// into models.json, so a combo that only declares a window must be written
// there with that window rather than with the derived minimum.
func TestSyncPiCatalog_WritesDeclaredComboWindow(t *testing.T) {
	piFile, gsFile := seedPiFiles(t)

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("delete connections: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, contextWindow, createdAt, updatedAt) VALUES
		('c-mirror', 'mirror-combo', 'llm', '["deepseek/deepseek-chat"]', 1000000, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed combo: %v", err)
	}

	h := NewChatHandler(db.NewRepo(database))
	if _, err := h.SyncPiCatalog(context.Background()); err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}

	// The array entry for the combo, whitespace-tolerant: the mirror is written
	// as JSON text and one entry is all this asserts about it.
	entryWindow := regexp.MustCompile(`(?s)"id":\s*"mirror-combo".{0,400}?"contextWindow":\s*1000000`)
	for _, f := range []string{piFile, gsFile} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !entryWindow.Match(raw) {
			t.Errorf("%s: mirror-combo does not carry the declared 1000000 window:\n%s", f, string(raw))
		}
	}
}
