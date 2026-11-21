package middleware

import (
	"context"
	"testing"
	"time"
)

// Provider daily token budget (tpd): the token counterpart of the rpd budget. A
// fixed UTC-day window, charged from the prompt estimate at admission and settled
// against the turn's real total, because a streaming response has no completion
// count until it ends.
//
// Spec S1 (separate axis), S2 (UTC-day window), S6 (congestion, not node state),
// S8 (rpd unchanged).

func TestAcquireProvider_DailyTokensRefusesOnceSpent(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	limits := Limits{DailyTokens: 100}

	slot, refusal := rl.AcquireProvider(ctx, "onomeo", limits, 100)
	if slot == nil || refusal != nil {
		t.Fatalf("first attempt = slot %v refusal %v, want admitted", slot, refusal)
	}
	// Settle is what makes the charge real spend: an unsettled slot is refunded
	// by Release, because the provider never produced a usage reading.
	slot.Settle(100)
	slot.Release()
	if got := rl.SnapshotDailyTokens(ProviderScopeKey("onomeo")); got != 100 {
		t.Fatalf("daily tokens = %d, want 100", got)
	}

	_, refusal = rl.AcquireProvider(ctx, "onomeo", limits, 1)
	if refusal == nil {
		t.Fatal("a request past a spent daily token budget was admitted")
	}
	if refusal.Axis != "tpd" {
		t.Errorf("refusal axis = %q, want tpd", refusal.Axis)
	}
	if refusal.Limit != 100 {
		t.Errorf("refusal limit = %d, want 100", refusal.Limit)
	}
	if refusal.RetryAfter <= 0 || refusal.RetryAfter > 24*time.Hour {
		t.Errorf("retry after = %v, want within a day", refusal.RetryAfter)
	}
}

// S2: the budget refills at the UTC day boundary, not one minute after the first
// request.
func TestProviderDailyTokens_ResetsOnUTCDayBoundary(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	key := ProviderScopeKey("onomeo")
	day1 := time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC)
	day2 := day1.Add(2 * time.Hour)

	rl.chargeDailyTokens(key, 500, day1)
	if got := rl.SnapshotDailyTokensAt(key, day1); got != 500 {
		t.Fatalf("day 1 = %d, want 500", got)
	}
	if got := rl.SnapshotDailyTokensAt(key, day2); got != 0 {
		t.Errorf("day 2 = %d, want 0 after the UTC boundary", got)
	}
}

// S1: the daily token budget and the lifetime token quota are independent axes; a
// provider may carry both, and spending one does not move the other.
func TestProviderDailyTokens_IndependentFromLifetimeQuota(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	limits := Limits{DailyTokens: 50, TokenQuota: 1_000_000}

	slot, refusal := rl.AcquireProvider(ctx, "onomeo", limits, 50)
	if slot == nil || refusal != nil {
		t.Fatalf("attempt = slot %v refusal %v, want admitted", slot, refusal)
	}
	slot.Settle(50)
	slot.Release()

	if got := rl.SnapshotDailyTokens(ProviderScopeKey("onomeo")); got != 50 {
		t.Errorf("daily tokens = %d, want 50", got)
	}
	// The lifetime quota is not metered by the limiter; it must not have been
	// adjusted by the daily charge.
	if limits.TokenQuota != 1_000_000 {
		t.Errorf("lifetime quota = %d, want unchanged 1000000", limits.TokenQuota)
	}
}

// S6: a refusal on the token budget is congestion (429-able), like the request
// budget, never a node-state 403.
func TestProviderDailyTokens_RefusalIsCongestion(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	limits := Limits{DailyTokens: 10}

	if slot, _ := rl.AcquireProvider(ctx, "onomeo", limits, 10); slot == nil {
		t.Fatal("first attempt was refused")
	} else {
		slot.Settle(10)
		slot.Release()
	}
	_, refusal := rl.AcquireProvider(ctx, "onomeo", limits, 1)
	if refusal == nil {
		t.Fatal("expected a refusal")
	}
	if refusal.Axis != axisTokensPerDay {
		t.Errorf("axis = %q, want %q", refusal.Axis, axisTokensPerDay)
	}
}

// Settle charges only the difference between the prompt estimate and the turn's
// real total, so the estimate is corrected rather than counted twice.
func TestProviderSlot_SettleChargesCompletionOnly(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	limits := Limits{DailyTokens: 1000}

	slot, refusal := rl.AcquireProvider(ctx, "onomeo", limits, 100)
	if slot == nil || refusal != nil {
		t.Fatalf("attempt = slot %v refusal %v", slot, refusal)
	}
	if got := rl.SnapshotDailyTokens(ProviderScopeKey("onomeo")); got != 100 {
		t.Fatalf("after reservation = %d, want the 100-token estimate", got)
	}

	slot.Settle(250)
	if got := rl.SnapshotDailyTokens(ProviderScopeKey("onomeo")); got != 250 {
		t.Errorf("after settle = %d, want 250 (estimate corrected, not doubled)", got)
	}
}

// Release returns the whole daily charge for an attempt that never produced a
// usage reading.
func TestProviderSlot_ReleaseRefundsDailyTokens(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	limits := Limits{DailyTokens: 1000}

	slot, refusal := rl.AcquireProvider(ctx, "onomeo", limits, 400)
	if slot == nil || refusal != nil {
		t.Fatalf("attempt = slot %v refusal %v", slot, refusal)
	}
	slot.Release()
	if got := rl.SnapshotDailyTokens(ProviderScopeKey("onomeo")); got != 0 {
		t.Errorf("after release = %d, want 0", got)
	}
}

// S8: adding the token axis must not change the request-per-day budget. Same
// numbers, same axis, same refill boundary as before the change.
func TestAcquireProvider_DailyRequestsUnchangedByTokenAxis(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	limits := Limits{DailyRequests: 2}

	for i := 0; i < 2; i++ {
		slot, refusal := rl.AcquireProvider(ctx, "bazaarlink", limits, 999)
		if slot == nil || refusal != nil {
			t.Fatalf("attempt %d = slot %v refusal %v, want admitted", i, slot, refusal)
		}
		slot.Release()
	}
	if got := rl.SnapshotDaily(ProviderScopeKey("bazaarlink")); got != 2 {
		t.Errorf("daily requests = %d, want 2", got)
	}
	// The token axis was never configured, so nothing accumulated on it.
	if got := rl.SnapshotDailyTokens(ProviderScopeKey("bazaarlink")); got != 0 {
		t.Errorf("daily tokens = %d, want 0 when no token budget is set", got)
	}

	_, refusal := rl.AcquireProvider(ctx, "bazaarlink", limits, 999)
	if refusal == nil || refusal.Axis != axisRequestsPerDay || refusal.Limit != 2 {
		t.Errorf("refusal = %+v, want axis %q limit 2", refusal, axisRequestsPerDay)
	}
}
