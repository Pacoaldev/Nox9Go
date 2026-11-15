package quotalearn

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// EnsureQuotaState creates the quota_state table if it does not exist.
// It follows the house style of internal/db/leases.go.
func EnsureQuotaState(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("quota_state: nil db")
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS quota_state (
			provider    TEXT NOT NULL,
			model       TEXT NOT NULL,
			connection  TEXT NOT NULL,
			learned_rpm INTEGER DEFAULT 0,
			learned_rpd INTEGER DEFAULT 0,
			learned_tpm INTEGER DEFAULT 0,
			learned_tpd INTEGER DEFAULT 0,
			cooldown_until TEXT DEFAULT '',
			PRIMARY KEY (provider, model, connection)
		)`)
	if err != nil {
		return fmt.Errorf("quota_state: create table: %w", err)
	}
	return nil
}

// Repo persists quota state using an existing *sql.DB.
type Repo struct {
	db *sql.DB
}

// NewRepo wraps a database handle for quota persistence.
func NewRepo(db *sql.DB) *Repo {
	return &Repo{db: db}
}

// Load reads persisted state for a key into the in-memory state.
// Missing rows zero out the destination fields so a stale in-memory value
// does not survive a missing persisted row.
func (r *Repo) Load(k Key, dst *state) error {
	var learnedRPM, learnedRPD, learnedTPM, learnedTPD int
	var cooldownUntilStr string
	err := r.db.QueryRow(
		`SELECT learned_rpm, learned_rpd, learned_tpm, learned_tpd, cooldown_until
		   FROM quota_state
		  WHERE provider = ? AND model = ? AND connection = ?`,
		k.Provider, k.Model, k.Connection,
	).Scan(&learnedRPM, &learnedRPD, &learnedTPM, &learnedTPD, &cooldownUntilStr)
	if err == sql.ErrNoRows {
		// Zero out destination so stale memory state does not survive.
		*dst = state{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("load quota_state %s: %w", k, err)
	}
	dst.learnedRPM = learnedRPM
	dst.learnedRPD = learnedRPD
	dst.learnedTPM = learnedTPM
	dst.learnedTPD = learnedTPD
	if cooldownUntilStr != "" {
		t, perr := time.Parse(time.RFC3339, cooldownUntilStr)
		if perr == nil {
			dst.cooldownUntil = t
		}
	}
	return nil
}

// Save writes the learned ceilings and cooldown from an in-memory state.
// The sliding-window history is not persisted; it rebuilds from runtime
// observation after a restart.
func (r *Repo) Save(k Key, src *state) error {
	_, err := r.db.Exec(
		`INSERT INTO quota_state
			(provider, model, connection, learned_rpm, learned_rpd, learned_tpm, learned_tpd, cooldown_until)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(provider, model, connection) DO UPDATE SET
		   learned_rpm = excluded.learned_rpm,
		   learned_rpd = excluded.learned_rpd,
		   learned_tpm = excluded.learned_tpm,
		   learned_tpd = excluded.learned_tpd,
		   cooldown_until = excluded.cooldown_until`,
		k.Provider, k.Model, k.Connection,
		src.learnedRPM, src.learnedRPD, src.learnedTPM, src.learnedTPD,
		src.cooldownUntil.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save quota_state %s: %w", k, err)
	}
	return nil
}

// SetCooldown persists an active cooldown for a key so a restart keeps
// skipping the throttled connection. Used by tests and, later, by dashboard
// controls.
func (r *Repo) SetCooldown(provider, model, connID string, until time.Time) error {
	return r.Save(Key{Provider: provider, Model: model, Connection: connID}, &state{cooldownUntil: until})
}
