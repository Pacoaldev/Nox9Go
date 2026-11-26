package app

import (
	"context"
	"fmt"
	"time"
)

// piCatalogSyncBudget bounds how long startup waits for the catalogue reads to
// succeed, and piCatalogSyncRetryDelay separates the attempts.
const (
	piCatalogSyncBudget     = 30 * time.Second
	piCatalogSyncRetryDelay = 500 * time.Millisecond
)

// syncPiCatalogWithRetry publishes the Pi mirror once every catalogue read
// succeeds, retrying within the budget instead of publishing whatever a
// half-started gateway can see.
//
// The failure this exists for: the startup hook fired the sync concurrently
// with the loops that populate the catalogue, the reads failed, and the sync
// wrote a short list over the operator's models.json — valid JSON, no error
// anywhere, and no combo or model left in Pi. A failed attempt publishes
// nothing, so a sync that never gets a clean read leaves the previous file
// alone and only reports the reason.
func syncPiCatalogWithRetry(
	ctx context.Context,
	budget time.Duration,
	delay time.Duration,
	sync func(context.Context) (bool, error),
) error {
	if sync == nil {
		return fmt.Errorf("pi catalogue sync: no synchronizer")
	}
	deadline := time.Now().Add(budget)
	for {
		_, err := sync(ctx)
		if err == nil {
			return nil
		}
		if time.Now().Add(delay).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay):
		}
	}
}
