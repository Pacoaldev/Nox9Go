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

func TestClassifyObservation_RealWorldProviderBodies(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantOK     bool
		wantSignal string
		wantVal    int
		wantUnit   string
	}{
		{
			name:       "groq 413 TPM limit",
			status:     http.StatusRequestEntityTooLarge,
			body:       `Request too large for model moonshotai/kimi-k2-instruct on tokens per minute (TPM): Limit 8000, Requested 72572`,
			wantOK:     true,
			wantSignal: SignalRateLimit,
			wantVal:    8000,
			wantUnit:   "tpm",
		},
		{
			name:       "groq 413 ITPM limit",
			status:     http.StatusRequestEntityTooLarge,
			body:       `Request too large for model openai/gpt-oss-120b on input tokens per minute (ITPM): Limit 7000, Requested 22480`,
			wantOK:     true,
			wantSignal: SignalRateLimit,
			wantVal:    7000,
			wantUnit:   "tpm",
		},
		{
			name:       "gonkabroker 6 RPM",
			status:     http.StatusTooManyRequests,
			body:       `Rate limit exceeded (6 RPM). Add X-Wallet-Address header with $GNK balance for 60 RPM.`,
			wantOK:     true,
			wantSignal: SignalRateLimit,
			wantVal:    6,
			wantUnit:   "rpm",
		},
		{
			name:       "api-airforce 1 request(s) per minute",
			status:     http.StatusTooManyRequests,
			body:       `Ratelimit Exceeded! Please wait before making more requests. Limit: 1 request(s) per minute.`,
			wantOK:     true,
			wantSignal: SignalRateLimit,
			wantVal:    1,
			wantUnit:   "rpm",
		},
		{
			name:       "minimax-cn token plan quota CJK",
			status:     http.StatusTooManyRequests,
			body:       `已达到 Token Plan 用量上限，请稍后再试 (2056)`,
			wantOK:     true,
			wantSignal: SignalQuotaExceeded,
		},
		{
			name:       "minimax-cn token plan rate limit CJK",
			status:     http.StatusTooManyRequests,
			body:       `已达到 Token Plan 速率限制，请稍后再试 (1002)`,
			wantOK:     true,
			wantSignal: SignalRateLimit,
		},
		{
			name:       "openrouter upsell does not invent 1000 rpd limit",
			status:     http.StatusTooManyRequests,
			body:       `Rate limit exceeded: free-models-per-day. Add 10 credits to unlock 1000 free model requests per day`,
			wantOK:     true,
			wantSignal: SignalQuotaExceeded,
			wantVal:    0,
			wantUnit:   "",
		},
		{
			name:   "embedded 502 context length error is rejected",
			status: http.StatusBadGateway,
			body:   `upstream SSE error frame: {"message":"Upstream error: This model's maximum context length is 256000 tokens. However, your messages resulted in 288332 tokens.","code":"bad_response_status_code"}`,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ClassifyObservation(tt.status, []byte(tt.body), nil)
			if ok != tt.wantOK {
				t.Fatalf("ClassifyObservation() ok = %v, want %v (got %+v)", ok, tt.wantOK, got)
			}
			if !tt.wantOK {
				return
			}
			if got.Signal != tt.wantSignal {
				t.Errorf("Signal = %q, want %q", got.Signal, tt.wantSignal)
			}
			if got.LimitValue != tt.wantVal || got.LimitUnit != tt.wantUnit {
				t.Errorf("Limit = %d %q, want %d %q", got.LimitValue, got.LimitUnit, tt.wantVal, tt.wantUnit)
			}
		})
	}
}

func TestClassifyHeaderObservations(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests-minute", "30")
	h.Set("x-ratelimit-limit-tokens-minute", "12000")
	h.Set("x-ratelimit-limit-requests-day", "14400")

	got := ClassifyHeaderObservations(http.StatusOK, h)
	if len(got) != 3 {
		t.Fatalf("len(ClassifyHeaderObservations) = %d, want 3 (%+v)", len(got), got)
	}
	want := map[string]int{"rpm": 30, "tpm": 12000, "rpd": 14400}
	for _, obs := range got {
		if want[obs.LimitUnit] != obs.LimitValue {
			t.Errorf("unexpected header observation: %+v", obs)
		}
	}
}
