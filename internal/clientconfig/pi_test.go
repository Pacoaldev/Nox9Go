package clientconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func applyPiToFixture(t *testing.T, home, model string) (TargetFile, Result) {
	t.Helper()
	a, err := Resolve("pi")
	if err != nil {
		t.Fatalf("Resolve(pi): %v", err)
	}
	opts := Options{Home: home, BaseURL: "http://127.0.0.1:20130", APIKey: "sk-test", Model: model}
	res, err := a.Apply(opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	paths, err := a.Paths(opts)
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	return paths.Files[0], res
}

func TestPiApply_WritesProviderUnderProductName(t *testing.T) {
	home := t.TempDir()
	file, res := applyPiToFixture(t, home, "minimax-cn/MiniMax-M3")

	if !res.Changed {
		t.Fatal("first apply must report Changed")
	}
	wantPath := filepath.Join(home, ".pi", "agent", "models.json")
	if file.Path != wantPath {
		t.Fatalf("patched %s, want %s", file.Path, wantPath)
	}
	got := readFile(t, file.Path)

	if !strings.Contains(got, "\""+ProviderKey+"\"") {
		t.Fatalf("provider %q missing from output:\n%s", ProviderKey, got)
	}
	if strings.Contains(got, "9router") {
		t.Fatalf("forked provider name leaked into output:\n%s", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:20130/v1") {
		t.Fatalf("baseUrl not normalised to /v1:\n%s", got)
	}
	if !strings.Contains(got, `"openai-completions"`) {
		t.Fatalf("api field missing from output:\n%s", got)
	}
	if !strings.Contains(got, `"minimax-cn/MiniMax-M3"`) {
		t.Fatalf("model missing from output:\n%s", got)
	}
}

func TestPiApply_IsIdempotent(t *testing.T) {
	home := t.TempDir()
	applyPiToFixture(t, home, "m1")
	_, res := applyPiToFixture(t, home, "m1")
	if res.Changed {
		t.Fatal("a second identical apply must not rewrite the file")
	}
}

func TestPiApply_PreservesExistingModelsAndOverrides(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelsFile := filepath.Join(dir, "models.json")
	seed := "{\n" +
		"  // user catalogue\n" +
		"  \"$schema\": \"https://pi.dev/schemas/models.schema.json\",\n" +
		"  \"providers\": {\n" +
		"    \"" + ProviderKey + "\": {\n" +
		"      \"baseUrl\": \"http://localhost:20130/v1\",\n" +
		"      \"api\": \"openai-completions\",\n" +
		"      \"apiKey\": \"sk-old\",\n" +
		"      \"modelOverrides\": {\n" +
		"        \"MinionsPRO\": { \"input\": [\"text\", \"image\"] }\n" +
		"      },\n" +
		"      \"models\": [\n" +
		"        { \"id\": \"MinionsPRO\", \"name\": \"MinionsPRO\", \"contextWindow\": 200000, \"maxTokens\": 32000 },\n" +
		"        { \"id\": \"gh/gpt-4o\", \"name\": \"gh/gpt-4o\", \"contextWindow\": 128000, \"maxTokens\": 16384 }\n" +
		"      ]\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if err := os.WriteFile(modelsFile, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	applyPiToFixture(t, home, "MinionsPRO")

	got := readFile(t, modelsFile)
	if !strings.Contains(got, "sk-test") {
		t.Fatalf("apiKey not updated:\n%s", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:20130/v1") {
		t.Fatalf("baseUrl not updated:\n%s", got)
	}
	if !strings.Contains(got, `"modelOverrides"`) {
		t.Fatalf("modelOverrides was destroyed:\n%s", got)
	}
	if !strings.Contains(got, `"gh/gpt-4o"`) {
		t.Fatalf("existing models catalogue was destroyed:\n%s", got)
	}
	if !strings.Contains(got, "// user catalogue") {
		t.Fatalf("comment was destroyed:\n%s", got)
	}
}

func TestPiApply_AppendsMissingModelToPopulatedCatalogue(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelsFile := filepath.Join(dir, "models.json")
	seed := "{\n" +
		"  \"$schema\": \"https://pi.dev/schemas/models.schema.json\",\n" +
		"  \"providers\": {\n" +
		"    \"" + ProviderKey + "\": {\n" +
		"      \"baseUrl\": \"http://localhost:20130/v1\",\n" +
		"      \"api\": \"openai-completions\",\n" +
		"      \"models\": [\n" +
		"        { \"id\": \"MinionsPRO\", \"name\": \"MinionsPRO\", \"contextWindow\": 200000, \"maxTokens\": 32000 },\n" +
		"        { \"id\": \"gh/gpt-4o\", \"name\": \"gh/gpt-4o\", \"contextWindow\": 128000, \"maxTokens\": 16384 }\n" +
		"      ]\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if err := os.WriteFile(modelsFile, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	_, res := applyPiToFixture(t, home, "MinionsELITE")
	if !res.Changed {
		t.Fatal("adding missing model must report Changed")
	}

	got := readFile(t, modelsFile)
	if !strings.Contains(got, `"MinionsELITE"`) {
		t.Fatalf("MinionsELITE not added to catalogue:\n%s", got)
	}
	if !strings.Contains(got, `"MinionsPRO"`) {
		t.Fatalf("MinionsPRO was destroyed:\n%s", got)
	}
	if !strings.Contains(got, `"gh/gpt-4o"`) {
		t.Fatalf("gh/gpt-4o was destroyed:\n%s", got)
	}

	// Idempotent: second apply must not change anything
	_, res2 := applyPiToFixture(t, home, "MinionsELITE")
	if res2.Changed {
		t.Fatal("second identical apply must report Changed: false")
	}
}

func TestPiApply_AppendsModelToEveryArrayShape(t *testing.T) {
	cases := []struct {
		name  string
		array string
		want  []string
	}{
		{name: "empty array", array: "[]", want: []string{"MinionsELITE"}},
		{name: "single line", array: `[{"id": "m1", "name": "m1"}]`, want: []string{"m1", "MinionsELITE"}},
		{name: "trailing comma", array: "[\n        { \"id\": \"m1\", \"name\": \"m1\" },\n      ]", want: []string{"m1", "MinionsELITE"}},
		{name: "multi line", array: "[\n        { \"id\": \"m1\", \"name\": \"m1\" }\n      ]", want: []string{"m1", "MinionsELITE"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".pi", "agent")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			modelsFile := filepath.Join(dir, "models.json")
			seed := "{\n  \"providers\": {\n    \"" + ProviderKey + "\": {\n" +
				"      \"models\": " + tc.array + "\n    }\n  }\n}\n"
			if err := os.WriteFile(modelsFile, []byte(seed), 0o644); err != nil {
				t.Fatal(err)
			}

			applyPiToFixture(t, home, "MinionsELITE")

			got := readFile(t, modelsFile)
			if !strings.Contains(got, "MinionsELITE") {
				t.Fatalf("MinionsELITE not added:\n%s", got)
			}
			var parsed struct {
				Providers map[string]struct {
					Models []struct {
						ID string `json:"id"`
					} `json:"models"`
				} `json:"providers"`
			}
			if err := json.Unmarshal([]byte(got), &parsed); err != nil {
				t.Fatalf("patched file is not valid JSON: %v\n%s", err, got)
			}
			ids := parsed.Providers[ProviderKey].Models
			if len(ids) != len(tc.want) {
				t.Fatalf("catalogue = %+v, want %v", ids, tc.want)
			}
			for i, want := range tc.want {
				if ids[i].ID != want {
					t.Fatalf("catalogue[%d] = %q, want %q", i, ids[i].ID, want)
				}
			}
		})
	}
}

func TestPiReset_RemovesOnlyOwnedProvider(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelsFile := filepath.Join(dir, "models.json")
	seed := "{\n" +
		"  \"providers\": {\n" +
		"    \"" + ProviderKey + "\": { \"baseUrl\": \"http://localhost:20130/v1\", \"api\": \"openai-completions\" },\n" +
		"    \"other\": { \"baseUrl\": \"https://api.example.com/v1\" }\n" +
		"  }\n" +
		"}\n"
	if err := os.WriteFile(modelsFile, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := Resolve("pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reset(Options{Home: home}); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	got := readFile(t, modelsFile)
	if strings.Contains(got, ProviderKey) {
		t.Fatalf("reset left the provider behind:\n%s", got)
	}
	if !strings.Contains(got, `"other"`) {
		t.Fatalf("reset deleted a foreign provider:\n%s", got)
	}
}

func TestSyncPiCatalog_UpdatesPiAndGentleShellFiles(t *testing.T) {
	home := t.TempDir()
	piDir := filepath.Join(home, ".pi", "agent")
	gsDir := filepath.Join(home, ".gentle-shell", "agent")
	for _, d := range []string{piDir, gsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	piFile := filepath.Join(piDir, "models.json")
	gsFile := filepath.Join(gsDir, "models.json")
	seed := "{\n" +
		"  // keep comment\n" +
		"  \"providers\": {\n" +
		"    \"nox9go\": {\n" +
		"      \"name\": \"NOX9GO\",\n" +
		"      \"baseUrl\": \"https://rv5xehj.abc-tunnel.us/v1\",\n" +
		"      \"api\": \"openai-completions\",\n" +
		"      \"modelOverrides\": { \"MinionsPRO\": { \"input\": [\"text\", \"image\"] } },\n" +
		"      \"models\": [\n" +
		"        { \"id\": \"stale-model\" }\n" +
		"      ]\n" +
		"    },\n" +
		"    \"other\": { \"baseUrl\": \"https://api.example.com/v1\" }\n" +
		"  }\n" +
		"}\n"
	for _, f := range []string{piFile, gsFile} {
		if err := os.WriteFile(f, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	catalog := []PiCatalogModel{
		{
			ID:                  "MinionsPRO",
			Name:                "MinionsPRO",
			Object:              "model",
			OwnedBy:             "combo",
			Reasoning:           true,
			Input:               []string{"text", "image"},
			ContextWindow:       128000,
			MaxTokens:           4096,
			ContextLength:       128000,
			MaxCompletionTokens: 4096,
		},
		{
			ID:                  "ag/gemini-4-carbon-medium",
			Name:                "ag/gemini-4-carbon-medium",
			Object:              "model",
			OwnedBy:             "ag",
			Reasoning:           true,
			Input:               []string{"text", "image"},
			ContextWindow:       1048576,
			MaxTokens:           65536,
			ContextLength:       1048576,
			MaxCompletionTokens: 65536,
		},
	}

	changed, err := SyncPiCatalog(home, catalog)
	if err != nil {
		t.Fatalf("SyncPiCatalog: %v", err)
	}
	if !changed {
		t.Fatal("SyncPiCatalog must report changed on first sync")
	}

	for _, f := range []string{piFile, gsFile} {
		got := readFile(t, f)
		if strings.Contains(got, "stale-model") {
			t.Fatalf("%s: stale model survived sync:\n%s", f, got)
		}
		if !strings.Contains(got, `"ag/gemini-4-carbon-medium"`) {
			t.Fatalf("%s: new model missing:\n%s", f, got)
		}
		if !strings.Contains(got, `"modelOverrides"`) {
			t.Fatalf("%s: modelOverrides destroyed:\n%s", f, got)
		}
		if !strings.Contains(got, "// keep comment") {
			t.Fatalf("%s: comment destroyed:\n%s", f, got)
		}
		if !strings.Contains(got, `"other"`) {
			t.Fatalf("%s: other provider destroyed:\n%s", f, got)
		}
	}

	changed2, err := SyncPiCatalog(home, catalog)
	if err != nil {
		t.Fatalf("second SyncPiCatalog: %v", err)
	}
	if changed2 {
		t.Fatal("second identical SyncPiCatalog must be a no-op")
	}
}

