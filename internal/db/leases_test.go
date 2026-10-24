package db

import (
	"testing"
	"time"
)

func setupLeasesTestRepo(t *testing.T) (*Repo, func()) {
	t.Helper()
	database, cleanup := setupTestDB(t)
	if err := EnsureUpstreamLeases(database); err != nil {
		cleanup()
		t.Fatalf("ensure upstream_leases: %v", err)
	}
	return NewRepo(database), cleanup
}

func TestUpstreamLeases_AcquireReadRelease(t *testing.T) {
	repo, cleanup := setupLeasesTestRepo(t)
	defer cleanup()

	got, err := repo.ReadLease(LeaseScopeFreebuffSession, "k1")
	if err != nil || got != nil {
		t.Fatalf("expected miss, got %v, %v", got, err)
	}

	held, err := repo.AcquireLease(LeaseScopeFreebuffSession, "k1", "inst-a", time.Hour)
	if err != nil || !held {
		t.Fatalf("expected acquire, got held=%v err=%v", held, err)
	}

	got, err = repo.ReadLease(LeaseScopeFreebuffSession, "k1")
	if err != nil || got == nil || got.Value != "inst-a" {
		t.Fatalf("expected inst-a, got %+v, %v", got, err)
	}

	// Second acquirer loses while the lease is live.
	held, err = repo.AcquireLease(LeaseScopeFreebuffSession, "k1", "inst-b", time.Hour)
	if err != nil || held {
		t.Fatalf("expected loss, got held=%v err=%v", held, err)
	}

	// Refresh by the holder extends; refresh by a stranger fails.
	ok, err := repo.RefreshLease(LeaseScopeFreebuffSession, "k1", "inst-a", 2*time.Hour)
	if err != nil || !ok {
		t.Fatalf("expected refresh ok, got %v, %v", ok, err)
	}
	ok, err = repo.RefreshLease(LeaseScopeFreebuffSession, "k1", "inst-b", 2*time.Hour)
	if err != nil || ok {
		t.Fatalf("expected stranger refresh fail, got %v, %v", ok, err)
	}

	// Release with the wrong value must not delete the row.
	if err := repo.ReleaseLease(LeaseScopeFreebuffSession, "k1", "inst-b"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err = repo.ReadLease(LeaseScopeFreebuffSession, "k1")
	if err != nil || got == nil || got.Value != "inst-a" {
		t.Fatalf("wrong-value release deleted the row: %+v, %v", got, err)
	}

	if err := repo.ReleaseLease(LeaseScopeFreebuffSession, "k1", "inst-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err = repo.ReadLease(LeaseScopeFreebuffSession, "k1")
	if err != nil || got != nil {
		t.Fatalf("expected miss after release, got %+v, %v", got, err)
	}
}

func TestUpstreamLeases_ExpiredTakeover(t *testing.T) {
	repo, cleanup := setupLeasesTestRepo(t)
	defer cleanup()

	held, err := repo.AcquireLease(LeaseScopeFreebuffSession, "k2", "inst-old", -time.Minute)
	if err != nil || !held {
		t.Fatalf("expected acquire (immediately expiring), got %v, %v", held, err)
	}
	got, err := repo.ReadLease(LeaseScopeFreebuffSession, "k2")
	if err != nil || got != nil {
		t.Fatalf("expired lease must read as miss, got %+v, %v", got, err)
	}

	// A dead holder's row must not block a new acquirer.
	held, err = repo.AcquireLease(LeaseScopeFreebuffSession, "k2", "inst-new", time.Hour)
	if err != nil || !held {
		t.Fatalf("expected takeover, got %v, %v", held, err)
	}
	got, err = repo.ReadLease(LeaseScopeFreebuffSession, "k2")
	if err != nil || got == nil || got.Value != "inst-new" {
		t.Fatalf("expected inst-new, got %+v, %v", got, err)
	}
}

func TestUpstreamLeases_ScopesIsolated(t *testing.T) {
	repo, cleanup := setupLeasesTestRepo(t)
	defer cleanup()

	if _, err := repo.AcquireLease("scope-a", "same-key", "v-a", time.Hour); err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	held, err := repo.AcquireLease("scope-b", "same-key", "v-b", time.Hour)
	if err != nil || !held {
		t.Fatalf("same key in another scope must not collide, got %v, %v", held, err)
	}
}

func TestLeaseKey_HidesSecrets(t *testing.T) {
	k := LeaseKey("raw-token-abc", "model-x")
	if len(k) != 64 {
		t.Fatalf("expected 64 hex chars, got %q", k)
	}
	if k == "raw-token-abc" || containsStr(k, "raw-token") {
		t.Fatalf("key leaks secret material: %q", k)
	}
	if LeaseKey("raw-token-abc", "model-x") != k {
		t.Fatalf("key must be deterministic")
	}
	if LeaseKey("other", "model-x") == k {
		t.Fatalf("key must differ per token")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
