package clientconfig

import (
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

