package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

// TestApplyPromotionOnCopy exercises the real write path against the working
// tree and restores every file afterwards, so the promotion is proven to
// produce parsing source rather than merely matching a string.
func TestApplyPromotionOnCopy(t *testing.T) {
	if os.Getenv("PROMOTE_E2E") == "" {
		t.Skip("set PROMOTE_E2E=1 to run the destructive promotion check")
	}
	root := findRepoRoot(t)
	p := samplePromotion()
	p.ID = "zzpromotefixture"
	p.Alias = "zzpromotefixture"
	p.Models = []string{"fixture-a"}

	files := []string{
		"internal/providers/providers.go",
		"internal/providers/registry_models.go",
		"web/src/lib/providers.ts",
		"web/src/components/connections/types.ts",
		"web/src/components/connections/ProviderDetailView.svelte",
	}
	backup := map[string][]byte{}
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("backup %s: %v", rel, err)
		}
		backup[rel] = b
	}
	t.Cleanup(func() {
		for rel, b := range backup {
			_ = os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), b, 0o644)
		}
	})

	if _, err := ApplyCatalogPromotion(root, p, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, rel := range files {
		after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(after) == string(backup[rel]) {
			t.Errorf("%s unchanged", rel)
		}
	}
}
