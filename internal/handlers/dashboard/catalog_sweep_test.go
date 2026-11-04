package dashboard

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"nox9go/proxy/internal/db"
)

// sweepCatalogueURLs maps the live catalogue endpoint of every provider the
// sweep tests use to the body the fake upstream answers with.
var sweepCatalogueURLs = map[string]string{
	"https://integrate.api.nvidia.com/v1/models": `{"data":[{"id":"meta/llama-3"}]}`,
	"https://llm.chutes.ai/v1/models":            `{"data":[{"id":"some/model"}]}`,
}

// stubSweepCatalogue points the connection probe at a fake upstream that
// answers the catalogue endpoints the sweep reads.
func stubSweepCatalogue(t *testing.T) {
	t.Helper()
	original := validateProbeDo
	t.Cleanup(func() { validateProbeDo = original })
	validateProbeDo = func(_ context.Context, _ string, rawURL string, _ map[string]string, _ []byte) (int, []byte, error) {
		body, ok := sweepCatalogueURLs[rawURL]
		if !ok {
			return http.StatusNotFound, []byte(`{"error":"no catalogue"}`), nil
		}
		return http.StatusOK, []byte(body), nil
	}
}

// seedSweepConnection stores an active connection with a usable key.
func seedSweepConnection(t *testing.T, repo *db.Repo, id, provider string) {
	t.Helper()
	if err := repo.CreateProviderConnectionFull(id, provider, "apikey", provider+" test", nil, `{"apiKey":"test-key"}`); err != nil {
		t.Fatalf("seed connection %s: %v", provider, err)
	}
}

// seedSweepCustomModel stores a custom model the operator configured.
func seedSweepCustomModel(t *testing.T, repo *db.Repo, provider, id string) {
	t.Helper()
	key := provider + "|" + id + "|llm"
	value := `{"providerAlias":"` + provider + `","id":"` + id + `","type":"llm","name":"` + id + `"}`
	if err := repo.SetKV("customModels", key, value); err != nil {
		t.Fatalf("seed custom model %s/%s: %v", provider, id, err)
	}
}

// seedSweepCombo stores a combo whose members are "provider/model" entries.
func seedSweepCombo(t *testing.T, repo *db.Repo, id, name string, members []string) {
	t.Helper()
	encoded, err := json.Marshal(members)
	if err != nil {
		t.Fatalf("encode combo members: %v", err)
	}
	if _, err := repo.RawDB().Exec(
		`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (?, ?, 'fallback', ?, ?, ?)`,
		id, name, string(encoded), "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("seed combo %s: %v", name, err)
	}
}

// findSweepEntry returns the entry of one provider.
func findSweepEntry(t *testing.T, entries []CatalogSweepEntry, provider string) CatalogSweepEntry {
	t.Helper()
	for _, e := range entries {
		if e.Provider == provider {
			return e
		}
	}
	t.Fatalf("no sweep entry for provider %q in %+v", provider, entries)
	return CatalogSweepEntry{}
}

func TestParseCatalogSweepConfig(t *testing.T) {
	tests := []struct {
		name         string
		value        any
		wantEnabled  bool
		wantInterval int
	}{
		{
			name:         "missing value yields the default",
			value:        nil,
			wantEnabled:  true,
			wantInterval: 720,
		},
		{
			name:         "unreadable value yields the default",
			value:        "not-an-object",
			wantEnabled:  true,
			wantInterval: 720,
		},
		{
			name:         "explicit disabled is respected",
			value:        map[string]any{"enabled": false},
			wantEnabled:  false,
			wantInterval: 720,
		},
		{
			name:         "explicit enabled with an interval is respected",
			value:        map[string]any{"enabled": true, "intervalMin": float64(60)},
			wantEnabled:  true,
			wantInterval: 60,
		},
		{
			name:         "interval below the minimum is clamped",
			value:        map[string]any{"intervalMin": float64(1)},
			wantEnabled:  true,
			wantInterval: 15,
		},
		{
			name:         "interval above the maximum is clamped",
			value:        map[string]any{"intervalMin": float64(99999)},
			wantEnabled:  true,
			wantInterval: 10080,
		},
		{
			name:         "unreadable interval keeps the default",
			value:        map[string]any{"intervalMin": "soon"},
			wantEnabled:  true,
			wantInterval: 720,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCatalogSweepConfig(tt.value)
			if got.Enabled != tt.wantEnabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tt.wantEnabled)
			}
			if got.IntervalMin != tt.wantInterval {
				t.Errorf("IntervalMin = %d, want %d", got.IntervalMin, tt.wantInterval)
			}
		})
	}
}

func TestRunCatalogSweepRecordsUnlistedSkippedAndPersists(t *testing.T) {
	stubSweepCatalogue(t)

	repo, cleanup := setupTestDB(t)
	defer cleanup()

	seedSweepConnection(t, repo, "conn-nvidia", "nvidia")
	seedSweepConnection(t, repo, "conn-chutes", "chutes")
	// Claude publishes no queryable catalogue, so the sweep skips it.
	seedSweepConnection(t, repo, "conn-claude", "claude")
	seedSweepCustomModel(t, repo, "nvidia", "old-model")
	seedSweepCustomModel(t, repo, "nvidia", "meta/llama-3")
	seedSweepCombo(t, repo, "combo-1", "mix", []string{"nvidia/combo-model", "other/thing"})

	h := NewDashboardHandler(repo)
	entries := h.RunCatalogSweep(context.Background(), "")

	if len(entries) != 3 {
		t.Fatalf("expected 3 provider entries, got %d: %+v", len(entries), entries)
	}

	nvidia := findSweepEntry(t, entries, "nvidia")
	if nvidia.Error != "" {
		t.Fatalf("nvidia entry reported an error: %q", nvidia.Error)
	}
	if nvidia.Skipped {
		t.Errorf("nvidia must not be skipped")
	}
	if nvidia.Listed != 1 {
		t.Errorf("nvidia Listed = %d, want 1", nvidia.Listed)
	}
	if nvidia.CheckedAt == "" {
		t.Errorf("nvidia CheckedAt is empty")
	}
	wantUnlisted := []string{"combo-model", "old-model"}
	if len(nvidia.Unlisted) != len(wantUnlisted) {
		t.Fatalf("nvidia Unlisted = %v, want %v", nvidia.Unlisted, wantUnlisted)
	}
	for i, want := range wantUnlisted {
		if nvidia.Unlisted[i] != want {
			t.Errorf("nvidia Unlisted[%d] = %q, want %q", i, nvidia.Unlisted[i], want)
		}
	}

	chutes := findSweepEntry(t, entries, "chutes")
	if chutes.Error != "" || chutes.Skipped {
		t.Errorf("chutes entry = %+v, want a successful sweep", chutes)
	}
	if len(chutes.Unlisted) != 0 {
		t.Errorf("chutes Unlisted = %v, want empty (no configured models)", chutes.Unlisted)
	}

	claude := findSweepEntry(t, entries, "claude")
	if !claude.Skipped {
		t.Errorf("claude entry = %+v, want Skipped=true", claude)
	}
	if claude.Error != "" {
		t.Errorf("claude Error = %q, want empty", claude.Error)
	}

	// Absence from a catalogue is informational: it must never file a
	// deprecation.
	deps, err := repo.ListModelDeprecations("nvidia")
	if err != nil {
		t.Fatalf("list deprecations: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("expected no deprecations for nvidia, got %+v", deps)
	}

	persisted := h.LoadCatalogSweep()
	if len(persisted) != 3 {
		t.Fatalf("expected 3 persisted entries, got %d: %+v", len(persisted), persisted)
	}
	stored, ok := persisted["nvidia"]
	if !ok {
		t.Fatalf("no persisted entry for nvidia: %+v", persisted)
	}
	if stored.Listed != nvidia.Listed || stored.CheckedAt != nvidia.CheckedAt {
		t.Errorf("persisted nvidia entry = %+v, want %+v", stored, nvidia)
	}
	if len(stored.Unlisted) != len(wantUnlisted) {
		t.Errorf("persisted nvidia Unlisted = %v, want %v", stored.Unlisted, wantUnlisted)
	}
	skipped, ok := persisted["claude"]
	if !ok || !skipped.Skipped {
		t.Errorf("persisted claude entry = %+v, want Skipped=true", skipped)
	}
}

func TestCatalogSweepHandlers(t *testing.T) {
	stubSweepCatalogue(t)

	repo, cleanup := setupTestDB(t)
	defer cleanup()

	seedSweepConnection(t, repo, "conn-nvidia", "nvidia")
	seedSweepCustomModel(t, repo, "nvidia", "old-model")

	h := NewDashboardHandler(repo)
	r := chi.NewRouter()
	r.Get("/api/models/sweep", h.HandleGetCatalogSweep)
	r.Post("/api/models/sweep", h.HandleRunCatalogSweep)

	t.Run("get before any sweep", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/sweep", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Config struct {
				Enabled     bool `json:"enabled"`
				IntervalMin int  `json:"intervalMin"`
			} `json:"config"`
			Providers map[string]CatalogSweepEntry `json:"providers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if !out.Config.Enabled || out.Config.IntervalMin != 720 {
			t.Errorf("config = %+v, want enabled with a 720 minute interval", out.Config)
		}
		if len(out.Providers) != 0 {
			t.Errorf("providers = %+v, want none before the first sweep", out.Providers)
		}
	})

	t.Run("post runs the sweep", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/sweep", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Providers map[string]CatalogSweepEntry `json:"providers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		entry, ok := out.Providers["nvidia"]
		if !ok {
			t.Fatalf("providers = %+v, want a nvidia entry", out.Providers)
		}
		if entry.Listed != 1 || len(entry.Unlisted) != 1 || entry.Unlisted[0] != "old-model" {
			t.Errorf("nvidia entry = %+v, want 1 listed model and old-model unlisted", entry)
		}
	})
}

// TestSyncProviderModelsKeepsItsResponse pins the contract the dashboard
// already depends on: the same fields, the same 404 without connections and
// the same 502 when every provider it touched failed.
func TestSyncProviderModelsKeepsItsResponse(t *testing.T) {
	original := validateProbeDo
	t.Cleanup(func() { validateProbeDo = original })

	repo, cleanup := setupTestDB(t)
	defer cleanup()

	h := NewDashboardHandler(repo)
	r := chi.NewRouter()
	r.Post("/api/models/sync", h.HandleSyncProviderModels)

	t.Run("no connections answers 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/sync", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("every provider failed answers 502", func(t *testing.T) {
		validateProbeDo = func(_ context.Context, _ string, _ string, _ map[string]string, _ []byte) (int, []byte, error) {
			return http.StatusInternalServerError, []byte(`{"error":"boom"}`), nil
		}
		seedSweepConnection(t, repo, "conn-moonshot", "moonshot")

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/sync", nil))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a healthy provider answers 200 with the documented fields", func(t *testing.T) {
		validateProbeDo = func(_ context.Context, _ string, rawURL string, _ map[string]string, _ []byte) (int, []byte, error) {
			if rawURL != "https://integrate.api.nvidia.com/v1/models" {
				return http.StatusInternalServerError, []byte(`{"error":"boom"}`), nil
			}
			return http.StatusOK, []byte(`{"data":[{"id":"meta/llama-3"}]}`), nil
		}
		seedSweepConnection(t, repo, "conn-nvidia", "nvidia")

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/sync", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		for _, field := range []string{"success", "provider", "synced", "failed", "skipped", "revived", "lastError", "catalogState"} {
			if _, ok := out[field]; !ok {
				t.Errorf("response is missing the %q field: %+v", field, out)
			}
		}
		if out["success"] != true {
			t.Errorf("success = %v, want true", out["success"])
		}
		if out["synced"] != float64(1) {
			t.Errorf("synced = %v, want 1", out["synced"])
		}
	})
}
