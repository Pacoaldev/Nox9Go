package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSyncPiCatalogWithRetry_RetriesUntilTheReadsSucceed(t *testing.T) {
	var calls int
	sync := func(context.Context) (bool, error) {
		calls++
		if calls < 3 {
			return false, errors.New("database is locked")
		}
		return true, nil
	}
	if err := syncPiCatalogWithRetry(context.Background(), time.Second, time.Millisecond, sync); err != nil {
		t.Fatalf("syncPiCatalogWithRetry: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestSyncPiCatalogWithRetry_GivesUpWithTheReadFailure(t *testing.T) {
	var calls int
	failure := errors.New("database is locked")
	sync := func(context.Context) (bool, error) {
		calls++
		return false, failure
	}
	err := syncPiCatalogWithRetry(context.Background(), 20*time.Millisecond, 5*time.Millisecond, sync)
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the read failure", err)
	}
	if calls < 2 {
		t.Fatalf("calls = %d, want at least 2 attempts inside the budget", calls)
	}
}

func TestSyncPiCatalogWithRetry_StopsWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	sync := func(context.Context) (bool, error) {
		calls++
		cancel()
		return false, errors.New("database is locked")
	}
	if err := syncPiCatalogWithRetry(ctx, time.Minute, 5*time.Millisecond, sync); err == nil {
		t.Fatal("want the read failure, got nil")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1: a cancelled context must not retry", calls)
	}
}
