package clientconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// applyToFixture runs the opencode adapter against a temp home and returns the
// file it wrote.
func applyToFixture(t *testing.T, home, model string) (TargetFile, Result) {
	t.Helper()
	a, err := Resolve("opencode")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
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

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestOpenCodeApply_WritesProviderUnderProductName(t *testing.T) {
	home := t.TempDir()
	file, res := applyToFixture(t, home, "minimax-cn/MiniMax-M3")

	if !res.Changed {
		t.Fatal("first apply must report Changed")
	}
	got := readFile(t, file.Path)

	if !strings.Contains(got, "\""+ProviderKey+"\"") {
		t.Fatalf("provider %q missing from output:\n%s", ProviderKey, got)
	}
	if strings.Contains(got, "9router") {
		t.Fatalf("forked provider name leaked into output:\n%s", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:20130/v1") {
		t.Fatalf("baseURL not normalised to /v1:\n%s", got)
	}
	if !strings.Contains(got, "\""+ProviderKey+"/minimax-cn/MiniMax-M3\"") {
		t.Fatalf("model not prefixed with the provider name:\n%s", got)
	}
}

func TestOpenCodeApply_IsIdempotent(t *testing.T) {
	home := t.TempDir()
	applyToFixture(t, home, "m1")
	_, res := applyToFixture(t, home, "m1")
	if res.Changed {
		t.Fatal("a second identical apply must not rewrite the file")
	}
}

// An inherited XDG_CONFIG_HOME must not redirect a caller that pinned Home.
//
// This is the CI failure verbatim: the Linux runner exports XDG_CONFIG_HOME,
// xdgConfigHome returned it before looking at Options.Home, so the fixture in
// the temp home was ignored and the patch was written to the runner's own
// ~/.config/opencode/opencode.json — a config the test never created. Both the
// path assertion and the parse failure above it came from that one lookup.
func TestOpenCodePaths_PinnedHomeBeatsXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "elsewhere"))

	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(jsonc, []byte("{}"+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	file, _ := applyToFixture(t, home, "MinionsPRO")
	if file.Path != jsonc {
		t.Fatalf("patched %s, want %s — the pinned home must win over XDG_CONFIG_HOME", file.Path, jsonc)
	}
}

// The user's own file uses .jsonc and names the provider after this product.
// Both must round-trip: the .jsonc is the one patched, and the existing
// provider entry is updated rather than a second one invented.
func TestOpenCodeApply_ExistingJSONCAndProvider(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	seed := "{\n" +
		"  // the user's own file\n" +
		"  \"model\": \"" + ProviderKey + "/MinionsPRO\",\n" +
		"  \"provider\": {\n" +
		"    \"" + ProviderKey + "\": {\n" +
		"      \"name\": \"" + ProviderKey + "\",\n" +
		"      \"npm\": \"@ai-sdk/openai-compatible\",\n" +
		"      \"options\": { \"apiKey\": \"sk-old\", \"baseURL\": \"http://localhost:20130/v1\" }\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if err := os.WriteFile(jsonc, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	file, _ := applyToFixture(t, home, "MinionsPRO")

	if file.Path != jsonc {
		t.Fatalf("patched %s, want the existing .jsonc %s", file.Path, jsonc)
	}
	got := readFile(t, jsonc)
	if strings.Count(got, "\""+ProviderKey+"\": {") != 1 {
		t.Fatalf("expected exactly one provider entry, got:\n%s", got)
	}
	if !strings.Contains(got, "sk-test") {
		t.Fatalf("apiKey not updated:\n%s", got)
	}
	if !strings.Contains(got, "// the user's own file") {
		t.Fatalf("the user's comment was destroyed:\n%s", got)
	}
}

func TestOpenCodeReset_RemovesOnlyOwnedKeys(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	seed := "{\n" +
		"  \"model\": \"other/keep-me\",\n" +
		"  \"provider\": {\n" +
		"    \"" + ProviderKey + "\": { \"npm\": \"@ai-sdk/openai-compatible\" },\n" +
		"    \"other\": { \"npm\": \"x\" }\n" +
		"  }\n" +
		"}\n"
	if err := os.WriteFile(jsonc, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := Resolve("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reset(Options{Home: home}); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	got := readFile(t, jsonc)
	if strings.Contains(got, ProviderKey) {
		t.Fatalf("reset left the provider behind:\n%s", got)
	}
	if !strings.Contains(got, "other/keep-me") {
		t.Fatalf("reset deleted a model that was not ours:\n%s", got)
	}
	if !strings.Contains(got, "\"other\"") {
		t.Fatalf("reset deleted a foreign provider:\n%s", got)
	}
}
