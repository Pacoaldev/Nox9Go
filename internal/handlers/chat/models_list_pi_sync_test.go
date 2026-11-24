package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/providers"
)

// piSeed is a models.json shaped like the ones the Pi and Gentle Shell adapters
// write: a NOX9GO provider block pointing at this gateway, a stale model array,
// a comment the splice must preserve and a foreign provider it must not touch.
const piSeed = "{\n" +
	"  // operator comment\n" +
	"  \"providers\": {\n" +
	"    \"nox9go\": {\n" +
	"      \"name\": \"NOX9GO\",\n" +
	"      \"baseUrl\": \"http://127.0.0.1:20130/v1\",\n" +
	"      \"api\": \"openai-completions\",\n" +
	"      \"models\": [ { \"id\": \"stale-model\" } ]\n" +
	"    },\n" +
	"    \"other\": { \"baseUrl\": \"https://api.example.com/v1\" }\n" +
	"  }\n" +
	"}\n"

// seedPiFiles writes piSeed into both candidate client files under a temporary
// home and points the process home at it, because SyncPiCatalog resolves the
// real home directory when its argument is empty.
func seedPiFiles(t *testing.T) (piFile, gsFile string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	piDir := filepath.Join(home, ".pi", "agent")
	gsDir := filepath.Join(home, ".gentle-shell", "agent")
	for _, dir := range []string{piDir, gsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	piFile = filepath.Join(piDir, "models.json")
	gsFile = filepath.Join(gsDir, "models.json")
	for _, f := range []string{piFile, gsFile} {
		if err := os.WriteFile(f, []byte(piSeed), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return piFile, gsFile
}

// TestSyncPiCatalog_WritesLiveCatalogueToBothClientFiles proves the sync the
// dashboard triggers after a model or combo write replaces the stale array in
// every NOX9GO provider block while preserving the operator's comment and the
// foreign provider.
func TestSyncPiCatalog_WritesLiveCatalogueToBothClientFiles(t *testing.T) {
	piFile, gsFile := seedPiFiles(t)

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("delete connections: %v", err)
	}
	h := NewChatHandler(db.NewRepo(database))

	changed, err := h.SyncPiCatalog(context.Background())
	if err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}
	if !changed {
		t.Fatal("SyncPiCatalog must report changed when the catalogue differs")
	}
	for _, f := range []string{piFile, gsFile} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		got := string(raw)
		if strings.Contains(got, "stale-model") {
			t.Errorf("%s: stale model survived the sync:\n%s", f, got)
		}
		if !strings.Contains(got, "// operator comment") {
			t.Errorf("%s: the splice destroyed the operator comment:\n%s", f, got)
		}
		if !strings.Contains(got, `"other"`) {
			t.Errorf("%s: the splice destroyed a foreign provider:\n%s", f, got)
		}
		if !strings.Contains(got, `"id"`) {
			t.Errorf("%s: the catalogue was not written:\n%s", f, got)
		}
	}

	// A second sync of an unchanged catalogue must not rewrite the files.
	changedAgain, err := h.SyncPiCatalog(context.Background())
	if err != nil {
		t.Fatalf("second SyncPiCatalog: %v", err)
	}
	if changedAgain {
		t.Error("SyncPiCatalog must report unchanged when the catalogue already matches")
	}
}

// TestSyncPiCatalog_SkipsWhenNoProviderTargetsGateway proves the sync never
// rewrites a client file whose NOX9GO provider block was never created: the
// dashboard write path must not invent a provider entry the operator did not
// configure.
func TestSyncPiCatalog_SkipsWhenNoProviderTargetsGateway(t *testing.T) {
	piFile, gsFile := seedPiFiles(t)
	foreign := "{\n  \"providers\": {\n    \"other\": { \"baseUrl\": \"https://api.example.com/v1\", \"models\": [ { \"id\": \"keep-me\" } ] }\n  }\n}\n"
	for _, f := range []string{piFile, gsFile} {
		if err := os.WriteFile(f, []byte(foreign), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	changed, err := h.SyncPiCatalog(context.Background())
	if err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}
	if changed {
		t.Error("SyncPiCatalog must not report a change for a file without a gateway provider")
	}
	for _, f := range []string{piFile, gsFile} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if string(raw) != foreign {
			t.Errorf("%s: a file without a gateway provider was rewritten:\n%s", f, string(raw))
		}
	}
}

// TestSyncPiCatalog_NilRepoIsANoOp proves the dashboard can hold a ChatHandler
// that was never wired to a database: the best-effort sync path must answer
// "nothing changed" instead of panicking on a nil repository.
func TestSyncPiCatalog_NilRepoIsANoOp(t *testing.T) {
	changed, err := (&ChatHandler{}).SyncPiCatalog(context.Background())
	if err != nil {
		t.Fatalf("SyncPiCatalog with a nil repo: %v", err)
	}
	if changed {
		t.Error("SyncPiCatalog with a nil repo must report unchanged")
	}
	var nilHandler *ChatHandler
	changed, err = nilHandler.SyncPiCatalog(context.Background())
	if err != nil {
		t.Fatalf("SyncPiCatalog on a nil handler: %v", err)
	}
	if changed {
		t.Error("SyncPiCatalog on a nil handler must report unchanged")
	}
}

// TestExtractModelCapsForPi_ReadsEveryShape covers the four concrete capability
// shapes a catalogue entry can carry plus the opaque map the combo aggregation
// path produces. Pi reads reasoning and vision from this projection, so a shape
// that silently reported nothing would strip image input from a vision model.
func TestExtractModelCapsForPi_ReadsEveryShape(t *testing.T) {
	detail := providers.CapabilitiesDetail{Vision: true, Reasoning: true, ContextWindow: 128000, MaxOutput: 4096}
	combo := providers.ComboCapabilities{Vision: true, Reasoning: true, ContextWindow: 64000, MaxOutput: 2048}

	tests := []struct {
		name          string
		raw           any
		wantVision    bool
		wantReasoning bool
		wantWindow    int
		wantMaxOut    int
	}{
		{name: "capability detail value", raw: detail, wantVision: true, wantReasoning: true, wantWindow: 128000, wantMaxOut: 4096},
		{name: "capability detail pointer", raw: &detail, wantVision: true, wantReasoning: true, wantWindow: 128000, wantMaxOut: 4096},
		{name: "combo capabilities value", raw: combo, wantVision: true, wantReasoning: true, wantWindow: 64000, wantMaxOut: 2048},
		{name: "combo capabilities pointer", raw: &combo, wantVision: true, wantReasoning: true, wantWindow: 64000, wantMaxOut: 2048},
		{
			name:          "opaque map with thinking instead of reasoning",
			raw:           map[string]any{"vision": true, "thinking": true, "contextWindow": 32000, "maxOutput": 1024},
			wantVision:    true,
			wantReasoning: true,
			wantWindow:    32000,
			wantMaxOut:    1024,
		},
		{name: "nil capabilities", raw: nil},
		{name: "unsupported scalar", raw: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vision, reasoning, window, maxOut := extractModelCapsForPi(tt.raw)
			if vision != tt.wantVision {
				t.Errorf("vision = %v, want %v", vision, tt.wantVision)
			}
			if reasoning != tt.wantReasoning {
				t.Errorf("reasoning = %v, want %v", reasoning, tt.wantReasoning)
			}
			if window != tt.wantWindow {
				t.Errorf("context window = %d, want %d", window, tt.wantWindow)
			}
			if maxOut != tt.wantMaxOut {
				t.Errorf("max output = %d, want %d", maxOut, tt.wantMaxOut)
			}
		})
	}
}

// TestBuildModelsListResult_IsDeterministic pins the catalogue order.
//
// The order is invisible in a single HTTP response, but SyncPiCatalog writes it
// to disk: a map-order catalogue made every dashboard write rewrite a 1.7 MB
// models.json with the same models shuffled, and the sync never reported
// "unchanged". Two calls must produce the same ids in the same positions.
func TestBuildModelsListResult_IsDeterministic(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	first := h.BuildModelsListResult(context.Background(), ModeListAll)
	if len(first.Models) == 0 {
		t.Fatal("expected a populated catalogue")
	}
	for run := 0; run < 3; run++ {
		next := h.BuildModelsListResult(context.Background(), ModeListAll)
		if len(next.Models) != len(first.Models) {
			t.Fatalf("run %d: catalogue size %d, want %d", run, len(next.Models), len(first.Models))
		}
		for i := range first.Models {
			if next.Models[i].ID != first.Models[i].ID {
				t.Fatalf("run %d: position %d is %q, want %q — the catalogue order must be stable",
					run, i, next.Models[i].ID, first.Models[i].ID)
			}
		}
	}
}
