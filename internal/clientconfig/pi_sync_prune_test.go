package clientconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The harness resolves every pattern in settings.json enabledModels against the
// live model catalogue and prints `No models match pattern "<pattern>"` for
// each one that misses. The gateway owns the catalogue it publishes, so when a
// provider drops a model (Fluence retired its `cheapest` routing profile, GitHub
// Copilot stopped serving gpt-5.4) the operator's previously selectable pattern
// becomes an orphan that warns on every start, forever: SyncPiCatalog rewrote
// models.json but never reconciled enabledModels.
//
// storeSettings seeds one settings.json next to the given models.json.
func storeSettings(t *testing.T, modelsFile, content string) string {
	t.Helper()
	settings := filepath.Join(filepath.Dir(modelsFile), "settings.json")
	if err := os.WriteFile(settings, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return settings
}

func readEnabledModels(t *testing.T, path string) []string {
	t.Helper()
	raw := readFile(t, path)
	var doc struct {
		EnabledModels []string `json:"enabledModels"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, raw)
	}
	return doc.EnabledModels
}

func seedSyncHome(t *testing.T, settingsBody string) (home, piSettings, gsSettings string) {
	t.Helper()
	home = t.TempDir()
	piDir := filepath.Join(home, ".pi", "agent")
	gsDir := filepath.Join(home, ".gentle-shell", "agent")
	for _, d := range []string{piDir, gsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	piModels := filepath.Join(piDir, "models.json")
	gsModels := filepath.Join(gsDir, "models.json")
	models := "{\n  \"providers\": {\n    \"nox9go\": {\n" +
		"      \"baseUrl\": \"http://127.0.0.1:20130/v1\",\n" +
		"      \"models\": [ { \"id\": \"stale\" } ]\n    }\n  }\n}\n"
	for _, f := range []string{piModels, gsModels} {
		if err := os.WriteFile(f, []byte(models), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	piSettings = storeSettings(t, piModels, settingsBody)
	gsSettings = storeSettings(t, gsModels, settingsBody)
	return home, piSettings, gsSettings
}

// TestSyncPiCatalog_PrunesOrphanedEnabledModels is the fix for the recurring
// warning: a gateway-owned pattern with no catalogue match must go, and every
// pattern the catalogue still serves must stay exactly where it was.
func TestSyncPiCatalog_PrunesOrphanedEnabledModels(t *testing.T) {
	home, piSettings, gsSettings := seedSyncHome(t, `{
  "defaultProvider": "nox9go",
  "enabledModels": [
    "nox9go/fluence/cheapest",
    "nox9go/MinionsPRO",
    "nox9go/ag/gemini-4-carbon-medium",
    "fluence/coder",
    "nan/glm5.3-flash",
    "nox9go/haiku"
  ]
}
`)

	catalog := []PiCatalogModel{
		{ID: "MinionsPRO", Name: "MinionsPRO"},
		{ID: "ag/gemini-4-carbon-medium", Name: "ag/gemini-4-carbon-medium"},
		{ID: "nan/glm5.3-flash", Name: "nan/glm5.3-flash"},
		{ID: "gh/claude-haiku-4.5", Name: "gh/claude-haiku-4.5"},
	}
	if _, err := SyncPiCatalog(home, catalog); err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}

	for _, path := range []string{piSettings, gsSettings} {
		got := readEnabledModels(t, path)
		want := []string{
			"nox9go/MinionsPRO",
			"nox9go/ag/gemini-4-carbon-medium",
			// Another provider's pattern is not this gateway's business, even
			// though the catalogue does not list it.
			"fluence/coder",
			// A bare model id is how Pi addresses a provider-qualified catalogue
			// entry, so it matches and must survive.
			"nan/glm5.3-flash",
			// Pi's fuzzy fallback is substring containment: "haiku" resolves to
			// gh/claude-haiku-4.5, so pruning it would delete a working entry.
			"nox9go/haiku",
		}
		if len(got) != len(want) {
			t.Fatalf("%s: enabledModels = %v, want %v", path, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: enabledModels = %v, want %v (order preserved)", path, got, want)
			}
		}
		raw := readFile(t, path)
		if want2 := `"defaultProvider": "nox9go"`; !strings.Contains(raw, want2) {
			t.Fatalf("%s: sync destroyed an unrelated setting:\n%s", path, raw)
		}
	}
}

// TestSyncPiCatalog_PruneIsIdempotent pins that a second sync over a clean file
// reports no change: a prune that rewrites the array every time would make the
// background sync write settings.json on every dashboard load.
func TestSyncPiCatalog_PruneIsIdempotent(t *testing.T) {
	home, piSettings, gsSettings := seedSyncHome(t, `{
  "enabledModels": [ "nox9go/gone", "nox9go/kept" ]
}
`)
	catalog := []PiCatalogModel{{ID: "kept", Name: "kept"}, {ID: "new", Name: "new"}}

	if _, err := SyncPiCatalog(home, catalog); err != nil {
		t.Fatalf("first SyncPiCatalog: %v", err)
	}
	for _, path := range []string{piSettings, gsSettings} {
		if got := readEnabledModels(t, path); len(got) != 1 || got[0] != "nox9go/kept" {
			t.Fatalf("%s: after first sync enabledModels = %v, want [nox9go/kept]", path, got)
		}
	}
	changed, err := SyncPiCatalog(home, catalog)
	if err != nil {
		t.Fatalf("second SyncPiCatalog: %v", err)
	}
	if changed {
		t.Fatal("second sync over a pruned file must report no change")
	}
}

// TestSyncPiCatalog_PruneDropsKeyWhenNothingRemains: emptying the list would
// leave Pi with an allowlist of nothing. Removing the key restores the default
// (every catalogue model selectable), which is what the operator had before
// opting into a stale pattern.
func TestSyncPiCatalog_PruneDropsKeyWhenNothingRemains(t *testing.T) {
	home, piSettings, _ := seedSyncHome(t, `{
  "enabledModels": [ "nox9go/fluence/cheapest" ]
}
`)
	if _, err := SyncPiCatalog(home, []PiCatalogModel{{ID: "kept", Name: "kept"}}); err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}
	raw := readFile(t, piSettings)
	if strings.Contains(raw, "enabledModels") {
		t.Fatalf("an all-orphan allowlist must be removed, not emptied:\n%s", raw)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("pruned settings.json must stay valid JSON: %v\n%s", err, raw)
	}
	if _, ok := doc["enabledModels"]; ok {
		t.Fatalf("enabledModels survived the prune:\n%s", raw)
	}
}

// TestSyncPiCatalog_PruneSkipsGlobsThinkingAndFuzzyPatterns guards the limits of
// the prune: a glob (minimatch), a thinking-level suffix, and any pattern Pi can
// satisfy by substring are not orphaned and must be left alone.
func TestSyncPiCatalog_PruneSkipsGlobsThinkingAndFuzzyPatterns(t *testing.T) {
	home, piSettings, _ := seedSyncHome(t, `{
  "enabledModels": [ "nox9go/ag/*", "*sonnet*", "nox9go/fluence/*", "nox9go/haiku:high", "nox9go/haiku" ]
}
`)
	catalog := []PiCatalogModel{{ID: "gh/claude-haiku-4.5", Name: "gh/claude-haiku-4.5"}}
	if _, err := SyncPiCatalog(home, catalog); err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}
	got := readEnabledModels(t, piSettings)
	if len(got) != 5 {
		t.Fatalf("glob, thinking and fuzzy patterns must survive the prune, got %v", got)
	}
}

// TestSyncPiCatalog_PruneIgnoresForeignSettingsFiles: a directory without
// enabledModels, or with patterns that belong to another provider, is not
// rewritten at all.
func TestSyncPiCatalog_PruneIgnoresForeignSettingsFiles(t *testing.T) {
	home, piSettings, _ := seedSyncHome(t, `{
  "enabledModels": [ "fluence/coder", "openai/gpt-4o" ]
}
`)
	before := readFile(t, piSettings)
	if _, err := SyncPiCatalog(home, []PiCatalogModel{{ID: "kept", Name: "kept"}}); err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}
	if after := readFile(t, piSettings); after != before {
		t.Fatalf("settings.json with no gateway pattern must not be rewritten:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestSyncPiCatalog_PruneMatchesPiResolution is the table that pins the prune to
// Pi's own resolution rules, so the pruning decision cannot drift from the
// warning it exists to silence. Each row is one pattern from an operator's
// settings.json and whether Pi would resolve it.
func TestSyncPiCatalog_PruneMatchesPiResolution(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		served  bool
	}{
		{"retired routing profile", "nox9go/fluence/cheapest", false},
		{"provider-qualified exact id", "nox9go/MinionsPRO", true},
		{"bare catalogue id", "nan/glm5.3-flash", true},
		{"fuzzy substring in a catalogue id", "nox9go/haiku", true},
		{"fuzzy match with trailing whitespace", "nox9go/haiku ", true},
		{"fuzzy match with leading whitespace", "  nox9go/haiku", true},
		{"served base with thinking suffix", "nox9go/MinionsPRO:high", true},
		{"orphan base with thinking suffix", "nox9go/ghost:high", false},
		{"orphan base with invalid suffix", "nox9go/ghost:weird", false},
		{"glob over the provider namespace", "nox9go/ag/*", true},
		{"brace pattern is not a glob to Pi", "nox9go/{MinionsPRO", false},
		{"another provider is not ours", "fluence/coder", true},
	}
	catalog := []PiCatalogModel{
		{ID: "MinionsPRO", Name: "MinionsPRO"},
		{ID: "gh/claude-haiku-4.5", Name: "gh/claude-haiku-4.5"},
		{ID: "nan/glm5.3-flash", Name: "nan/glm5.3-flash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, piSettings, _ := seedSyncHome(t, `{
  "enabledModels": [ `+jsonString(tt.pattern)+` ]
}
`)
			if _, err := SyncPiCatalog(home, catalog); err != nil {
				t.Fatalf("SyncPiCatalog: %v", err)
			}
			got := readEnabledModels(t, piSettings)
			if tt.served && (len(got) != 1 || got[0] != tt.pattern) {
				t.Fatalf("pattern %q must survive: enabledModels = %#v", tt.pattern, got)
			}
			if !tt.served && len(got) != 0 {
				t.Fatalf("pattern %q must be pruned: enabledModels = %#v", tt.pattern, got)
			}
		})
	}
}
