package app

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"go.uber.org/fx"

	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/handlers/dashboard"
	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/translator"
	"nox9go/proxy/internal/vault"
)

// DatabaseModule handles database initialization and provides *sql.DB and *db.Repo.
var DatabaseModule = fx.Module("database",
	fx.Provide(
		ProvideDatabase,
		ProvideRepo,
	),
	// An Invoke, not a Provide: the migration produces no value, it is a
	// startup side effect. As a Provide it would be rejected for returning
	// only an error.
	fx.Invoke(MigrateVault),
	// An Invoke for the same reason: it writes the settings row rather than
	// producing a value. Runs on every start and is a no-op once a real
	// password exists.
	fx.Invoke(EnsureDashboardPassword),
)

// ProvideDatabase initializes the global SQLite database and registers an OnStop lifecycle hook to close it cleanly.
func ProvideDatabase(lc fx.Lifecycle, cfg *config.Config) (*sql.DB, error) {
	if err := db.InitGlobalDatabase(cfg.DatabasePath); err != nil {
		return nil, fmt.Errorf("database init: %w", err)
	}

	conn, err := db.GetConnection()
	if err != nil {
		return nil, fmt.Errorf("database connect: %w", err)
	}

	// Upstream core schema bootstrap (fresh .9router): create the shared
	// tables/indexes when absent, backfill missing columns, and seed the
	// minimal rows. Idempotent — existing user data is never touched.
	// Best-effort like leases below: a shared test binary may hand us a
	// connection bound to a removed temp file (global singleton); a dead
	// connection is a test artifact, not a production schema failure.
	if err := db.EnsureCoreSchema(conn); err != nil {
		_, statErr := conn.Exec("SELECT 1")
		if statErr == nil {
			return nil, fmt.Errorf("database schema: %w", err)
		}
	}

	// Best-effort: a row stuck on a provider id the catalog dropped should
	// not block startup, it should just keep failing the way it already was.
	if err := db.MigrateLegacyProviderIDs(conn); err != nil {
		log.Warn("db", "legacy provider id migration failed", "error", err)
	}

	// Cross-process lease table for upstream coordination (Freebuff
	// sessions, future scopes). Idempotent: no-op when already present,
	// invisible to dashboards that do not know the table. Best-effort:
	// a shared test binary may hand us a connection bound to a removed
	// temp file (global singleton); leases then simply stay unavailable.
	if err := db.EnsureUpstreamLeases(conn); err != nil {
		_, statErr := conn.Exec("SELECT 1")
		if statErr == nil {
			return nil, fmt.Errorf("database leases: %w", err)
		}
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return conn.Close()
		},
	})

	return conn, nil
}

// ProvideRepo provides *db.Repo using the database connection.
// EnsureDashboardPassword gives a fresh install a generated dashboard password
// and prints it exactly once.
//
// The password used to be "123456" until someone changed it, which is a public
// password on every install that never got around to it. It is a best-effort
// startup step on purpose: a failure here must not stop the gateway, because
// the gateway's job is to serve, and an install with no dashboard password is
// still reachable by the local CLI token.
func EnsureDashboardPassword(repo *db.Repo, cfg *config.Config) {
	initialPassword := ""
	if cfg != nil {
		initialPassword = cfg.InitialPassword
	}
	password, err := dashboard.EnsureInitialPassword(repo, initialPassword)
	if err != nil {
		log.Warn("dashboard", "could not prepare the initial dashboard password", "error", err)
		return
	}
	if password == "" {
		return
	}
	// The port comes from the resolved config, not a literal: the advice used
	// to point at 20130 on an install started with PORT set to anything else.
	port := "20130"
	if cfg != nil && cfg.Port > 0 {
		port = strconv.Itoa(cfg.Port)
	}
	fmt.Print(dashboard.InitialPasswordNotice(password, port))
}

func ProvideRepo(conn *sql.DB) *db.Repo {
	repo := db.NewRepo(conn)
	repo.SetVault(vault.NewFromEnv())
	// G26: hydrate thought signatures from SQLite so a signature written before
	// a restart is still replayable on the next tool turn. Fail-open — a broken
	// database only leaves the store process-local, exactly as it was before.
	translator.SetThoughtSignaturePersistence(repo)
	return repo
}

// MigrateVault seals any credential still stored in plaintext.
//
// It is a separate provider so it runs after ProvideRepo — the Repo is what
// carries the vault — and before the server starts accepting traffic, so a
// connection is never dispatched with a half-migrated credential.
//
// Failure is not fatal. The migration takes its own backup first, and a row it
// cannot seal keeps working in plaintext, which is strictly better than a
// gateway that refuses to start.
func MigrateVault(repo *db.Repo, cfg *config.Config) error {
	ctx := context.Background()
	result, err := repo.MigratePlaintextCredentials(ctx, cfg.DatabasePath)
	if err != nil {
		log.Warn("vault", "plaintext credential migration skipped", "error", err)
		return nil
	}
	if result.Sealed > 0 || len(result.Skipped) > 0 {
		log.Info("vault", "credential migration complete", "sealed", result.Sealed, "skipped", len(result.Skipped))
	}
	return nil
}
