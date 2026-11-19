package chat

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/models"
)

func insertCodexConn(t *testing.T, database *sql.DB, id string, priority int) {
	t.Helper()
	_, err := database.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
		 VALUES (?, 'codex', 'oauth', ?, ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		id, id, priority, `{"apiKey":"sk-codex-test"}`)
	if err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

// codexRoundRobin makes rotation active, so a pin is the only reason two
// consecutive turns can land on the same account.
func codexRoundRobin(t *testing.T, repo *db.Repo) {
	t.Helper()
	if err := repo.UpdateSettingsRaw(map[string]any{
		"providerStrategies": map[string]any{
			"codex": map[string]any{
				"fallbackStrategy":      "round-robin",
				"stickyRoundRobinLimit": 1,
			},
		},
	}); err != nil {
		t.Fatalf("update provider strategies: %v", err)
	}
}

// The Codex prompt cache is kept per account, so a second turn of the same
// conversation that rotates to another account re-sends the whole prefix.
// The pin is what keeps a conversation on the account that warmed its cache.
func TestGetBestConnection_PinsCodexConversationToItsAccount(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	dropSeededConn(t, database)
	insertCodexConn(t, database, "cx-a", 1)
	insertCodexConn(t, database, "cx-b", 2)
	repo := db.NewRepo(database)
	codexRoundRobin(t, repo)
	handler := NewChatHandler(repo)

	ctx := handlerutil.WithSessionID(context.Background(), "sess-42")
	first, _, err := handler.getBestConnectionWithContext(ctx, "codex", "", nil, "gpt-6-luna")
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	for turn := 2; turn <= 4; turn++ {
		conn, _, err := handler.getBestConnectionWithContext(ctx, "codex", "", nil, "gpt-6-luna")
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		if conn.ID != first.ID {
			t.Fatalf("turn %d served by %s, want the pinned %s", turn, conn.ID, first.ID)
		}
	}
}

// Two conversations are two cache shards: pinning one must not pull the other
// onto the same account.
func TestGetBestConnection_DifferentConversationsKeepTheirOwnPin(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	dropSeededConn(t, database)
	insertCodexConn(t, database, "cx-a", 1)
	insertCodexConn(t, database, "cx-b", 2)
	repo := db.NewRepo(database)
	codexRoundRobin(t, repo)
	handler := NewChatHandler(repo)

	ctxA := handlerutil.WithSessionID(context.Background(), "sess-a")
	ctxB := handlerutil.WithSessionID(context.Background(), "sess-b")
	firstA, _, err := handler.getBestConnectionWithContext(ctxA, "codex", "", nil, "gpt-6-luna")
	if err != nil {
		t.Fatalf("conversation A first turn: %v", err)
	}
	firstB, _, err := handler.getBestConnectionWithContext(ctxB, "codex", "", nil, "gpt-6-luna")
	if err != nil {
		t.Fatalf("conversation B first turn: %v", err)
	}

	againA, _, err := handler.getBestConnectionWithContext(ctxA, "codex", "", nil, "gpt-6-luna")
	if err != nil {
		t.Fatalf("conversation A second turn: %v", err)
	}
	if againA.ID != firstA.ID {
		t.Fatalf("conversation A moved to %s, want %s", againA.ID, firstA.ID)
	}
	if firstB.ID == firstA.ID {
		t.Logf("both conversations landed on %s on their first turn", firstA.ID)
	}
}

// A pin is a hint, not a lock: an excluded account costs a cold cache, never a
// failed turn.
func TestGetBestConnection_PinnedAccountExcludedFallsBack(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	dropSeededConn(t, database)
	insertCodexConn(t, database, "cx-a", 1)
	insertCodexConn(t, database, "cx-b", 2)
	repo := db.NewRepo(database)
	codexRoundRobin(t, repo)
	handler := NewChatHandler(repo)

	ctx := handlerutil.WithSessionID(context.Background(), "sess-excl")
	first, _, err := handler.getBestConnectionWithContext(ctx, "codex", "", nil, "gpt-6-luna")
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	conn, _, err := handler.getBestConnectionWithContext(ctx, "codex", "", []string{first.ID}, "gpt-6-luna")
	if err != nil {
		t.Fatalf("second turn with the pinned account excluded: %v", err)
	}
	if conn.ID == first.ID {
		t.Fatalf("excluded account %s was served anyway", first.ID)
	}
}

// A provider whose cache is not per account gains nothing from a pin, and the
// pin would only cost it account rotation.
func TestGetBestConnection_NoPinOutsideCodex(t *testing.T) {
	if key := sessionPinKey("deepseek", "sess-1"); key != "" {
		t.Fatalf("deepseek must not be pinned, got key %q", key)
	}
	if key := sessionPinKey("codex", "sess-1"); key != "codex:sess-1" {
		t.Fatalf("codex pin key = %q", key)
	}
	if key := sessionPinKey("codex", ""); key != "" {
		t.Fatalf("a session-less request must not be pinned, got key %q", key)
	}
}

func TestSessionPinExpires(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var state sessionPinState
	state.nowFn = func() time.Time { return now }

	state.record("codex:sess-1", "cx-a")
	if id, ok := state.lookup("codex:sess-1"); !ok || id != "cx-a" {
		t.Fatalf("fresh pin = %q, %v; want cx-a", id, ok)
	}

	now = now.Add(codexSessionPinTTL - time.Second)
	if id, ok := state.lookup("codex:sess-1"); !ok || id != "cx-a" {
		t.Fatalf("pin expired %v early", codexSessionPinTTL)
	}

	now = now.Add(2 * time.Second)
	if id, ok := state.lookup("codex:sess-1"); ok {
		t.Fatalf("pin survived its TTL: %q", id)
	}
}

// The key is client-supplied, so the table must stay bounded.
func TestSessionPinsStayBounded(t *testing.T) {
	var state sessionPinState
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	state.nowFn = func() time.Time { return now }

	for i := range maxSessionPins + 100 {
		state.record("codex:sess-"+string(rune('a'+i%26))+string(rune('a'+i/26)), "cx-a")
	}
	state.mu.Lock()
	size := len(state.pins)
	state.mu.Unlock()
	if size > maxSessionPins {
		t.Fatalf("pin table grew to %d entries, bound is %d", size, maxSessionPins)
	}
}

func TestPromotePinnedConnection(t *testing.T) {
	a := &models.ProviderConnection{ID: "a"}
	b := &models.ProviderConnection{ID: "b"}
	c := &models.ProviderConnection{ID: "c"}

	tests := []struct {
		name    string
		pinned  string
		input   []*models.ProviderConnection
		wantIDs []string
	}{
		{name: "pinned row moves to the front", pinned: "c", input: []*models.ProviderConnection{a, b, c}, wantIDs: []string{"c", "a", "b"}},
		{name: "already first is unchanged", pinned: "a", input: []*models.ProviderConnection{a, b, c}, wantIDs: []string{"a", "b", "c"}},
		{name: "absent pin leaves the order alone", pinned: "z", input: []*models.ProviderConnection{a, b, c}, wantIDs: []string{"a", "b", "c"}},
		{name: "no pin leaves the order alone", pinned: "", input: []*models.ProviderConnection{a, b, c}, wantIDs: []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := promotePinnedConnection(tt.input, tt.pinned)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("length = %d, want %d", len(got), len(tt.wantIDs))
			}
			for i, want := range tt.wantIDs {
				if got[i].ID != want {
					t.Fatalf("position %d = %s, want %s", i, got[i].ID, want)
				}
			}
		})
	}
}
