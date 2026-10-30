package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nox9go/proxy/internal/db"
)

// A combo must never dial a cooling account just because forceFallback is on.
// That toggle exists for a lone provider with nowhere else to go; a combo's next
// member is the failover. Dialling Gemini (or any provider) through an active
// rateLimitedUntil is how an operator earns a ban.
func TestCombo_ForceFallbackDoesNotDialCoolingMember(t *testing.T) {
	var coolingHits atomic.Int32
	cooling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coolingHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer cooling.Close()

	var healthyHits atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(okChatBody))
	}))
	defer healthy.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	clearSeededConns(t, database)

	until := time.Now().UTC().Add(30 * time.Minute)
	// Account-scoped cooldown only — no modelLock_* — which is the shape the
	// alerts panel shows for "Bloqueado: Rate limit exceeded".
	seedConnDB(t, database, "deepseek", "conn-cool", "sk-cool", cooling.URL)
	if err := db.NewRepo(database).LockConnectionRateLimit(
		"conn-cool", until, 2, http.StatusTooManyRequests, "rate limit exceeded",
	); err != nil {
		t.Fatalf("LockConnectionRateLimit: %v", err)
	}
	seedConnDB(t, database, "groq", "conn-groq", "sk-groq", healthy.URL)
	seedForceFallback(t, database)

	h := NewChatHandler(db.NewRepo(database))
	body := []byte(`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	h.handleComboFallback(context.Background(), rec, body,
		[]string{"deepseek/deepseek-chat", "groq/llama-3.1-70b-versatile"},
		"fallback", false, false, "combo-no-force", 0)

	if got := coolingHits.Load(); got != 0 {
		t.Errorf("cooling combo member received %d requests, want 0 (forceFallback must not dial it)", got)
	}
	if got := healthyHits.Load(); got != 1 {
		t.Errorf("next combo member received %d requests, want 1", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}
