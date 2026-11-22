package quotalearn

import (
	"database/sql"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	// In-memory SQLite belongs to the connection that created it: a second
	// pooled connection would query an empty database and report "no such
	// table" at random.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := EnsureQuotaState(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db
}

func TestPersistRoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepo(db)

	k := Key{Provider: "groq", Model: "llama-3.3-70b", Connection: "conn-1"}

	// Initially empty.
	st := &state{}
	if err := repo.Load(k, st); err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if st.learnedRPM != 0 || st.learnedTPD != 0 {
		t.Fatalf("expected zero state, got rpm=%d tpd=%d", st.learnedRPM, st.learnedTPD)
	}

	// Save learned state.
	st.learnedRPM = 30
	st.learnedTPD = 100000
	st.cooldownUntil = time.Date(2026, 4, 1, 12, 5, 0, 0, time.UTC)
	if err := repo.Save(k, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Load into a fresh struct.
	st2 := &state{}
	if err := repo.Load(k, st2); err != nil {
		t.Fatalf("load after save: %v", err)
	}
	if st2.learnedRPM != 30 {
		t.Fatalf("expected rpm=30, got %d", st2.learnedRPM)
	}
	if st2.learnedTPD != 100000 {
		t.Fatalf("expected tpd=100000, got %d", st2.learnedTPD)
	}
	if !st2.cooldownUntil.Equal(st.cooldownUntil) {
		t.Fatalf("cooldown mismatch: got %s, want %s", st2.cooldownUntil, st.cooldownUntil)
	}
}

func TestPersistMissingRowIsZero(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepo(db)

	k := Key{Provider: "p", Model: "m", Connection: "c"}
	st := &state{learnedRPM: 5} // non-zero initial
	if err := repo.Load(k, st); err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if st.learnedRPM != 0 {
		t.Fatalf("expected zero after loading missing row, got %d", st.learnedRPM)
	}
}

func TestLearnerLoadsPersistedCooldown(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepo(db)
	until := time.Now().Add(time.Hour)
	if err := repo.SetCooldown("groq", "llama-3.3-70b-versatile", "conn-1", until); err != nil {
		t.Fatalf("set cooldown: %v", err)
	}

	l := New(Options{})
	l.WithPersistence(repo)
	blocked, reason := l.IsBlocked("groq", "llama-3.3-70b-versatile", "conn-1")
	if !blocked {
		t.Fatalf("expected persisted cooldown to block, reason=%q", reason)
	}
}
