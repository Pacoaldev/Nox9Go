package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nox9go/proxy/internal/db"
)

// The failover walk used to dial every remaining account after a 429. With a
// pool of independently keyed accounts that is 45 upstream calls in a single
// client turn, each one parking a healthy account on its own cooldown for a
// limit that answered all of them — the operator then reads "rate limit
// exceeded" on every account and concludes the credentials are exhausted.
//
// A run of 429s across different credentials is not a fact about any one of
// them, so the walk stops after maxConsecutive429PerTurn of them and reports
// the upstream's own answer (which carries Retry-After).
func TestHandleAccountFallback_StopsWalkingAfterConsecutive429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	// More accounts than the cap, so the cap is what stops the walk rather
	// than the pool running out: the difference between the two is exactly
	// the accounts the old code parked for no reason.
	const accounts = maxConsecutive429PerTurn + 5
	for i := 0; i < accounts; i++ {
		seedConnDB(t, database, "mistral", "conn-429-"+string(rune('a'+i)), "sk-"+string(rune('a'+i)), srv.URL)
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"codestral-latest","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	err := h.handleAccountFallback(context.Background(), rec, "mistral", "codestral-latest", "", body, false, false, "/v1/chat/completions")
	if err == nil {
		t.Fatal("expected an error after the walk hit the 429 cap")
	}

	if got := hits.Load(); got != maxConsecutive429PerTurn {
		t.Errorf("upstream hits = %d, want %d (walk must stop at the cap, not dial all %d accounts)",
			got, maxConsecutive429PerTurn, accounts)
	}

	// The client has to receive the upstream's own 429 so the Retry-After it
	// carries survives; a generic "no available connections" would erase the
	// only information that tells the caller when to come back.
	var ue *upstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v, want the upstream 429 to be preserved", err)
	}
	if ue.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", ue.StatusCode, http.StatusTooManyRequests)
	}
}

// A 500 in the middle of the walk says the provider is not refusing the whole
// pool, so the run of 429s is broken and the cap must not fire: the accounts
// after it were never rate limited and are still worth dialling.
func TestHandleAccountFallback_Non429ResetsTheCascadeCounter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		// Every attempt before the cap answers 429; the one at the cap
		// answers 503, a retryable verdict that says the provider is not
		// refusing the whole pool, so the run of 429s must break there.
		if n == maxConsecutive429PerTurn {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"temporarily unavailable"}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	const accounts = maxConsecutive429PerTurn + 3
	for i := 0; i < accounts; i++ {
		seedConnDB(t, database, "mistral", "conn-mix-"+string(rune('a'+i)), "sk-"+string(rune('a'+i)), srv.URL)
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"codestral-latest","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	_ = h.handleAccountFallback(context.Background(), rec, "mistral", "codestral-latest", "", body, false, false, "/v1/chat/completions")

	// One extra 429 after the reset is allowed before the cap can fire again,
	// so the walk reaches further than the cap alone.
	if got := hits.Load(); got <= int32(maxConsecutive429PerTurn) {
		t.Errorf("upstream hits = %d, want more than %d: a non-429 must reset the run", got, maxConsecutive429PerTurn)
	}
}

// retryableCooldownSec is the one owner of "how long this account stays out of
// rotation". Mistral's free tier answers a 1 RPS burst, and the classifier's
// two-second base put the account back in the pool before that burst had
// drained, so the same account absorbed the next request and earned another
// 429. The floor applies only when the upstream named no wait of its own: an
// announced Retry-After is the upstream's answer and is honoured exactly.
func TestRetryableCooldownSec_Mistral429Floor(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		header   http.Header
		body     []byte
		wantMin  int
		wantMax  int
	}{
		{
			name:     "mistral 429 with no announced wait waits the floor",
			provider: "mistral",
			body:     []byte(`{"error":"rate limited"}`),
			wantMin:  int(mistral429CooldownFloor / time.Second),
			wantMax:  int(mistral429CooldownFloor / time.Second),
		},
		{
			name:     "mistral 429 honours a shorter Retry-After instead of the floor",
			provider: "mistral",
			header:   http.Header{"Retry-After": []string{"3"}},
			body:     []byte(`{"error":"rate limited"}`),
			wantMin:  3,
			wantMax:  3,
		},
		{
			name:     "another provider keeps the classifier base",
			provider: "deepseek",
			body:     []byte(`{"error":"rate limited"}`),
			wantMin:  2,
			wantMax:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ue := &upstreamError{
				StatusCode: http.StatusTooManyRequests,
				Body:       tt.body,
				Header:     tt.header,
			}
			// The classifier's own base for a 429 is two seconds; the floor
			// is what this test is about, so the baseline stays real.
			base := time.Duration(2) * time.Second
			got := retryableCooldownSec(tt.provider, http.StatusTooManyRequests, base, ue)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("retryableCooldownSec(%s) = %d, want between %d and %d", tt.provider, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}