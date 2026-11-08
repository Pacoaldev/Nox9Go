package middleware

import (
	"context"
	"testing"
	"time"
)

// Provider daily request budget (rpd): a fixed UTC-day window, distinct from the
// sliding RPM window. These tests pin the three properties nothing else proves:
// the budget refuses once spent, it refills on the UTC day boundary, and a request
// the provider never saw does not spend it.

func TestAcquireProvider_DailyBudgetRefusesOnceSpent(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	limits := Limits{DailyRequests: 2}

	for i := 0; i < 2; i++ {
		slot, refusal := rl.AcquireProvider(ctx, "bazaarlink", limits, 0)
		if slot == nil || refusal != nil {
			t.Fatalf("attempt %d = slot %v refusal %v, want admitted", i, slot, refusal)
		}
		slot.Release()
	}
	if got := rl.SnapshotDaily(ProviderScopeKey("bazaarlink")); got != 2 {
		t.Fatalf("daily used = %d, want 2", got)
	}

	_, refusal := rl.AcquireProvider(ctx, "bazaarlink", limits, 0)
	if refusal == nil {
		t.Fatal("third request was admitted past a daily budget of 2")
	}
	if refusal.Axis != "rpd" {
		t.Errorf("refusal axis = %q, want rpd", refusal.Axis)
	}
	if refusal.Limit != 2 {
		t.Errorf("refusal limit = %d, want 2", refusal.Limit)
	}
	if refusal.RetryAfter <= 0 || refusal.RetryAfter > 24*time.Hour {
		t.Errorf("refusal RetryAfter = %v, want (0, 24h]", refusal.RetryAfter)
	}
}

func TestReserveDaily_RefillsOnTheUTCDayBoundary(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	key := ProviderScopeKey("bazaarlink")
	day1 := time.Date(2026, 11, 8, 23, 59, 0, 0, time.UTC)

	ok, _ := rl.reserveDaily(key, 1, day1)
	if !ok {
		t.Fatal("first request on day 1 was refused")
	}
	if ok, _ := rl.reserveDaily(key, 1, day1); ok {
		t.Fatal("second request on the same day was admitted past a budget of 1")
	}
	// A minute later is the next UTC day: the budget refills.
	day2 := day1.Add(2 * time.Minute)
	if ok, _ := rl.reserveDaily(key, 1, day2); !ok {
		t.Fatal("the budget did not refill on the UTC day boundary")
	}
	if got := rl.SnapshotDailyAt(key, day2); got != 1 {
		t.Errorf("daily used after rollover = %d, want 1", got)
	}
}

func TestAcquireProvider_QueueTimeoutRefundsTheDailyCharge(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	ctx := context.Background()
	// One concurrency slot held, a daily budget with room: the second request
	// reserves a daily slot, then fails to get the concurrency slot and must hand
	// the daily reservation back — the provider never saw it.
	limits := Limits{DailyRequests: 5, Concurrency: 1, QueueTimeout: 25 * time.Millisecond}

	held, refusal := rl.AcquireProvider(ctx, "bazaarlink", limits, 0)
	if held == nil || refusal != nil {
		t.Fatalf("first = slot %v refusal %v, want admitted", held, refusal)
	}
	defer held.Release()

	_, refusal = rl.AcquireProvider(ctx, "bazaarlink", limits, 0)
	if refusal == nil {
		t.Fatal("second request was admitted while the concurrency slot was held")
	}
	if got := rl.SnapshotDaily(ProviderScopeKey("bazaarlink")); got != 1 {
		t.Errorf("daily used after a queue timeout = %d, want 1 (the failed attempt refunded)", got)
	}
}

func TestAcquireProvider_UnlimitedDailyIsNotTracked(t *testing.T) {
	rl := NewRateLimiter(time.Minute)
	slot, refusal := rl.AcquireProvider(context.Background(), "bazaarlink", Limits{}, 0)
	if slot != nil || refusal != nil {
		t.Fatalf("no limits = slot %v refusal %v, want nil/nil", slot, refusal)
	}
	if got := rl.SnapshotDaily(ProviderScopeKey("bazaarlink")); got != 0 {
		t.Errorf("daily used = %d, want 0 for an untracked provider", got)
	}
}

func TestTimeUntilUTCMidnight(t *testing.T) {
	at := time.Date(2026, 11, 8, 23, 0, 0, 0, time.UTC)
	if got := timeUntilUTCMidnight(at); got != time.Hour {
		t.Errorf("timeUntilUTCMidnight = %v, want 1h", got)
	}
	if got := timeUntilUTCMidnight(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)); got != 24*time.Hour {
		t.Errorf("timeUntilUTCMidnight at midnight = %v, want 24h", got)
	}
}
