package chat

import (
	"context"
	"os"
	"testing"

	"nox9go/proxy/internal/db"
)

// TestSyncPiCatalog_RefusesToPublishWhenReadsFail pins the rule that keeps the
// gateway from truncating the operator's Pi catalogue at startup: when a read
// the catalogue depends on fails, the sync reports it and publishes nothing.
//
// The reads themselves deliberately discard their errors inside
// BuildModelsListResult — an HTTP request still answers with whatever it has —
// but the mirror is a file, and a short file carries no hint that it is short.
// A gateway that published half a catalogue looked healthy while every model
// list behind it was wrong.
func TestSyncPiCatalog_RefusesToPublishWhenReadsFail(t *testing.T) {
	piFile, gsFile := seedPiFiles(t)
	database, cleanup := setupChatTestDB(t)
	// Closing the handle underneath the repository is the cheapest faithful
	// stand-in for the startup state that produced the damage: the process is
	// up, the catalogue reads fail, and the file on disk is still the good one.
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	changed, err := h.SyncPiCatalog(context.Background())
	if err == nil {
		t.Fatal("SyncPiCatalog must report failed reads instead of publishing a partial catalogue")
	}
	if changed {
		t.Fatalf("changed = true, want false: a failed read must not publish anything")
	}
	for _, f := range []string{piFile, gsFile} {
		raw, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		if string(raw) != piSeed {
			t.Fatalf("%s was rewritten from a failed read:\n%s", f, raw)
		}
	}
}
