package providers

import (
	"net/http"
	"testing"
)

func TestClassifyObservation_StatusFallback(t *testing.T) {
	tests := []struct {
		status int
		want   string
		ok     bool
	}{
		{http.StatusTooManyRequests, SignalRateLimit, true},
		{http.StatusPaymentRequired, SignalQuotaExceeded, true},
		{http.StatusUnauthorized, SignalAuth, true},
		{http.StatusForbidden, SignalAuth, true},
		{http.StatusBadGateway, SignalCapacity, true},
		{http.StatusServiceUnavailable, SignalCapacity, true},
		{http.StatusGatewayTimeout, SignalTimeout, true},
		{http.StatusInternalServerError, SignalServerError, true},
		{http.StatusNotFound, "", false},
		{http.StatusConflict, "", false},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			got, ok := ClassifyObservation(tt.status, nil, nil)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got.Signal != tt.want {
				t.Errorf("signal = %q, want %q", got.Signal, tt.want)
			}
		})
	}
}

func TestClassifyObservation_RequestShapeIsNotACapacityFact(t *testing.T) {
	// A context-window rejection and a bad-tool rejection say nothing about how
	// much the provider tolerates, so neither is recorded.
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"model fit", http.StatusBadRequest, `{"error":{"message":"context_length_exceeded"}}`},
		{"request invalid", http.StatusBadRequest, `{"error":{"message":"invalid tool use format"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ClassifyObservation(tc.status, []byte(tc.body), nil); ok {
				t.Errorf("%s was classified as a capacity fact, want ok=false", tc.name)
			}
		})
	}
}

func TestClassifyObservation_DailyLimitBody(t *testing.T) {
	body := []byte(`{"error":{"message":"You have reached the daily limit of 60 free-model requests for your account (it resets at 00:00 UTC) Retry after 34641 seconds, or add credits to use the paid version"}}`)
	got, ok := ClassifyObservation(http.StatusForbidden, body, nil)
	if !ok {
		t.Fatalf("a daily-limit 403 was not classified (ok=false)")
	}
	if got.Signal != SignalQuotaExceeded {
		t.Errorf("signal = %q, want %q", got.Signal, SignalQuotaExceeded)
	}
	if got.LimitValue != 60 || got.LimitUnit != "rpd" {
		t.Errorf("limit = %d %s, want 60 req/day (rpd)", got.LimitValue, got.LimitUnit)
	}
	if got.Evidence == "" {
		t.Error("evidence was not captured")
	}
}

func TestClassifyObservation_HeaderLimitWins(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-limit-tokens", "10000")
	got, ok := ClassifyObservation(http.StatusTooManyRequests, []byte(`rate limit`), h)
	if !ok {
		t.Fatalf("ok=false for a 429 with headers")
	}
	if got.LimitValue != 10000 || got.LimitUnit != "tokens" {
		t.Errorf("limit = %d %s, want 10000 tokens", got.LimitValue, got.LimitUnit)
	}
}

func TestParseObservedLimit_Units(t *testing.T) {
	tests := []struct {
		text     string
		wantVal  int
		wantUnit string
	}{
		{"limit is 5000 tokens per minute", 5000, "tpm"},
		{"300 requests per minute", 300, "rpm"},
		{"max 4 concurrent requests allowed", 4, "concurrent"},
		{"you may send 60 requests per day", 60, "rpd"},
		{"up to 200000 tokens per request", 200000, "tokens"},
		{"no numbers here", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			v, u := parseObservedLimit(tt.text, nil)
			if v != tt.wantVal || u != tt.wantUnit {
				t.Errorf("parseObservedLimit(%q) = %d %q, want %d %q", tt.text, v, u, tt.wantVal, tt.wantUnit)
			}
		})
	}
}

func TestObservationEvidence_PrefersJSONMessage(t *testing.T) {
	got := observationEvidence([]byte(`{"error":{"message":"quota exceeded for today"}}`))
	if got != "quota exceeded for today" {
		t.Errorf("evidence = %q, want the JSON message", got)
	}
	raw := observationEvidence([]byte("plain text failure"))
	if raw != "plain text failure" {
		t.Errorf("evidence = %q, want the trimmed raw text", raw)
	}
}
