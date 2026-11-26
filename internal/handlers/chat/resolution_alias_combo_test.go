package chat

import (
	"reflect"
	"testing"

	"nox9go/proxy/internal/db"
)

// S1 — an alias whose target carries no "/" names a combo.
//
// Before this, the alias branch only followed a target containing "/", so a
// slash-less target was dropped and the request fell through with the alias
// name itself. The alias silently did nothing.
func TestResolveModel_AliasWithSlashlessTargetResolvesCombo(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
		('cw', 'Combo-Work', 'fallback', '["deepseek/deepseek-chat","groq/llama-3.3-70b"]', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed combo: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO kv (scope, key, value) VALUES ('modelAliases', 'pi-work', '"Combo-Work"')`); err != nil {
		t.Fatalf("seed alias: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))

	direct, err := handler.resolveModel("Combo-Work")
	if err != nil {
		t.Fatalf("direct combo resolution: %v", err)
	}
	viaAlias, err := handler.resolveModel("pi-work")
	if err != nil {
		t.Fatalf("alias to combo: %v", err)
	}

	wantModels := []string{"deepseek/deepseek-chat", "groq/llama-3.3-70b"}
	if !reflect.DeepEqual(viaAlias.ComboModels, wantModels) {
		t.Errorf("ComboModels = %v, want %v", viaAlias.ComboModels, wantModels)
	}
	if viaAlias.Provider != direct.Provider || viaAlias.Model != direct.Model {
		t.Errorf("alias resolved to %s/%s, want %s/%s", viaAlias.Provider, viaAlias.Model, direct.Provider, direct.Model)
	}
	if viaAlias.Strategy != direct.Strategy {
		t.Errorf("Strategy = %q, want %q", viaAlias.Strategy, direct.Strategy)
	}
	if viaAlias.StickyLimit != direct.StickyLimit {
		t.Errorf("StickyLimit = %d, want %d", viaAlias.StickyLimit, direct.StickyLimit)
	}
}

// S1 — a combo with no usable seat list is inert, not an error: the name is not
// a combo as far as resolution is concerned, and the request continues.
func TestResolveModel_ComboWithUnusableSeatListIsInert(t *testing.T) {
	tests := []struct {
		name   string
		models string
	}{
		{name: "empty seat list", models: `[]`},
		{name: "unparseable seat list", models: `not-json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, cleanup := setupChatTestDB(t)
			defer cleanup()

			if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
				('ce', 'Combo-Empty', 'fallback', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, tt.models); err != nil {
				t.Fatalf("seed combo: %v", err)
			}

			handler := NewChatHandler(db.NewRepo(database))

			got, err := handler.resolveModel("Combo-Empty")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got.ComboModels) != 0 {
				t.Errorf("ComboModels = %v, want empty: an unusable seat list is not a combo", got.ComboModels)
			}
		})
	}
}

// S1/S3 — with no repository there is no alias and no combo to consult, and the
// lookup must not panic on the nil Repo.
func TestResolveModel_NilRepoSkipsAliasAndComboLookup(t *testing.T) {
	handler := NewChatHandler(nil)

	got, err := handler.resolveComboByName("Combo-Anything")
	if err != nil {
		t.Fatalf("resolveComboByName with a nil Repo: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}

	if _, err := handler.resolveModel("plain-model"); err == nil {
		t.Error("resolveModel with a nil Repo should fail: no provider can serve the request")
	}
}

// S2 — an alias whose target contains "/" still resolves as provider/model.
func TestResolveModel_AliasWithSlashTargetUnchanged(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	handler := NewChatHandler(db.NewRepo(database))

	info, err := handler.resolveModel("fast-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "deepseek" || info.Model != "deepseek-chat" {
		t.Errorf("got %s/%s, want deepseek/deepseek-chat", info.Provider, info.Model)
	}
	if len(info.ComboModels) != 0 {
		t.Errorf("ComboModels = %v, want empty: a provider/model alias is not a combo", info.ComboModels)
	}
}

// S3 — a slash-less target that names no combo changes nothing: the alias is
// skipped and resolution continues with the alias name itself, exactly as it
// did before the lookup existed. The target is deliberately NOT substituted:
// an alias pointing at nothing must not silently reroute the request.
func TestResolveModel_AliasToUnknownSlashlessTargetIsInert(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(`INSERT INTO kv (scope, key, value) VALUES ('modelAliases', 'orphan-alias', '"not-a-combo"')`); err != nil {
		t.Fatalf("seed alias: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))

	got, err := handler.resolveModel("orphan-alias")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Provider != "deepseek" || got.Model != "orphan-alias" {
		t.Errorf("got %s/%s, want deepseek/orphan-alias (alias name, not the target)", got.Provider, got.Model)
	}
	if len(got.ComboModels) != 0 {
		t.Errorf("ComboModels = %v, want empty", got.ComboModels)
	}
}
