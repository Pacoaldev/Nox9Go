package quotalearn

import (
	"net/http"
	"testing"
	"time"
)

func TestLearner429WithNoWindowDoesNotParkConnection(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	l := New(Options{})
	l.now = func() time.Time { return now }

	// 429 without any Retry-After window: the combo loop owns that decision
	// (bounded second pass + DB locks), so the learner must not park the
	// connection with an invented cooldown.
	l.RecordOutcome("p", "m", "c", 429, nil, 100)

	if blocked, reason := l.IsBlocked("p", "m", "c"); blocked {
		t.Fatalf("expected not blocked after windowless 429, got reason=%s", reason)
	}
}

func TestLearnerHonorsExplicitRetryAfterHeader(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	l := New(Options{})
	l.now = func() time.Time { return now }

	hdr := make(http.Header)
	hdr.Set("Retry-After", "30")
	l.RecordOutcome("p", "m", "c", 429, hdr, 100)

	if blocked, reason := l.IsBlocked("p", "m", "c"); !blocked {
		t.Fatalf("expected blocked by explicit Retry-After, got reason=%s", reason)
	}
	// Another connection on the same provider/model is NOT blocked.
	if blocked, _ := l.IsBlocked("p", "m", "c2"); blocked {
		t.Fatalf("c2 should not be blocked")
	}
	// After 31 seconds the cooldown expires.
	now = now.Add(31 * time.Second)
	if blocked, reason := l.IsBlocked("p", "m", "c"); blocked {
		t.Fatalf("expected unblocked after cooldown, still blocked: %s", reason)
	}
}

func TestLearnerLearnsCeilingFromHeaders(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	l := New(Options{})
	l.now = func() time.Time { return now }

	// Upstream reports limit 5 RPM via headers.
	hdr := make(http.Header)
	hdr.Set("X-RateLimit-Limit-Requests", "5")
	l.RecordOutcome("groq", "mixtral", "conn-1", 200, hdr, 50)

	// Fire 4 more requests in the same minute -> total 5.
	for i := 0; i < 4; i++ {
		l.RecordOutcome("groq", "mixtral", "conn-1", 200, nil, 50)
	}

	// Next check within that minute must be blocked by RPM.
	if blocked, reason := l.IsBlocked("groq", "mixtral", "conn-1"); !blocked {
		t.Fatalf("expected blocked by learned RPM ceiling, got reason=%s", reason)
	}

	// Moving 61s into the future clears the sliding minute window.
	now = now.Add(61 * time.Second)
	if blocked, reason := l.IsBlocked("groq", "mixtral", "conn-1"); blocked {
		t.Fatalf("expected unblocked after minute rolled over, still blocked: %s", reason)
	}
}

func TestLearnerTPMCeiling(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	l := New(Options{})
	l.now = func() time.Time { return now }

	hdr := make(http.Header)
	hdr.Set("X-RateLimit-Limit-Tokens", "1000")
	l.RecordOutcome("cerebras", "llama3.1-8b", "conn-1", 200, hdr, 600)

	if blocked, _ := l.IsBlocked("cerebras", "llama3.1-8b", "conn-1"); blocked {
		t.Fatalf("expected healthy at 600/1000 tokens")
	}

	// Another 500 tokens pushes it to 1100 > 1000.
	l.RecordOutcome("cerebras", "llama3.1-8b", "conn-1", 200, nil, 500)
	if blocked, reason := l.IsBlocked("cerebras", "llama3.1-8b", "conn-1"); !blocked {
		t.Fatalf("expected blocked by TPM, got reason=%s", reason)
	}
}
