package dashboard

import (
	"strings"
	"testing"
)

// TestCatalogConflictBlocksExistingID is the regression for the case that used
// to slip through: an id already registered made every patcher skip, so the
// promotion reported success, migrated connections and deleted the node.
func TestCatalogConflictBlocksExistingID(t *testing.T) {
	root := findRepoRoot(t)

	cases := []struct {
		id     string
		expect string
	}{
		// moonshot is registered in all three places by the time this runs.
		{"moonshot", "PROVIDER_CATALOG"},
		{"openai", "KnownProviders"},
		{"anthropic", "KnownProviders"},
	}
	for _, tc := range cases {
		conflict, err := catalogEntryExists(root, tc.id)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		if conflict == nil {
			t.Errorf("%s: expected a conflict, got none", tc.id)
			continue
		}
		joined := strings.Join(conflict.Source, " ")
		if !strings.Contains(joined, tc.expect) {
			t.Errorf("%s: sources %v do not mention %s", tc.id, conflict.Source, tc.expect)
		}
		if !strings.Contains(conflict.Detail, tc.id) {
			t.Errorf("%s: detail does not name the id: %q", tc.id, conflict.Detail)
		}
	}
}

func TestCatalogConflictAllowsFreshID(t *testing.T) {
	root := findRepoRoot(t)
	conflict, err := catalogEntryExists(root, "zz-not-in-catalog-yet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conflict != nil {
		t.Errorf("fresh id reported as a conflict: %v", conflict.Source)
	}
}

// TestCatalogConflictEmptyID is not a conflict: an empty id is rejected by
// validatePromotion, which runs first in the handler.
func TestCatalogConflictEmptyID(t *testing.T) {
	root := findRepoRoot(t)
	conflict, err := catalogEntryExists(root, "")
	if err != nil || conflict != nil {
		t.Errorf("empty id: conflict=%v err=%v", conflict, err)
	}
}
