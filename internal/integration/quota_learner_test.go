//go:build integration

package integration

import (
	"io"
	"net/http"
	"testing"
	"time"

	"nox9go/proxy/internal/quotalearn"
)

// TestComboSkipsQuotaBlockedConnection pins the proactive quota skip: a
// connection parked by an active cooldown must not be dialed, and the combo
// serves the turn from the next eligible connection. The cooldown is seeded
// through the persisted quota_state row, which also proves the learner's
// load-on-first-touch path.
func TestComboSkipsQuotaBlockedConnection(t *testing.T) {
	env := newEnv(t)
	blocked := env.NewUpstream(t, chatCompletionResponder())
	healthy := env.NewUpstream(t, chatCompletionResponder())
	// Lower priority number is picked first: park conn-blocked at the front so
	// the skip is actually exercised rather than passed vacuously by the
	// picker choosing the healthy connection first.
	env.AddConnectionWithPriority(t, "conn-blocked", "groq", "Blocked", blocked, "sk-groq-1", 0)
	env.AddConnectionWithPriority(t, "conn-healthy", "groq", "Healthy", healthy, "sk-groq-2", 1)
	env.AddCombo(t, "combo-q", "quota", []string{"groq/llama-3.3-70b-versatile", "groq/mixtral-8x7b-32768"})

	// Seed an active cooldown for the exact (provider, model, connection) key
	// the first combo member resolves to.
	qr := quotalearn.NewRepo(env.Repo.RawDB())
	if err := qr.SetCooldown("groq", "llama-3.3-70b-versatile", "conn-blocked", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("seed cooldown: %v", err)
	}

	res := env.Post(t, "/v1/chat/completions", ChatBody("quota", false), WithAPIKey(env.APIKey))
	if res.Status != http.StatusOK {
		t.Fatalf("combo request = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	if got := blocked.Count(); got != 0 {
		t.Errorf("blocked upstream received %d requests, want 0 (proactive quota skip)", got)
	}
	if got := healthy.Count(); got != 1 {
		t.Errorf("healthy upstream received %d requests, want 1", got)
	}
}

// TestComboSkipsConnectionAtLearnedCeiling pins the learner's proactive half
// end to end: a successful response that announces an RPM ceiling of 1 is
// remembered, and the connection that already spent it is skipped before it is
// dialed again — the combo serves the next request from another connection
// instead of earning a throttle.
func TestComboSkipsConnectionAtLearnedCeiling(t *testing.T) {
	env := newEnv(t)
	// This upstream answers 200 and announces an RPM ceiling of 1.
	ceilinged := env.NewUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit-Requests", "1")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, chatCompletionJSON)
	})
	healthy := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnectionWithPriority(t, "conn-first", "groq", "First", ceilinged, "sk-groq-1", 0)
	env.AddConnectionWithPriority(t, "conn-healthy", "groq", "Healthy", healthy, "sk-groq-2", 1)
	env.AddCombo(t, "combo-ceil", "ceiling", []string{"groq/llama-3.3-70b-versatile", "groq/mixtral-8x7b-32768"})

	// First request: conn-first serves it and teaches the learner RPM=1.
	res := env.Post(t, "/v1/chat/completions", ChatBody("ceiling", false), WithAPIKey(env.APIKey))
	if res.Status != http.StatusOK {
		t.Fatalf("first combo request = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	if got := ceilinged.Count(); got != 1 {
		t.Fatalf("first upstream request count = %d, want 1", got)
	}

	// Second request: conn-first has spent the learned ceiling and must be
	// skipped without being dialed; conn-healthy serves the turn.
	res2 := env.Post(t, "/v1/chat/completions", ChatBody("ceiling", false), WithAPIKey(env.APIKey))
	if res2.Status != http.StatusOK {
		t.Fatalf("second combo request = %d, want 200 (body: %s)", res2.Status, truncate(res2.Body))
	}
	if got := ceilinged.Count(); got != 1 {
		t.Errorf("ceilinged upstream request count after second pass = %d, want still 1 (learned-ceiling skip)", got)
	}
	if got := healthy.Count(); got != 1 {
		t.Errorf("healthy upstream request count = %d, want 1", got)
	}
}
