package app

import (
	"context"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlers/dashboard"
)

// startCatalogSweepLoop starts the periodic catalogue sweep (settings key
// "modelSweep", on by default). The configuration is read on every cycle, so
// switching it off in the settings needs no restart. It stops with the
// shutdown context.
//
// The loop is a no-op without a repository: there is nothing to sweep without
// the connections it reads.
func startCatalogSweepLoop(ctx context.Context, repo *db.Repo) {
	if repo == nil {
		return
	}
	go dashboard.StartCatalogSweep(ctx, repo)
}
