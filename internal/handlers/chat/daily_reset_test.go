package chat

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// openRouterDailyBody is the exact 429 OpenRouter answers an exhausted
// free-models-per-day window with: the reset rides as an epoch-milliseconds
// X-RateLimit-Reset inside error.metadata.headers, not in a Retry-After or a
// field the old parser read.
func openRouterDailyBody(resetEpochMS string) string {
	return `{"error":{"message":"Rate limit exceeded: free-models-per-day. Add 10 credits to unlock 1000 free model requests per day: https://openrouter.ai/settings/credits?utm_source=api&utm_campaign=free_daily_limit","code":429,"metadata":{"headers":{"X-RateLimit-Limit":"50","X-RateLimit-Remaining":"0","X-RateLimit-Reset":"` + resetEpochMS + `","limit_source":"openrouter_free_tier_daily","remedy_hint":"Wait for the daily reset (see X-RateLimit-Reset), or purchase credits to raise your free-model daily limit.","provider_name":"OpenRouter"}}}}`
}

// openCodeDailyBody is the shape the OpenCode free-tier gate answers an
// exhausted usage window with, sometimes carrying retry-after-ms in headers.
func openCodeDailyBody() string {
	return `{"error":{"type":"FreeUsageLimitError","message":"FreeUsageLimitError: rate limit exceeded for today. They reset at 21:00."}}`
}

// resetsAtClockBody names the reset as a wall-clock time, the form OpenCode's
// session notice uses ("They reset at 21:00").
func resetsAtClockBody(hh, mm int) string {
	return `{"error":{"type":"FreeUsageLimitError","message":"Daily limit reached. They reset at ` + two(hh) + `:` + two(mm) + `."}}`
}

func headerWith(k, v string) http.Header {
	h := make(http.Header)
	h.Set(k, v)
	return h
}

func two(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Daily free-tier windows must park for the window the upstream announced, not
// for the classifier's minutes: OpenRouter's free-models-per-day 429 and
// OpenCode's FreeUsageLimitError both reset after roughly a day, and re-dialing
// a spent daily quota on the next request is exactly the lockdown the operator
// reported ("minutes" instead of the daily reset).
func TestRetryableCooldownSec_DailyLimitHonoursResetCarriers(t *testing.T) {
	now := time.Now().UTC()
	// X-RateLimit-Reset as epoch-milliseconds, the unit OpenRouter sends.
	resetMS := now.Add(7 * time.Hour).UnixMilli()

	tests := []struct {
		name   string
		header http.Header
		body   string
		// window is the range the cooldown must land in.
		wantMin int
		wantMax int
	}{
		{
			name:   "openrouter daily epoch-ms in body metadata.headers",
			header: nil,
			body:   openRouterDailyBody(itoa(int(resetMS))),
			wantMin: int(7*time.Hour-time.Minute) / int(time.Second),
			wantMax: int(7*time.Hour+time.Minute) / int(time.Second),
		},
		{
			name:   "openrouter daily epoch-ms in HTTP header",
			header: headerWith("X-RateLimit-Reset", itoa(int(resetMS))),
			body:   `{"error":{"message":"Rate limit exceeded: free-models-per-day"}}`,
			wantMin: int(7*time.Hour-time.Minute) / int(time.Second),
			wantMax: int(7*time.Hour+time.Minute) / int(time.Second),
		},
		{
			name:   "opencode retry-after-ms header",
			header: headerWith("retry-after-ms", "90000"),
			body:   `{"error":{"type":"FreeUsageLimitError","message":"FreeUsageLimitError: rate limit exceeded for today."}}`,
			wantMin: 89,
			wantMax: 92,
		},
		{
			name:   "opencode resets at wall clock",
			header: nil,
			body:   resetsAtClockBody(21, 0),
			wantMin: 1,
			wantMax: int((24*time.Hour+time.Minute) / time.Second),
		},
		{
			// The same window on a non-daily provider keeps the old 2h clamp:
			// a hostile 72h retryinfo stays capped, only daily markers widen it.
			name:   "non-daily 429 keeps the 2h clamp",
			header: nil,
			body:   `{"error":{"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"72h"}]}}`,
			wantMin: int(maxResetCooldown / time.Second),
			wantMax: int(maxResetCooldown / time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ue := &upstreamError{
				StatusCode: http.StatusTooManyRequests,
				Body:       []byte(tt.body),
				Header:     tt.header,
			}
			got := retryableCooldownSec("openrouter", http.StatusTooManyRequests, 2*time.Second, ue)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("retryableCooldownSec = %ds, want between %d and %d (body %.120s)", got, tt.wantMin, tt.wantMax, tt.body)
			}
		})
	}
}

// A daily-limit 429 that announces nothing must not fall back to the backoff
// ladder's minutes: the operator reported a "minutes lockdown" for a window
// that clearly resets daily. With no usable carrier the honest answer is a
// full day, bounded by the daily cap.
func TestRetryableCooldownSec_DailyLimitWithoutResetFallsBackToDay(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"openrouter prose only", `{"error":{"message":"Rate limit exceeded: free-models-per-day"}}`},
		{"opencode prose only", `{"error":{"type":"FreeUsageLimitError","message":"Rate limit exceeded. Please try again later."}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ue := &upstreamError{
				StatusCode: http.StatusTooManyRequests,
				Body:       []byte(tt.body),
			}
			got := retryableCooldownSec("openrouter", http.StatusTooManyRequests, 2*time.Second, ue)
			want := int(dailyLimitFallbackCooldown / time.Second)
			if got != want {
				t.Errorf("retryableCooldownSec = %ds, want %ds (body %.100s)", got, want, tt.body)
			}
		})
	}
}

// retryAfterWait must surface every carrier of a daily-limit reset so
// providerModelLockoutUntil and comboLockRetryable inherit the same window.
func TestRetryAfterWait_DailyResetCarriers(t *testing.T) {
	now := time.Now().UTC()
	resetMS := now.Add(3 * time.Hour).UnixMilli()

	tests := []struct {
		name   string
		header http.Header
		body   string
		wantOK bool
	}{
		{"epoch-ms in body metadata.headers", nil, openRouterDailyBody(itoa(int(resetMS))), true},
		{"epoch-ms in header", headerWith("X-RateLimit-Reset", itoa(int(resetMS))), `{}`, true},
		{"epoch-seconds in header", headerWith("X-RateLimit-Reset", itoa(int(now.Add(2*time.Hour).Unix()))), `{}`, true},
		{"retry-after-ms in header", headerWith("retry-after-ms", "45000"), `{}`, true},
		{"resets at clock in body", nil, resetsAtClockBody(21, 30), true},
		{"no carrier", nil, `{"error":{"message":"rate limited"}}`, false},
		{"garbage epoch", headerWith("X-RateLimit-Reset", "soon"), `{}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ue := &upstreamError{StatusCode: http.StatusTooManyRequests, Body: []byte(tt.body), Header: tt.header}
			got, ok := retryAfterWait(ue)
			if ok != tt.wantOK {
				t.Fatalf("retryAfterWait ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got <= 0 {
				t.Errorf("retryAfterWait = %v, want a positive window", got)
			}
		})
	}
}

// A daily-limit 429 must announce its real window to the client too: the
// OpenCode closure previously pinned Retry-After to a fixed 60s even for a
// FreeUsageLimitError that resets daily.
func TestOpencodeClosureError_DailyLimitRetryAfter(t *testing.T) {
	withSignal := opencodeClosureError("opencode", "nemotron-3-free", &upstreamError{
		StatusCode: http.StatusForbidden,
		Body:       []byte(`{"error":{"type":"FreeTierError","message":"FreeUsageLimitError: rate limit exceeded for today"}}`),
		Header:     headerWith("retry-after-ms", "90000"),
	})
	var ue *upstreamError
	if !errors.As(withSignal, &ue) {
		t.Fatalf("expected *upstreamError, got %T", withSignal)
	}
	got := ue.Header.Get("Retry-After")
	if got == "" || got == "60" {
		t.Errorf("Retry-After = %q, want the announced daily window (not the fixed 60s)", got)
	}

	noSignal := opencodeClosureError("opencode", "nemotron-3-free", &upstreamError{
		StatusCode: http.StatusForbidden,
		Body:       []byte(`{"error":{"type":"FreeTierError","message":"FreeUsageLimitError: rate limit exceeded for today"}}`),
	})
	ue = nil
	if !errors.As(noSignal, &ue) {
		t.Fatalf("expected *upstreamError, got %T", noSignal)
	}
	if got := ue.Header.Get("Retry-After"); got != itoa(int(dailyLimitFallbackCooldown/time.Second)) {
		t.Errorf("Retry-After = %q, want the daily fallback %ds", got, int(dailyLimitFallbackCooldown/time.Second))
	}
}

