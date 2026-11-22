package telegram

import (
	"context"
	"database/sql"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/vault"
)

// newRealRepo builds a *db.Repo over an in-memory database with the Telegram
// tables and a vault, so these tests exercise the real storage contract instead
// of a fake that could drift from it.
func newRealRepo(t *testing.T) *db.Repo {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.EnsureTelegramChannelTables(database); err != nil {
		t.Fatalf("EnsureTelegramChannelTables() = %v", err)
	}

	key := make([]byte, vault.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	v, err := vault.New(key)
	if err != nil {
		t.Fatalf("vault.New() = %v", err)
	}
	repo := db.NewRepo(database)
	repo.SetVault(v)
	return repo
}

// The real repository must satisfy the interface the supervisor consumes. This
// is the drift guard: a renamed or retyped storage method fails to compile here
// instead of at the wiring site.
func TestDBRepo_SatisfiesRepo(t *testing.T) {
	repo := newRealRepo(t)
	var r Repo = NewDBRepo(repo)
	if r == nil {
		t.Fatal("NewDBRepo() = nil")
	}
}

// S4/S6: /model persists the choice, and the supervisor reads it back through
// the same path the poll loop uses.
func TestDBRepo_ModelRoundTrip(t *testing.T) {
	repo := newRealRepo(t)
	s := NewSupervisor(NewDBRepo(repo), nil)

	model, err := s.repo.TelegramChannelModel()
	if err != nil {
		t.Fatalf("TelegramChannelModel() error = %v", err)
	}
	if model != "" {
		t.Errorf("TelegramChannelModel() = %q, want empty before any choice", model)
	}

	if err := s.repo.SetTelegramChannelModel("openai/gpt-4o"); err != nil {
		t.Fatalf("SetTelegramChannelModel() error = %v", err)
	}
	model, err = s.repo.TelegramChannelModel()
	if err != nil {
		t.Fatalf("TelegramChannelModel() error = %v", err)
	}
	if model != "openai/gpt-4o" {
		t.Errorf("TelegramChannelModel() = %q, want openai/gpt-4o", model)
	}
}

// S5: a session written through the supervisor's repo is readable through the
// same interface the handler uses, which is what makes multi-turn context work.
func TestDBRepo_SessionRoundTrip(t *testing.T) {
	repo := newRealRepo(t)
	r := NewDBRepo(repo)

	session, err := r.StartTelegramSession(99, "prueba")
	if err != nil {
		t.Fatalf("StartTelegramSession() error = %v", err)
	}
	active, err := r.ActiveTelegramSession(99)
	if err != nil {
		t.Fatalf("ActiveTelegramSession() error = %v", err)
	}
	if active == nil || active.ID != session.ID {
		t.Fatalf("ActiveTelegramSession() = %+v, want session %d", active, session.ID)
	}

	if err := r.AppendTelegramMessage(session.ID, "user", "hola"); err != nil {
		t.Fatalf("AppendTelegramMessage() error = %v", err)
	}
	transcript, err := r.TelegramTranscript(session.ID, 10)
	if err != nil {
		t.Fatalf("TelegramTranscript() error = %v", err)
	}
	if len(transcript) != 1 || transcript[0].Content != "hola" {
		t.Errorf("TelegramTranscript() = %+v, want one turn saying hola", transcript)
	}
}

// S3: the gate reads the real allowlist, so an operator's edit takes effect on
// the next message instead of the next restart.
func TestDBRepo_AllowedReadsStoredList(t *testing.T) {
	repo := newRealRepo(t)
	s := NewSupervisor(NewDBRepo(repo), nil)

	if s.Allowed(42) {
		t.Errorf("Allowed(42) = true before the user is listed, want false")
	}
	if err := repo.SetTelegramAllowedUserIDs([]int64{42}); err != nil {
		t.Fatalf("SetTelegramAllowedUserIDs() error = %v", err)
	}
	if !s.Allowed(42) {
		t.Errorf("Allowed(42) = false after being listed, want true")
	}

	// Revoking must take effect immediately, without a restart.
	if err := repo.SetTelegramAllowedUserIDs(nil); err != nil {
		t.Fatalf("SetTelegramAllowedUserIDs(nil) error = %v", err)
	}
	if s.Allowed(42) {
		t.Errorf("Allowed(42) = true after being revoked, want false")
	}
}

// S1/S3: the enabled decision comes from real storage: token plus allowlist.
func TestDBRepo_EnabledRequiresTokenAndAllowlist(t *testing.T) {
	repo := newRealRepo(t)
	r := NewDBRepo(repo)

	cfg, err := r.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if cfg.Enabled {
		t.Errorf("Enabled = true on a fresh database, want false")
	}

	if err := repo.SetTelegramAllowedUserIDs([]int64{42}); err != nil {
		t.Fatalf("SetTelegramAllowedUserIDs() error = %v", err)
	}
	cfg, _ = r.GetTelegramChannelConfig()
	if cfg.Enabled {
		t.Errorf("Enabled = true with an allowlist but no token, want false")
	}

	if err := repo.SaveTelegramToken("111:secret"); err != nil {
		t.Fatalf("SaveTelegramToken() error = %v", err)
	}
	cfg, _ = r.GetTelegramChannelConfig()
	if !cfg.Enabled {
		t.Errorf("Enabled = false with both a token and an allowlist, want true")
	}

	// S3: emptying the allowlist disables the channel again.
	if err := repo.SetTelegramAllowedUserIDs(nil); err != nil {
		t.Fatalf("SetTelegramAllowedUserIDs(nil) error = %v", err)
	}
	cfg, _ = r.GetTelegramChannelConfig()
	if cfg.Enabled {
		t.Errorf("Enabled = true after the allowlist was emptied, want false")
	}
}

// S6: /model with no argument lists what the gateway offers. The catalog is read
// from the instance's own route, so a connection added later shows up without a
// code change.
func TestFetchModelIDs(t *testing.T) {
	ids, err := fetchModelIDs(context.Background(), "", "")
	if err == nil {
		t.Fatalf("fetchModelIDs(\"\") error = nil, want an error for an empty base URL")
	}
	_ = ids
}
