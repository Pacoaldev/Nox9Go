package chat

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlerutil"
)

// okChatBody is the answer a healthy upstream returns, in the shape the
// OpenAI-compatible executors parse.
const okChatBody = `{"id":"chatcmpl-1","object":"chat.completion","created":0,"model":"deepseek-chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

// setupProviderModelLockDB opens the shared chat fixture and adds the
// fork-local providerModelLocks table. The shared fixture builds the upstream
// core tables plus the Go-only ones, and the lockout table is neither.
func setupProviderModelLockDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	database, cleanup := setupChatTestDB(t)
	if err := db.EnsureProviderModelLocks(database); err != nil {
		cleanup()
		t.Fatalf("ensure providerModelLocks: %v", err)
	}
	return database, cleanup
}

// lockoutRowExists reports whether the pair has a row at all, so a test can
// tell "no row" from "a row that reads as absent".
func lockoutRowExists(t *testing.T, database *sql.DB, provider, model string) bool {
	t.Helper()
	var until string
	err := database.QueryRow(
		`SELECT lockedUntil FROM providerModelLocks WHERE provider = ? AND model = ?`, provider, model,
	).Scan(&until)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("read providerModelLocks row: %v", err)
	}
	return true
}

// connectionData reads the raw data blob of a connection, so a test can check
// the per-connection fields the shared dashboard reads.
func connectionData(t *testing.T, database *sql.DB, connID string) string {
	t.Helper()
	var raw string
	if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = ?`, connID).Scan(&raw); err != nil {
		t.Fatalf("read connection %s: %v", connID, err)
	}
	return raw
}

// A quota-marked 429 is a fact about the (provider, model) pair: the quota is
// spent for every key of that provider, so the pair is parked as a unit.
func TestProviderModelLockout_Quota429ParksThePair(t *testing.T) {
	EnableSharedQuotaProvider("deepseek")
	defer ResetSharedQuotaProviders()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED","details":[{"reason":"QUOTA_EXHAUSTED"}]}}`))
	}))
	defer srv.Close()

	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds", "sk-ds", srv.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	if err := h.handleAccountFallback(context.Background(), rec, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err == nil {
		t.Fatal("expected the 429 to be returned as an error")
	}

	until, locked, err := repo.ProviderModelLockedUntil("deepseek", "deepseek-chat")
	if err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	}
	if !locked {
		t.Fatal("expected the (provider, model) pair to be parked after a quota-marked 429")
	}
	if !until.After(time.Now()) {
		t.Errorf("expected a future deadline, got %s", until.Format(time.RFC3339))
	}

	// The pair lockout is an ADDITIONAL scope: the connection that failed
	// keeps its own per-connection cooldown, which the shared dashboard
	// reads. What the pair lockout changes is the scope — the other keys of
	// the provider are never dialled for this model (see the combo test).
	raw := connectionData(t, database, "conn-ds")
	if _, ok := db.ConnectionCooldownUntil(raw); ok {
		t.Error("expected rateLimitedUntil to NOT be set for a model-scoped quota failure")
	}
	connLocked, err := repo.IsConnectionModelLocked("conn-ds", "deepseek-chat")
	if err != nil {
		t.Fatalf("IsConnectionModelLocked: %v", err)
	}
	if !connLocked {
		t.Error("expected the failing connection to keep its own per-model cooldown")
	}
}

// A generic 429 names a per-account or per-IP limit, not a spent model quota,
// so it keeps today's per-connection cooldown and must not park the pair.
func TestProviderModelLockout_Generic429DoesNotParkThePair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds", "sk-ds", srv.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	if err := h.handleAccountFallback(context.Background(), rec, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err == nil {
		t.Fatal("expected the 429 to be returned as an error")
	}

	if _, locked, err := repo.ProviderModelLockedUntil("deepseek", "deepseek-chat"); err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	} else if locked {
		t.Error("a generic 429 must not park the (provider, model) pair")
	}
	if lockoutRowExists(t, database, "deepseek", "deepseek-chat") {
		t.Error("a generic 429 must not write a provider+model lockout row")
	}
}

// A revoked grant parks one account: the provider's other keys keep serving,
// so the pair must not be parked.
func TestProviderModelLockout_401DoesNotParkThePair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid OAuth credentials","status":"UNAUTHENTICATED"}}`))
	}))
	defer srv.Close()

	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds", "sk-ds", srv.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	if err := h.handleAccountFallback(context.Background(), rec, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err == nil {
		t.Fatal("expected the 401 to be returned as an error")
	}

	if _, locked, err := repo.ProviderModelLockedUntil("deepseek", "deepseek-chat"); err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	} else if locked {
		t.Error("a 401 must not park the (provider, model) pair")
	}
}

// While the pair is parked, no connection of that provider may serve that
// model. Another model of the same provider is untouched.
func TestProviderModelLockout_SelectorRefusesThePair(t *testing.T) {
	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds-1", "sk-ds-1", "http://127.0.0.1:1")
	seedConnDB(t, database, "deepseek", "conn-ds-2", "sk-ds-2", "http://127.0.0.1:1")

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	until := time.Now().UTC().Add(30 * time.Minute)
	if err := repo.LockProviderModel("deepseek", "deepseek-chat", until, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}

	conn, _, err := h.getBestConnection("deepseek", "", nil, "deepseek-chat")
	if err == nil {
		t.Fatalf("expected the selector to refuse a parked pair, got connection %v", conn)
	}
	if conn != nil {
		t.Errorf("expected no connection for a parked pair, got %v", conn.ID)
	}

	// The lockout is scoped to the pair: another model of the same provider
	// is still served.
	conn, _, err = h.getBestConnection("deepseek", "", nil, "deepseek-reasoner")
	if err != nil {
		t.Fatalf("getBestConnection for an unlocked model: %v", err)
	}
	if conn == nil {
		t.Fatal("expected a connection for an unlocked model")
	}
}

// An expired lockout is absent: the pair is served again.
func TestProviderModelLockout_ExpiredLockoutIsAbsent(t *testing.T) {
	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds", "sk-ds", "http://127.0.0.1:1")

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	past := time.Now().UTC().Add(-time.Minute)
	if err := repo.LockProviderModel("deepseek", "deepseek-chat", past, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}

	conn, _, err := h.getBestConnection("deepseek", "", nil, "deepseek-chat")
	if err != nil {
		t.Fatalf("getBestConnection: %v", err)
	}
	if conn == nil || conn.ID != "conn-ds" {
		t.Errorf("expected the expired pair to be served again, got %v", conn)
	}
}

// A parked combo member is skipped whole: not one connection of that provider
// is dialled, and the next member serves the turn.
func TestProviderModelLockout_ComboSkipsTheParkedMember(t *testing.T) {
	var parkedHits atomic.Int32
	parked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parkedHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer parked.Close()

	var healthyHits atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(okChatBody))
	}))
	defer healthy.Close()

	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds-1", "sk-ds-1", parked.URL)
	seedConnDB(t, database, "deepseek", "conn-ds-2", "sk-ds-2", parked.URL)
	seedConnDB(t, database, "groq", "conn-groq", "sk-groq", healthy.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	until := time.Now().UTC().Add(30 * time.Minute)
	if err := repo.LockProviderModel("deepseek", "deepseek-chat", until, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}

	body := []byte(`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	h.handleComboFallback(context.Background(), rec, body,
		[]string{"deepseek/deepseek-chat", "groq/llama-3.1-70b-versatile"},
		"fallback", false, false, "combo-lockout", 0)

	if got := parkedHits.Load(); got != 0 {
		t.Errorf("the parked provider received %d requests, want 0", got)
	}
	if got := healthyHits.Load(); got != 1 {
		t.Errorf("the next member received %d requests, want 1", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// A quota-marked 429 inside a combo parks the pair and moves to the next
// member instead of walking the next key of the same provider.
func TestProviderModelLockout_ComboParksThePairAndMovesOn(t *testing.T) {
	EnableSharedQuotaProvider("deepseek")
	defer ResetSharedQuotaProviders()
	var dsHits atomic.Int32
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dsHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED","details":[{"reason":"QUOTA_EXHAUSTED"}]}}`))
	}))
	defer ds.Close()

	var healthyHits atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(okChatBody))
	}))
	defer healthy.Close()

	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds-1", "sk-ds-1", ds.URL)
	seedConnDB(t, database, "deepseek", "conn-ds-2", "sk-ds-2", ds.URL)
	seedConnDB(t, database, "groq", "conn-groq", "sk-groq", healthy.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	h.handleComboFallback(context.Background(), rec, body,
		[]string{"deepseek/deepseek-chat", "groq/llama-3.1-70b-versatile"},
		"fallback", false, false, "combo-lockout", 0)

	// The pool of the dead provider must not be walked: one attempt, then
	// the next combo member.
	if got := dsHits.Load(); got != 1 {
		t.Errorf("the dead provider received %d requests, want 1", got)
	}
	if got := healthyHits.Load(); got != 1 {
		t.Errorf("the next member received %d requests, want 1", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if _, locked, err := repo.ProviderModelLockedUntil("deepseek", "deepseek-chat"); err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	} else if !locked {
		t.Error("expected the pair to be parked after the quota-marked 429")
	}

	// The second key of the dead provider must not be marked: it was never
	// dialled, and marking it is what used to turn the whole failback pool
	// into "rate limited" accounts.
	secondLocked, err := repo.IsConnectionModelLocked("conn-ds-2", "deepseek-chat")
	if err != nil {
		t.Fatalf("IsConnectionModelLocked: %v", err)
	}
	if secondLocked {
		t.Error("the second key of the dead provider was marked, but it was never dialled")
	}
}

// A served request proves the pair is alive, so the lockout is dropped
// instead of waiting out its deadline.
//
// The request is dispatched through the forward path on purpose: the selector
// refuses a parked pair, so a request that IS served is one that was already
// in flight when the pair got parked. That is the same shape as the
// per-connection model lock, whose own clear-on-success has the same property.
func TestProviderModelLockout_ServedRequestClearsTheLockout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(okChatBody))
	}))
	defer srv.Close()

	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds", "sk-ds", srv.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	until := time.Now().UTC().Add(30 * time.Minute)
	if err := repo.LockProviderModel("deepseek", "deepseek-chat", until, "quota_exhausted", 429); err != nil {
		t.Fatalf("LockProviderModel: %v", err)
	}

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	if err := h.tryForwardWithConnection(forwardRequestParams{
		Ctx: context.Background(), W: rec, Provider: "deepseek", Model: "deepseek-chat",
		ConnectionID: "conn-ds", ConnData: &ConnectionData{APIKey: "sk-ds", BaseURL: srv.URL}, Body: body,
		IsStream: false, TranslateResponse: false, Endpoint: "/v1/chat/completions",
	}); err != nil {
		t.Fatalf("tryForwardWithConnection: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("client status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	if _, locked, err := repo.ProviderModelLockedUntil("deepseek", "deepseek-chat"); err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	} else if locked {
		t.Error("expected a served request to clear the pair lockout")
	}
	if lockoutRowExists(t, database, "deepseek", "deepseek-chat") {
		t.Error("expected the lockout row to be deleted after a served request")
	}
}

// The dashboard's Test button reaches the provider for real. Parking the pair
// because the operator tested it would take a working pair out of rotation.
func TestProviderModelLockout_ProbeDoesNotParkThePair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED","details":[{"reason":"QUOTA_EXHAUSTED"}]}}`))
	}))
	defer srv.Close()

	database, cleanup := setupProviderModelLockDB(t)
	defer cleanup()
	clearSeededConns(t, database)
	seedConnDB(t, database, "deepseek", "conn-ds", "sk-ds", srv.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	ctx := handlerutil.WithProbeContext(context.Background())
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	_ = h.handleAccountFallback(ctx, rec, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions")

	if _, locked, err := repo.ProviderModelLockedUntil("deepseek", "deepseek-chat"); err != nil {
		t.Fatalf("ProviderModelLockedUntil: %v", err)
	} else if locked {
		t.Error("a probe must not park the (provider, model) pair")
	}
}
