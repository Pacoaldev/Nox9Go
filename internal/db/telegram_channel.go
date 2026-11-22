package db

import (
	"database/sql"
	"fmt"
	"strings"

	"nox9go/proxy/internal/vault"
)

// telegramChannelTables back the optional Telegram transport. Both tables are
// Go-only: upstream declares neither and ignores them, so they are additive in
// the same sense as providerCapacityTables.
//
// The token is never stored in cleartext. telegramChannel keeps the sealed pair
// the vault produces (wrapped DEK + ciphertext) and nothing else, so a database
// copied without the master key yields no usable bot token.
var telegramChannelTables = []tableDef{
	{
		name: "telegramChannel",
		columns: [][2]string{
			{"id", "INTEGER PRIMARY KEY CHECK (id = 1)"},
			{"tokenWrappedDEK", "TEXT NOT NULL DEFAULT ''"},
			{"tokenCiphertext", "TEXT NOT NULL DEFAULT ''"},
			{"updatedAt", "TEXT NOT NULL DEFAULT ''"},
		},
	},
	{
		name: "telegramAllowedUser",
		columns: [][2]string{
			{"userId", "INTEGER PRIMARY KEY"},
			{"addedAt", "TEXT NOT NULL DEFAULT ''"},
		},
	},
}

// EnsureTelegramChannelTables creates the Go-only tables the Telegram channel
// needs. Exported for the same reason as EnsureProviderCapacityTables: a test
// fixture that builds only the upstream core tables still gets the shape the
// runtime queries.
func EnsureTelegramChannelTables(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("telegram channel tables: nil db")
	}
	for _, t := range telegramChannelTables {
		if err := ensureTable(db, t); err != nil {
			return err
		}
	}
	return nil
}

// TelegramChannelConfig is the read model the dashboard and the poll loop both
// consume. It deliberately carries a masked preview instead of the token, so no
// response can leak the secret by serializing this struct.
type TelegramChannelConfig struct {
	// Enabled reports whether the poll loop should run: a sealed token is
	// present AND at least one user is allowed. Either missing means the bot
	// answers nobody, which is the fail-closed contract.
	Enabled bool `json:"enabled"`
	// TokenPresent reports whether a token is stored, independent of Enabled.
	TokenPresent bool `json:"tokenPresent"`
	// TokenMasked is a preview such as "7712…cdef" for the operator to recognize
	// which token is stored. It never contains the full value.
	TokenMasked string `json:"tokenMasked,omitempty"`
	// AllowedUserIDs is the Telegram numeric user ID allowlist.
	AllowedUserIDs []int64 `json:"allowedUserIds"`
}

// GetTelegramChannelConfig reads the channel state. A database with no row yet
// reports a disabled channel rather than an error, so a fresh install starts
// silent without a migration step.
func (r *Repo) GetTelegramChannelConfig() (*TelegramChannelConfig, error) {
	cfg := &TelegramChannelConfig{AllowedUserIDs: []int64{}}

	var ciphertext string
	err := r.db.QueryRow(
		`SELECT tokenCiphertext FROM telegramChannel WHERE id = 1`,
	).Scan(&ciphertext)
	switch {
	case err == sql.ErrNoRows:
		// No token row: fall through to read the allowlist, which is a separate
		// table and may exist on its own.
	case err != nil:
		return nil, fmt.Errorf("get telegram channel config: %w", err)
	default:
		cfg.TokenPresent = ciphertext != ""
	}

	rows, err := r.db.Query(
		`SELECT userId FROM telegramAllowedUser ORDER BY userId`,
	)
	if err != nil {
		return nil, fmt.Errorf("get telegram allowlist: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan telegram allowlist: %w", err)
		}
		cfg.AllowedUserIDs = append(cfg.AllowedUserIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telegram allowlist: %w", err)
	}

	cfg.TokenMasked = r.maskedTelegramToken()
	cfg.Enabled = cfg.TokenPresent && len(cfg.AllowedUserIDs) > 0
	return cfg, nil
}

// TelegramToken returns the decrypted bot token, or an empty string when none is
// stored. It is the only accessor that returns cleartext, and it is never
// reached from a serialized response path.
func (r *Repo) TelegramToken() (string, error) {
	var wrapped, ciphertext string
	err := r.db.QueryRow(
		`SELECT tokenWrappedDEK, tokenCiphertext FROM telegramChannel WHERE id = 1`,
	).Scan(&wrapped, &ciphertext)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get telegram token: %w", err)
	}
	if ciphertext == "" {
		return "", nil
	}

	v := r.Vault()
	if v == nil || !v.Enabled() {
		return "", fmt.Errorf("get telegram token: %w", vault.ErrDisabled)
	}
	plaintext, err := v.Open(wrapped, ciphertext)
	if err != nil {
		return "", fmt.Errorf("open telegram token: %w", err)
	}
	return plaintext, nil
}

// SaveTelegramToken seals and stores the bot token, replacing any previous one.
// A token that fails to seal is not written at all, so a vault failure leaves the
// channel exactly as it was instead of storing a partial row.
func (r *Repo) SaveTelegramToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("save telegram token: empty token")
	}
	v := r.Vault()
	if v == nil || !v.Enabled() {
		return fmt.Errorf("save telegram token: %w", vault.ErrDisabled)
	}
	wrapped, ciphertext, err := v.Seal(token)
	if err != nil {
		return fmt.Errorf("save telegram token: seal: %w", err)
	}

	_, err = r.db.Exec(
		`INSERT INTO telegramChannel (id, tokenWrappedDEK, tokenCiphertext, updatedAt)
		 VALUES (1, ?, ?, datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET
		   tokenWrappedDEK = excluded.tokenWrappedDEK,
		   tokenCiphertext = excluded.tokenCiphertext,
		   updatedAt = excluded.updatedAt`,
		wrapped, ciphertext,
	)
	if err != nil {
		return fmt.Errorf("save telegram token: %w", err)
	}
	return nil
}

// ClearTelegramToken removes the stored token. The allowlist is left intact: it
// is the operator's authorization decision, not part of the credential.
func (r *Repo) ClearTelegramToken() error {
	_, err := r.db.Exec(
		`UPDATE telegramChannel
		    SET tokenWrappedDEK = '', tokenCiphertext = '', updatedAt = datetime('now')
		  WHERE id = 1`,
	)
	if err != nil {
		return fmt.Errorf("clear telegram token: %w", err)
	}
	return nil
}

// SetTelegramAllowedUserIDs replaces the whole allowlist in one transaction.
// Replacement rather than merge is deliberate: an operator who shortens the list
// expects the removed users to stop reaching the bot.
func (r *Repo) SetTelegramAllowedUserIDs(ids []int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("set telegram allowlist: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM telegramAllowedUser`); err != nil {
		return fmt.Errorf("set telegram allowlist: clear: %w", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO telegramAllowedUser (userId, addedAt) VALUES (?, datetime('now'))`,
	)
	if err != nil {
		return fmt.Errorf("set telegram allowlist: prepare: %w", err)
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return fmt.Errorf("set telegram allowlist: insert %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set telegram allowlist: commit: %w", err)
	}
	return nil
}

// maskedTelegramToken builds the preview shown to the operator. It reveals the
// first four and last four runes so a token can be recognized without being
// usable, and reveals nothing when the token is too short to mask meaningfully.
func (r *Repo) maskedTelegramToken() string {
	token, err := r.TelegramToken()
	if err != nil || token == "" {
		return ""
	}
	runes := []rune(token)
	if len(runes) < 12 {
		return strings.Repeat("•", len(runes))
	}
	return string(runes[:4]) + "…" + string(runes[len(runes)-4:])
}
