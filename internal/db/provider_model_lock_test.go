package db

import (
	"testing"
	"time"
)

func setupProviderModelLockRepo(t *testing.T) *Repo {
	t.Helper()
	database, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	// The table is part of the core schema, so a real boot creates it: the
	// fixture goes through the same bootstrap instead of a hand-written DDL.
	if err := EnsureCoreSchema(database); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return NewRepo(database)
}

// The fork-local table has to be created by the same bootstrap every boot
// uses, or the runtime finds no table to write the lockout into.
func TestEnsureCoreSchema_CreatesProviderModelLocks(t *testing.T) {
	repo := setupProviderModelLockRepo(t)
	if !schemaTableNames(t, repo)["providerModelLocks"] {
		t.Error("expected the providerModelLocks table to exist after EnsureCoreSchema")
	}
}

// A parked pair is readable with its deadline, reason and status: the
// dashboard alert renders them.
func TestProviderModelLock_LockAndRead(t *testing.T) {
	repo := setupProviderModelLockRepo(t)

	until := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	if err := repo.LockProviderModel("deepseek", "deepseek-chat", until, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}

	got, locked, err := repo.ProviderModelLockedUntil("deepseek", "deepseek-chat")
	if err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	}
	if !locked {
		t.Fatal("expected the pair to be parked")
	}
	if !got.Equal(until) {
		t.Errorf("deadline = %s, want %s", got.Format(time.RFC3339), until.Format(time.RFC3339))
	}

	locks, err := repo.ListProviderModelLocks()
	if err != nil {
		t.Fatalf("ListProviderModelLocks: %v", err)
	}
	lock, ok := locks["deepseek/deepseek-chat"]
	if !ok {
		t.Fatalf("expected the pair in the listing, got %v", locks)
	}
	if lock.Reason != "quota_exhausted" || lock.Status != 429 {
		t.Errorf("listing = %+v, want reason quota_exhausted and status 429", lock)
	}
	if lock.Provider != "deepseek" || lock.Model != "deepseek-chat" {
		t.Errorf("listing = %+v, want provider deepseek and model deepseek-chat", lock)
	}
}

// An upsert must never shorten a lockout that is already longer: the second
// writer computed a shorter window and must not cut the park short.
func TestProviderModelLock_UpsertKeepsTheLongerDeadline(t *testing.T) {
	repo := setupProviderModelLockRepo(t)

	long := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	short := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second)

	if err := repo.LockProviderModel("mistral", "mistral-large", long, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}
	if err := repo.LockProviderModel("mistral", "mistral-large", short, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel (shorter): %v", err)
	}

	got, locked, err := repo.ProviderModelLockedUntil("mistral", "mistral-large")
	if err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	}
	if !locked {
		t.Fatal("expected the pair to stay parked")
	}
	if !got.Equal(long) {
		t.Errorf("deadline = %s, want the longer %s", got.Format(time.RFC3339), long.Format(time.RFC3339))
	}

	// A longer window does replace the shorter one.
	if err := repo.LockProviderModel("mistral", "mistral-large", long.Add(time.Hour), "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel (longer): %v", err)
	}
	got, _, err = repo.ProviderModelLockedUntil("mistral", "mistral-large")
	if err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	}
	if !got.Equal(long.Add(time.Hour)) {
		t.Errorf("deadline = %s, want %s", got.Format(time.RFC3339), long.Add(time.Hour).Format(time.RFC3339))
	}
}

// A lockout is self-expiring: once the deadline has passed the row reads as
// absent, so the caller treats the pair as free.
func TestProviderModelLock_ExpiredRowReadsAsAbsent(t *testing.T) {
	repo := setupProviderModelLockRepo(t)

	past := time.Now().UTC().Add(-time.Minute)
	if err := repo.LockProviderModel("openrouter", "gpt-4o", past, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}

	got, locked, err := repo.ProviderModelLockedUntil("openrouter", "gpt-4o")
	if err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	}
	if locked {
		t.Errorf("expected an expired lockout to read as absent, got deadline %s", got.Format(time.RFC3339))
	}
	if !got.IsZero() {
		t.Errorf("expected a zero deadline for an expired lockout, got %s", got.Format(time.RFC3339))
	}
}

// The clear helper follows the same scope matrix as ResetProviderHealth: all
// pairs, every model of one provider, or one pair.
func TestProviderModelLock_ClearScopes(t *testing.T) {
	repo := setupProviderModelLockRepo(t)

	until := time.Now().UTC().Add(time.Hour)
	pairs := [][2]string{
		{"mistral", "mistral-large"},
		{"mistral", "mistral-small"},
		{"nvidia", "nemotron"},
	}
	for _, p := range pairs {
		if err := repo.LockProviderModel(p[0], p[1], until, "quota_exhausted", 429); err != nil {
			t.Fatalf("LockProviderModel %s/%s: %v", p[0], p[1], err)
		}
	}

	// One pair.
	if err := repo.ClearProviderModelLock("nvidia", "nemotron"); err != nil {
		t.Fatalf("ClearProviderModelLock: %v", err)
	}
	if _, locked, _ := repo.ProviderModelLockedUntil("nvidia", "nemotron"); locked {
		t.Error("expected the pair to be cleared")
	}
	if _, locked, _ := repo.ProviderModelLockedUntil("mistral", "mistral-large"); !locked {
		t.Error("clearing one pair must not touch another provider's lockout")
	}

	// Every model of one provider.
	if err := repo.ClearProviderModelLocks("mistral", ""); err != nil {
		t.Fatalf("ClearProviderModelLocks (provider): %v", err)
	}
	if _, locked, _ := repo.ProviderModelLockedUntil("mistral", "mistral-large"); locked {
		t.Error("expected every model of the provider to be cleared")
	}
	if _, locked, _ := repo.ProviderModelLockedUntil("mistral", "mistral-small"); locked {
		t.Error("expected every model of the provider to be cleared")
	}

	// Everything.
	if err := repo.LockProviderModel("openrouter", "gpt-4o", until, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}
	if err := repo.ClearProviderModelLocks("", ""); err != nil {
		t.Fatalf("ClearProviderModelLocks (all): %v", err)
	}
	locks, err := repo.ListProviderModelLocks()
	if err != nil {
		t.Fatalf("ListProviderModelLocks: %v", err)
	}
	if len(locks) != 0 {
		t.Errorf("expected every lockout to be cleared, got %v", locks)
	}
}

// A pair that was never parked reads as absent, not as an error.
func TestProviderModelLock_UnknownPairReadsAsAbsent(t *testing.T) {
	repo := setupProviderModelLockRepo(t)

	_, locked, err := repo.ProviderModelLockedUntil("groq", "llama-3.1-70b")
	if err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	}
	if locked {
		t.Error("expected an unknown pair to read as absent")
	}
}
