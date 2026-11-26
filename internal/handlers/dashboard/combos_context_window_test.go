package dashboard

import (
	"net/http"
	"testing"
)

// The declared window travels with the combo row: a create that carries it
// stores it, and a create that says nothing leaves it at zero, which is the
// derived seat minimum.
func TestCreateComboDeclaredContextWindow(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "declared window is stored",
			body: map[string]any{"id": "c-window", "name": "window-combo", "models": []string{"deepseek/deepseek-chat"}, "contextWindow": 1000000},
			want: 1000000,
		},
		{
			name: "absent window stays derived",
			body: map[string]any{"id": "c-window", "name": "window-combo", "models": []string{"deepseek/deepseek-chat"}},
			want: 0,
		},
		{
			name: "explicit zero stays derived",
			body: map[string]any{"id": "c-window", "name": "window-combo", "models": []string{"deepseek/deepseek-chat"}, "contextWindow": 0},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, cleanup := setupTestDB(t)
			defer cleanup()
			router := setupTestRouter(repo)

			rec := postJSON(t, router, "/api/combos", tt.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("create = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			if got := mustCombo(t, repo, "c-window").ContextWindow; got != tt.want {
				t.Errorf("stored contextWindow = %d, want %d", got, tt.want)
			}
		})
	}
}

// A window outside the accepted range is refused before anything is written, so
// a refused create leaves no combo behind for the operator to delete.
func TestCreateComboRejectsOutOfRangeContextWindow(t *testing.T) {
	tests := []struct {
		name   string
		window int
	}{
		{name: "negative", window: -1},
		{name: "above the typo guard", window: 100000001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, cleanup := setupTestDB(t)
			defer cleanup()
			router := setupTestRouter(repo)

			rec := postJSON(t, router, "/api/combos", map[string]any{
				"id": "c-bad", "name": "bad-combo", "models": []string{"deepseek/deepseek-chat"},
				"contextWindow": tt.window,
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("create = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if combo, err := repo.GetComboById("c-bad"); err != nil || combo != nil {
				t.Errorf("combo stored despite the refusal: %+v (err %v)", combo, err)
			}
		})
	}
}

// An edit writes the window only when the request carries the field: an
// unrelated edit keeps the declared value, an explicit zero restores the
// derived minimum, and a refused value leaves the row untouched.
func TestUpdateComboContextWindowPresentAndAbsent(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	rec := postJSON(t, router, "/api/combos", map[string]any{
		"id": "c-edit", "name": "edit-combo", "models": []string{"deepseek/deepseek-chat"},
		"contextWindow": 1000000,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Present: replaced.
	rec = putJSON(t, router, "/api/combos/c-edit", map[string]any{"contextWindow": 500000})
	if rec.Code != http.StatusOK {
		t.Fatalf("window change = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := mustCombo(t, repo, "c-edit").ContextWindow; got != 500000 {
		t.Errorf("stored contextWindow = %d, want 500000", got)
	}

	// Absent: a rename must not clear it.
	rec = putJSON(t, router, "/api/combos/c-edit", map[string]any{"name": "renamed-combo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := mustCombo(t, repo, "c-edit").ContextWindow; got != 500000 {
		t.Errorf("stored contextWindow after an unrelated edit = %d, want 500000", got)
	}

	// Explicit zero: back to the derived minimum.
	rec = putJSON(t, router, "/api/combos/c-edit", map[string]any{"contextWindow": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("zeroing = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := mustCombo(t, repo, "c-edit").ContextWindow; got != 0 {
		t.Errorf("stored contextWindow = %d, want 0", got)
	}

	// Out of range: refused, stored value unchanged.
	rec = putJSON(t, router, "/api/combos/c-edit", map[string]any{"contextWindow": 200000000})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range update = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := mustCombo(t, repo, "c-edit").ContextWindow; got != 0 {
		t.Errorf("stored contextWindow after a refused edit = %d, want 0", got)
	}
}
