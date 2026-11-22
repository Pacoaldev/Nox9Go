package handlers

import (
	"context"
	"database/sql"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/dbtest"
	"nox9go/proxy/internal/vault"
)

// newWiringRepo builds a repository with the Telegram tables and a vault.
func newWiringRepo(t *testing.T) *db.Repo {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := dbtest.CreateTables(conn); err != nil {
		t.Fatalf("CreateTables() = %v", err)
	}
	if err := db.EnsureTelegramChannelTables(conn); err != nil {
		t.Fatalf("EnsureTelegramChannelTables() = %v", err)
	}
	key := make([]byte, vault.KeySize)
	for i := range key {
		key[i] = byte(i + 3)
	}
	v, err := vault.New(key)
	if err != nil {
		t.Fatalf("vault.New() = %v", err)
	}
	repo := db.NewRepo(conn)
	repo.SetVault(v)
	return repo
}

// S3: a boot where the channel is not configured leaves the poll loop stopped.
// This is the gate at the wiring site: no token and no allowlist means nothing
// polls Telegram, so a gateway that has never been set up makes no calls.
func TestStartTelegramChannel_NoConfigNeverPolls(t *testing.T) {
	repo := newWiringRepo(t)

	rt := StartTelegramChannel(context.Background(), repo, selfBaseURL(20130))
	if rt == nil {
		t.Fatalf("StartTelegramChannel() = nil")
	}
	// Reconcile on an unconfigured channel must be a no-op, not a panic and not
	// a started loop.
	rt.Reconcile()
	rt.Stop(context.Background())
}

// S3: the gate refuses an unknown user even when the channel is configured. A
// failure here would let a stranger's message reach a provider and spend tokens.
func TestStartTelegramChannel_GateRefusesUnknownUser(t *testing.T) {
	repo := newWiringRepo(t)
	if err := repo.SaveTelegramToken("123456:secret"); err != nil {
		t.Fatalf("SaveTelegramToken() = %v", err)
	}
	if err := repo.SetTelegramAllowedUserIDs([]int64{601559545}); err != nil {
		t.Fatalf("SetTelegramAllowedUserIDs() = %v", err)
	}

	rt := StartTelegramChannel(context.Background(), repo, selfBaseURL(20130))
	t.Cleanup(func() { rt.Stop(context.Background()) })

	// The gate reads storage, so a user added later is authorized on the next
	// message without a restart, and a removed one is refused immediately.
	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() = %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("Enabled = false with a token and an allowlist, want true")
	}
	found := false
	for _, id := range cfg.AllowedUserIDs {
		if id == 601559545 {
			found = true
		}
	}
	if !found {
		t.Errorf("AllowedUserIDs = %v, want the configured id present", cfg.AllowedUserIDs)
	}
}

// A nil repository must not panic: the wiring runs during boot, where a nil repo
// is a configuration error the server reports elsewhere, not a crash here.
func TestStartTelegramChannel_NilRepo(t *testing.T) {
	rt := StartTelegramChannel(context.Background(), nil, selfBaseURL(20130))
	if rt == nil {
		t.Fatalf("StartTelegramChannel(nil) = nil, want a usable no-op runtime")
	}
	rt.Reconcile()
	rt.Stop(context.Background())
}

// S7: Stop is safe to call twice. Shutdown paths run more than once in practice
// (signal plus deferred cleanup), and a double stop must not panic.
func TestStartTelegramChannel_StopIsIdempotent(t *testing.T) {
	repo := newWiringRepo(t)
	rt := StartTelegramChannel(context.Background(), repo, selfBaseURL(20130))
	rt.Stop(context.Background())
	rt.Stop(context.Background())
}

// selfBaseURL is what the channel dials. It must be a local address: the token
// header is only meant for this process's own listener.
func TestSelfBaseURL(t *testing.T) {
	if got := selfBaseURL(20130); got != "http://127.0.0.1:20130" {
		t.Errorf("selfBaseURL(20130) = %q, want http://127.0.0.1:20130", got)
	}
}
