package db

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// providerModelLocksTable parks a (provider, model) pair.
//
// It is a table of its own and not a field on the providerConnections.data
// blob: the state belongs to the pair, not to a credential, and the shared
// Next.js dashboard reads that blob and must keep seeing it unchanged.
//
// The primary key is the pair itself, so the lookup the router does on every
// request is a single indexed equality and needs no extra index.
var providerModelLocksTable = tableDef{
	name: "providerModelLocks",
	columns: [][2]string{
		{"provider", "TEXT NOT NULL"},
		{"model", "TEXT NOT NULL"},
		{"lockedUntil", "TEXT NOT NULL"},
		{"reason", "TEXT NOT NULL DEFAULT ''"},
		{"status", "INTEGER NOT NULL DEFAULT 0"},
		{"updatedAt", "TEXT NOT NULL"},
	},
	primaryKey: "PRIMARY KEY (provider, model)",
}

// ProviderModelLock is one parked pair. LockedUntil is the absolute deadline:
// the lockout expires on its own, so no cleanup pass is needed to free it.
type ProviderModelLock struct {
	Provider    string
	Model       string
	LockedUntil time.Time
	Reason      string
	Status      int
	UpdatedAt   time.Time
}

// EnsureProviderModelLocks creates the table when absent. Safe to call on
// every startup and invisible to the Next.js dashboard, which ignores tables
// it does not know. Exported for the same reason as EnsureUpstreamLeases: a
// test fixture that builds only the upstream core schema still gets the shape
// the runtime queries.
func EnsureProviderModelLocks(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("provider model locks: nil db")
	}
	if err := ensureTable(db, providerModelLocksTable); err != nil {
		return fmt.Errorf("provider model locks: %w", err)
	}
	return nil
}

// providerModelLocksReady remembers the database handles this process already
// prepared.
//
// The boot bootstrap creates the table, but a fixture that builds the schema
// by hand never runs it, and a read that failed on a missing table would then
// flood the log and silently disable the feature on every request. Preparing
// once per handle keeps the table a fact of the database rather than of the
// bootstrap that happened to run first.
var providerModelLocksReady sync.Map // map[*sql.DB]struct{}

// ensureProviderModelLocks creates the table once per database handle.
func ensureProviderModelLocks(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("provider model locks: nil db")
	}
	if _, ready := providerModelLocksReady.Load(db); ready {
		return nil
	}
	if err := EnsureProviderModelLocks(db); err != nil {
		return err
	}
	providerModelLocksReady.Store(db, struct{}{})
	return nil
}

// LockProviderModel parks a (provider, model) pair until the given deadline.
//
// The upsert never shortens a lockout that is already longer: the writer that
// computed a shorter window must not cut the park short. The comparison lives
// in the statement's DO UPDATE clause because a read-then-write in Go would
// race with a concurrent writer and could park the pair for less time than
// the longest window anyone asked for.
func (r *Repo) LockProviderModel(provider, model string, until time.Time, reason string, status int) error {
	if provider == "" || model == "" {
		return fmt.Errorf("lock provider model: provider and model are required")
	}
	if err := ensureProviderModelLocks(r.db); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := r.db.Exec(
		`INSERT INTO providerModelLocks (provider, model, lockedUntil, reason, status, updatedAt)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(provider, model) DO UPDATE SET
		   lockedUntil = excluded.lockedUntil,
		   reason      = excluded.reason,
		   status      = excluded.status,
		   updatedAt   = excluded.updatedAt
		 WHERE excluded.lockedUntil > providerModelLocks.lockedUntil`,
		provider, model, until.UTC().Format(time.RFC3339), reason, status, now.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("lock provider model %s/%s: %w", provider, model, err)
	}
	return nil
}

// ProviderModelLockedUntil returns the deadline of an active lockout.
//
// An expired row reads as absent: the lockout is self-expiring and the caller
// treats a deadline in the past as no lockout at all. A read error is
// reported, and the caller must fail open — the row is a hint that the pair
// is dead, and refusing to route on a database hiccup would take the whole
// provider out of rotation.
func (r *Repo) ProviderModelLockedUntil(provider, model string) (time.Time, bool, error) {
	if provider == "" || model == "" {
		return time.Time{}, false, nil
	}
	if err := ensureProviderModelLocks(r.db); err != nil {
		return time.Time{}, false, err
	}
	var lockedUntil string
	err := r.db.QueryRow(
		`SELECT lockedUntil FROM providerModelLocks WHERE provider = ? AND model = ?`, provider, model,
	).Scan(&lockedUntil)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read provider model lock %s/%s: %w", provider, model, err)
	}
	until, err := time.Parse(time.RFC3339, lockedUntil)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read provider model lock %s/%s: bad lockedUntil: %w", provider, model, err)
	}
	if !until.After(time.Now().UTC()) {
		return time.Time{}, false, nil
	}
	return until, true, nil
}

// ClearProviderModelLock drops the lockout of one pair. A served request
// calls it: the pair answered, so it must not stay parked until its deadline.
func (r *Repo) ClearProviderModelLock(provider, model string) error {
	if provider == "" || model == "" {
		return fmt.Errorf("clear provider model lock: provider and model are required")
	}
	if err := ensureProviderModelLocks(r.db); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`DELETE FROM providerModelLocks WHERE provider = ? AND model = ?`, provider, model,
	)
	if err != nil {
		return fmt.Errorf("clear provider model lock %s/%s: %w", provider, model, err)
	}
	return nil
}

// ClearProviderModelLocks drops lockouts by scope, mirroring
// ResetProviderHealth:
//   - provider="" and model="" → every parked pair
//   - provider set and model="" → every model of that provider
//   - provider="" and model set → that model at every provider
//   - both set → that pair
func (r *Repo) ClearProviderModelLocks(provider, model string) error {
	if err := ensureProviderModelLocks(r.db); err != nil {
		return err
	}
	var (
		query string
		args  []any
	)
	switch {
	case provider == "" && model == "":
		query = `DELETE FROM providerModelLocks`
	case provider == "":
		query = `DELETE FROM providerModelLocks WHERE model = ?`
		args = []any{model}
	case model == "":
		query = `DELETE FROM providerModelLocks WHERE provider = ?`
		args = []any{provider}
	default:
		query = `DELETE FROM providerModelLocks WHERE provider = ? AND model = ?`
		args = []any{provider, model}
	}
	if _, err := r.db.Exec(query, args...); err != nil {
		return fmt.Errorf("clear provider model locks %s/%s: %w", provider, model, err)
	}
	return nil
}

// ListProviderModelLocks returns every recorded lockout, keyed by
// "provider/model". Expired rows are included: the caller decides which of
// them are still parked.
func (r *Repo) ListProviderModelLocks() (map[string]ProviderModelLock, error) {
	if err := ensureProviderModelLocks(r.db); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(
		`SELECT provider, model, lockedUntil, reason, status, updatedAt FROM providerModelLocks`,
	)
	if err != nil {
		return nil, fmt.Errorf("list provider model locks: %w", err)
	}
	defer rows.Close()

	locks := map[string]ProviderModelLock{}
	for rows.Next() {
		var (
			lock        ProviderModelLock
			lockedUntil string
			updatedAt   string
		)
		if err := rows.Scan(&lock.Provider, &lock.Model, &lockedUntil, &lock.Reason, &lock.Status, &updatedAt); err != nil {
			return nil, fmt.Errorf("list provider model locks: %w", err)
		}
		lock.LockedUntil, _ = time.Parse(time.RFC3339, lockedUntil)
		lock.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		locks[lock.Provider+"/"+lock.Model] = lock
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list provider model locks: %w", err)
	}
	return locks, nil
}
