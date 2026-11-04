package resilience

import (
	"testing"
	"time"
)

func newTestBreaker(clock *time.Time) *Breaker {
	b := &Breaker{name: "p", opts: BreakerOptions{}.withDefaults(), state: StateClosed}
	b.now = func() time.Time { return *clock }
	return b
}

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	clock := time.Unix(1000, 0)
	b := newTestBreaker(&clock)
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	if b.State() != StateOpen {
		t.Fatalf("state = %s, want OPEN", b.State())
	}
	if b.Allow() {
		t.Fatal("open breaker admitted a request")
	}
}

func TestBreakerSuccessClearsFailureStreak(t *testing.T) {
	clock := time.Unix(1000, 0)
	b := newTestBreaker(&clock)
	for i := 0; i < 4; i++ {
		b.RecordFailure()
	}
	b.RecordSuccess()
	b.RecordFailure()
	if b.State() == StateOpen {
		t.Fatal("non-consecutive failures opened the breaker")
	}
}

func TestBreakerProbeRecoversAndBacksOff(t *testing.T) {
	clock := time.Unix(1000, 0)
	b := newTestBreaker(&clock)
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	clock = clock.Add(31 * time.Second)
	if !b.Allow() || b.State() != StateHalfOpen {
		t.Fatalf("after reset timeout want one probe in HALF_OPEN, got %s", b.State())
	}
	if b.Allow() {
		t.Fatal("second concurrent probe admitted")
	}
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Fatalf("state = %s, want CLOSED after probe success", b.State())
	}

	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	clock = clock.Add(31 * time.Second)
	b.Allow()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("failed probe state = %s, want OPEN", b.State())
	}
}

func TestBreakerAbandonedProbeDoesNotStall(t *testing.T) {
	clock := time.Unix(1000, 0)
	b := newTestBreaker(&clock)
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	clock = clock.Add(31 * time.Second)
	b.Allow() // probe admitted, never reports
	clock = clock.Add(31 * time.Second)
	b.Allow() // detects the stalled probe and reopens
	clock = clock.Add(31 * time.Second)
	if !b.Allow() {
		t.Fatal("breaker stalled in HALF_OPEN after an abandoned probe")
	}
}

func TestIsProviderFailureStatus(t *testing.T) {
	tests := map[int]bool{500: true, 502: true, 503: true, 504: true, 408: true, 429: false, 401: false, 400: false, 200: false}
	for status, want := range tests {
		if got := IsProviderFailureStatus(status); got != want {
			t.Errorf("IsProviderFailureStatus(%d) = %v, want %v", status, got, want)
		}
	}
}