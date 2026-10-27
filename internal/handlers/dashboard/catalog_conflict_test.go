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

// TestCatalogConflictReportsMediaKinds is the case the dialog could not
// explain before: an id already registered only under Media Providers. The
// refusal was correct but the advice ("pick another id") hid that the existing
// entry is a different kind of provider, not a duplicate.
func TestCatalogConflictReportsMediaKinds(t *testing.T) {
	root := findRepoRoot(t)

	cases := []struct {
		id      string
		wantNot string // must not be reported as a kind
		wantHas string // at least one of these must be reported
	}{
		// replicate is the media provider that motivated the check: images
		// and video, never chat.
		{"replicate", "llm", "image"},
	}
	for _, tc := range cases {
		conflict, err := catalogEntryExists(root, tc.id)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		if conflict == nil {
			t.Fatalf("%s: expected a conflict, got none", tc.id)
		}
		if len(conflict.Kinds) == 0 {
			t.Fatalf("%s: no kinds reported", tc.id)
		}
		for _, k := range conflict.Kinds {
			if k == tc.wantNot {
				t.Errorf("%s: kinds %v unexpectedly include %s", tc.id, conflict.Kinds, k)
			}
		}
		found := false
		for _, k := range conflict.Kinds {
			if k == tc.wantHas {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: kinds %v do not include %s", tc.id, conflict.Kinds, tc.wantHas)
		}
		if !strings.Contains(conflict.Detail, tc.wantHas) {
			t.Errorf("%s: detail %q does not name the existing kind %q", tc.id, conflict.Detail, tc.wantHas)
		}
	}
}

// TestCatalogConflictChatProviderHasNoMediaKinds keeps the plain duplicate
// path distinct: openai serves chat, so the media wording must not appear.
func TestCatalogConflictChatProviderHasNoMediaKinds(t *testing.T) {
	root := findRepoRoot(t)
	conflict, err := catalogEntryExists(root, "anthropic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conflict == nil {
		t.Fatal("expected a conflict for anthropic")
	}
	if media := nonLLMKinds(conflict.Kinds); len(media) > 0 {
		t.Errorf("anthropic reported media kinds %v", media)
	}
	if strings.Contains(conflict.Detail, "Media Providers") {
		t.Errorf("chat provider got the media wording: %q", conflict.Detail)
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
