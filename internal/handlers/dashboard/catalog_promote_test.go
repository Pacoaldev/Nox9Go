package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// samplePromotion is a plausible promotion for an id that does not exist in the
// tree, so the patchers must add every entry rather than short-circuit.
func samplePromotion() CatalogPromotion {
	return CatalogPromotion{
		ID:           "zz-promote-fixture",
		NodeID:       "openai-compatible-chat-fixture",
		Name:         "Promote Fixture",
		Alias:        "zzfixture",
		Color:        "#123456",
		Icon:         "hub",
		Category:     "apikey",
		Website:      "https://example.com",
		NoticeText:   "fixture notice",
		APIKeyURL:    "https://example.com/keys",
		ServiceKinds: []string{"llm"},
		ChatURL:      "https://api.example.com/v1/chat/completions",
		ModelsURL:    "https://api.example.com/v1/models",
		Models:       []string{"fixture-model-a", "fixture-model-b"},
	}
}

// findRepoRoot walks up from the test's working directory. The patchers address
// real files, so the test needs the real tree rather than a fixture copy: a
// fixture would go on matching after the anchor it relies on moved.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "internal", "providers", "providers.go")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("source tree not reachable from the test working directory")
	return ""
}

func TestPromotionPatchersAddEveryDestination(t *testing.T) {
	root := findRepoRoot(t)
	p := samplePromotion()

	cases := []struct {
		file string
		fn   func(string, CatalogPromotion) (string, error)
		want string
	}{
		{"internal/providers/providers.go", patchKnownProviders, "\"zz-promote-fixture\": {"},
		{"internal/providers/providers.go", patchModelsListURL, "\"zz-promote-fixture\": \"https://api.example.com/v1/models\""},
		{"internal/providers/registry_models.go", patchProviderModels, "\"zz-promote-fixture\": {\"fixture-model-a\", \"fixture-model-b\"}"},
		{"web/src/lib/providers.ts", patchProviderCatalog, "\"id\": \"zz-promote-fixture\""},
		{"web/src/components/connections/types.ts", patchIconFiles, "'zz-promote-fixture': 'zz-promote-fixture.png'"},
		{"web/src/components/connections/ProviderDetailView.svelte", patchModelsListURLs, "'zz-promote-fixture',"},
	}

	for _, tc := range cases {
		before, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.file)))
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		after, err := tc.fn(string(before), p)
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)
			continue
		}
		if after == string(before) {
			t.Errorf("%s: patcher reported no change", tc.file)
			continue
		}
		if !strings.Contains(after, tc.want) {
			t.Errorf("%s: patched output does not contain %q", tc.file, tc.want)
		}
	}
}

// TestPromotionPatchersAreIdempotent is the property that makes a retry after a
// half-applied promotion safe: running a patcher on its own output must be a
// no-op, not a duplicate entry.
func TestPromotionPatchersAreIdempotent(t *testing.T) {
	root := findRepoRoot(t)
	p := samplePromotion()

	for _, st := range promotionSteps(p) {
		before, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(st.rel)))
		if err != nil {
			t.Fatalf("read %s: %v", st.rel, err)
		}
		once, err := st.fn(string(before))
		if err != nil {
			t.Fatalf("%s first pass: %v", st.rel, err)
		}
		twice, err := st.fn(once)
		if err != nil {
			t.Fatalf("%s second pass: %v", st.rel, err)
		}
		if once != twice {
			t.Errorf("%s: second pass changed the file again", st.rel)
		}
	}
}

func TestValidatePromotionRejectsBadInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CatalogPromotion)
	}{
		{"empty id", func(p *CatalogPromotion) { p.ID = "" }},
		{"uppercase id", func(p *CatalogPromotion) { p.ID = "Moonshot" }},
		{"id with slash", func(p *CatalogPromotion) { p.ID = "a/b" }},
		{"empty node", func(p *CatalogPromotion) { p.NodeID = "" }},
		{"http chat", func(p *CatalogPromotion) { p.ChatURL = "http://x/v1/chat/completions" }},
		{"http models", func(p *CatalogPromotion) { p.ModelsURL = "http://x/v1/models" }},
		{"unknown category", func(p *CatalogPromotion) { p.Category = "nope" }},
	}
	for _, tc := range cases {
		p := samplePromotion()
		tc.mutate(&p)
		if err := validatePromotion(&p); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
	valid := samplePromotion()
	if err := validatePromotion(&valid); err != nil {
		t.Errorf("valid payload rejected: %v", err)
	}
}
