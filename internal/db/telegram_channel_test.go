package db

import (
	"database/sql"
	"testing"

	"nox9go/proxy/internal/vault"
)

// openTelegramTestDB returns a database with only the Telegram channel tables,
// so these tests exercise the storage contract and not the whole core schema.
func openTelegramTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory database: %v", err)
	}
	// In-memory SQLite belongs to the connection that created it: a second
	// pooled connection would query an empty database and report "no such
	// table" at random.
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if err := EnsureTelegramChannelTables(database); err != nil {
		t.Fatalf("EnsureTelegramChannelTables() = %v", err)
	}
	return database
}

// sealedVault builds a vault with a fixed master key so a token written in one
// step can be read back in another.
func sealedVault(t *testing.T) *vault.Vault {
	t.Helper()
	key := make([]byte, vault.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	v, err := vault.New(key)
	if err != nil {
		t.Fatalf("vault.New() = %v", err)
	}
	return v
}

// S1: the channel ships disabled. A fresh database reports a zero config, so no
// poll loop starts and the gateway behaves exactly as it does without the feature.
func TestTelegramChannelConfig_DefaultsToDisabled(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if cfg.Enabled {
		t.Errorf("Enabled = true, want false on a fresh database")
	}
	if cfg.TokenPresent {
		t.Errorf("TokenPresent = true, want false on a fresh database")
	}
	if len(cfg.AllowedUserIDs) != 0 {
		t.Errorf("AllowedUserIDs = %v, want empty", cfg.AllowedUserIDs)
	}
}

// S2: the token is sealed at rest. The stored bytes must not contain the
// plaintext, and a read with the same vault must return it unchanged.
func TestTelegramChannelConfig_SealsTokenAtRest(t *testing.T) {
	database := openTelegramTestDB(t)
	repo := NewRepo(database)
	repo.SetVault(sealedVault(t))

	const token = "7712345678:AAH-test-token-value-never-in-cleartext"
	if err := repo.SaveTelegramToken(token); err != nil {
		t.Fatalf("SaveTelegramToken() error = %v", err)
	}

	var wrapped, ciphertext string
	err := database.QueryRow(
		`SELECT tokenWrappedDEK, tokenCiphertext FROM telegramChannel WHERE id = 1`,
	).Scan(&wrapped, &ciphertext)
	if err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	if wrapped == "" || ciphertext == "" {
		t.Fatalf("stored sealed pair = (%q, %q), want both non-empty", wrapped, ciphertext)
	}
	if contains(ciphertext, token) || contains(wrapped, token) {
		t.Fatalf("plaintext token found in stored columns; sealing did not happen")
	}

	got, err := repo.TelegramToken()
	if err != nil {
		t.Fatalf("TelegramToken() error = %v", err)
	}
	if got != token {
		t.Errorf("TelegramToken() = %q, want %q", got, token)
	}
}

// S2: no response ever carries the cleartext token back. The config read model
// exposes presence, never the value.
func TestTelegramChannelConfig_MasksTokenInReadModel(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))
	repo.SetVault(sealedVault(t))

	const token = "7712345678:AAH-another-secret-value"
	if err := repo.SaveTelegramToken(token); err != nil {
		t.Fatalf("SaveTelegramToken() error = %v", err)
	}

	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if !cfg.TokenPresent {
		t.Errorf("TokenPresent = false, want true after a token was stored")
	}
	if cfg.TokenMasked == "" {
		t.Errorf("TokenMasked = empty, want a masked preview")
	}
	if cfg.TokenMasked == token || contains(cfg.TokenMasked, token) {
		t.Errorf("TokenMasked = %q, want a mask that does not carry the token", cfg.TokenMasked)
	}
}

// S2: rotating the stored token must not leave the previous secret readable.
func TestTelegramChannelConfig_ReplaceToken(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))
	repo.SetVault(sealedVault(t))

	if err := repo.SaveTelegramToken("111:first-secret"); err != nil {
		t.Fatalf("SaveTelegramToken(first) error = %v", err)
	}
	if err := repo.SaveTelegramToken("222:second-secret"); err != nil {
		t.Fatalf("SaveTelegramToken(second) error = %v", err)
	}

	got, err := repo.TelegramToken()
	if err != nil {
		t.Fatalf("TelegramToken() error = %v", err)
	}
	if got != "222:second-secret" {
		t.Errorf("TelegramToken() = %q, want the replaced token", got)
	}
}

// S1: clearing the token disables the channel. A stale allowlist without a
// token must still not start a poll loop.
func TestTelegramChannelConfig_ClearToken(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))
	repo.SetVault(sealedVault(t))

	if err := repo.SaveTelegramToken("111:secret"); err != nil {
		t.Fatalf("SaveTelegramToken() error = %v", err)
	}
	if err := repo.ClearTelegramToken(); err != nil {
		t.Fatalf("ClearTelegramToken() error = %v", err)
	}

	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if cfg.TokenPresent {
		t.Errorf("TokenPresent = true, want false after ClearTelegramToken()")
	}

	got, err := repo.TelegramToken()
	if err != nil {
		t.Fatalf("TelegramToken() error = %v", err)
	}
	if got != "" {
		t.Errorf("TelegramToken() = %q, want empty", got)
	}
}

// S3: the allowlist is a set of Telegram numeric user IDs. Writes replace the
// whole set, and the read model never reports a token as usable while the
// allowlist is empty — that pairing is what keeps the gate fail-closed.
func TestTelegramChannelConfig_AllowlistReplace(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	if err := repo.SetTelegramAllowedUserIDs([]int64{42, 7, 9001}); err != nil {
		t.Fatalf("SetTelegramAllowedUserIDs() error = %v", err)
	}
	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if len(cfg.AllowedUserIDs) != 3 {
		t.Fatalf("AllowedUserIDs = %v, want 3 entries", cfg.AllowedUserIDs)
	}

	// Replacing with a smaller set must drop the ones left out, not merge.
	if err := repo.SetTelegramAllowedUserIDs([]int64{7}); err != nil {
		t.Fatalf("SetTelegramAllowedUserIDs() error = %v", err)
	}
	cfg, err = repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if len(cfg.AllowedUserIDs) != 1 || cfg.AllowedUserIDs[0] != 7 {
		t.Errorf("AllowedUserIDs = %v, want [7]", cfg.AllowedUserIDs)
	}
}

// S3: an empty allowlist admits nobody. This is the fail-closed contract, so it
// is asserted directly rather than inferred from the message handler.
func TestTelegramChannelConfig_EmptyAllowlistAdmitsNobody(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))
	repo.SetVault(sealedVault(t))
	if err := repo.SaveTelegramToken("111:secret"); err != nil {
		t.Fatalf("SaveTelegramToken() error = %v", err)
	}

	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if cfg.Enabled {
		t.Errorf("Enabled = true with an empty allowlist, want false: the gate must fail closed")
	}
}

func contains(haystack, needle string) bool {
	if needle == "" || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// S11: the pause switch persists as its own column. A fresh database reads
// unpaused, SetTelegramPaused round-trips both directions, and pausing leaves
// the token decision untouched so unpausing restores the channel as it was.
func TestTelegramChannelConfig_PausedRoundTrip(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if cfg.Paused {
		t.Errorf("Paused = true, want false on a fresh database")
	}

	if err := repo.SetTelegramPaused(true); err != nil {
		t.Fatalf("SetTelegramPaused(true) error = %v", err)
	}
	cfg, err = repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if !cfg.Paused {
		t.Errorf("Paused = false after SetTelegramPaused(true), want true")
	}

	// Pausing must not disturb the other settings on the row.
	if err := repo.SetTelegramMode(ModeAgent); err != nil {
		t.Fatalf("SetTelegramMode() error = %v", err)
	}
	if err := repo.SetTelegramPaused(false); err != nil {
		t.Fatalf("SetTelegramPaused(false) error = %v", err)
	}
	cfg, err = repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if cfg.Paused {
		t.Errorf("Paused = true after SetTelegramPaused(false), want false")
	}
	mode, err := repo.TelegramMode()
	if err != nil {
		t.Fatalf("TelegramMode() error = %v", err)
	}
	if mode != ModeAgent {
		t.Errorf("TelegramMode() = %q, want %q: pausing must not touch the row's other columns", mode, ModeAgent)
	}
}
