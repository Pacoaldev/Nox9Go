package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"nox9go/proxy/internal/db"
)

// alertEntry mirrors one row of the alerts array on the wire.
type alertEntry struct {
	ConnectionID   string `json:"connectionId"`
	ConnectionName string `json:"connectionName"`
	Scope          string `json:"scope"`
	Provider       string `json:"provider"`
	AlertType      string `json:"alertType"`
	Message        string `json:"message"`
	Status         int    `json:"status"`
	Model          string `json:"model"`
	Source         string `json:"source"`
	Timestamp      string `json:"timestamp"`
	RateLimitedAt  string `json:"rateLimitedAt"`
	BackoffLevel   int    `json:"backoffLevel"`
	TestStatus     string `json:"testStatus"`
}

// alertsPayload mirrors the wire shape of GET /api/dashboard/alerts.
type alertsPayload struct {
	Alerts      []alertEntry `json:"alerts"`
	Counts      struct {
		Total        int `json:"total"`
		Locked       int `json:"locked"`
		Errored      int `json:"errored"`
		ModelLock    int `json:"modelLock"`
		ProviderLock int `json:"providerLock"`
	} `json:"counts"`
	GeneratedAt string `json:"generatedAt"`
}

// findAlert returns the alert for a connection, and whether it was reported.
func findAlert(payload alertsPayload, connectionID string) (alertEntry, bool) {
	for _, a := range payload.Alerts {
		if a.ConnectionID == connectionID {
			return a, true
		}
	}
	return alertEntry{}, false
}

// findScopeAlert returns the alert recorded for one alert scope, keyed by the
// provider it names, so a pair-level alert with no connection can be found.
func findScopeAlert(payload alertsPayload, scope, provider string) (alertEntry, bool) {
	for _, a := range payload.Alerts {
		if a.Scope == scope && a.Provider == provider {
			return a, true
		}
	}
	return alertEntry{}, false
}

func TestHandleAlerts(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := database
	if err := db.EnsureCoreSchema(repo.RawDB()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	lockUntil := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	expiredLock := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)

	// Healthy: never reports.
	if err := repo.CreateProviderConnectionFull("c-healthy", "openai", "apikey", "Key1", nil, `{"testStatus": "active"}`); err != nil {
		t.Fatalf("create healthy connection: %v", err)
	}
	// Account-scoped rate limit, with the message the upstream produced.
	if err := repo.CreateProviderConnectionFull("c-locked", "mistral", "apikey", "Key118", nil,
		`{"rateLimitedUntil": "`+lockUntil+`", "backoffLevel": 2, "lastError": {"status": 429, "message": "Quota exceeded", "timestamp": "2026-10-26T10:00:00Z"}}`); err != nil {
		t.Fatalf("create locked connection: %v", err)
	}
	// Per-model lock: the model travels in the key name.
	if err := repo.CreateProviderConnectionFull("c-modellock", "anthropic", "apikey", "Key2", nil,
		`{"modelLock_mistral-large-latest": "`+lockUntil+`", "backoffLevel": 1}`); err != nil {
		t.Fatalf("create model-locked connection: %v", err)
	}
	// Failed probe: the error carries the model and the source.
	if err := repo.CreateProviderConnectionFull("c-failed", "groq", "apikey", "Key3", nil,
		`{"testStatus": "failed", "lastError": {"status": 401, "message": "Invalid API key", "model": "llama-3", "source": "chat", "timestamp": "2026-10-26T10:05:00Z"}}`); err != nil {
		t.Fatalf("create failed connection: %v", err)
	}
	// An expired cooldown is not an alert.
	if err := repo.CreateProviderConnectionFull("c-expired", "deepseek", "apikey", "Key4", nil,
		`{"rateLimitedUntil": "`+expiredLock+`"}`); err != nil {
		t.Fatalf("create expired connection: %v", err)
	}
	// Inactive: parked on purpose, never alerts.
	if err := repo.CreateProviderConnectionFull("c-inactive", "tavily", "apikey", "Key5", nil, `{"testStatus": "error"}`); err != nil {
		t.Fatalf("create inactive connection: %v", err)
	}
	if err := repo.SetConnectionStatus("c-inactive", false); err != nil {
		t.Fatalf("deactivate c-inactive: %v", err)
	}
	// Unsupported probe: a limitation of the test suite, not a broken credential.
	if err := repo.CreateProviderConnectionFull("c-unsupported", "brave", "apikey", "Key6", nil,
		`{"testStatus": "error", "lastError": "Provider test not supported"}`); err != nil {
		t.Fatalf("create unsupported connection: %v", err)
	}

	// A provider-model lockout has no connection row: the quota is spent for
	// every key of the pair, so it is reported once, at pair scope.
	pairUntil := time.Now().Add(20 * time.Minute).UTC()
	if err := repo.LockProviderModel("nvidia", "nemotron-4-340b", pairUntil, "quota_exhausted", 429); err != nil {
		t.Fatalf("lock provider model: %v", err)
	}
	// An expired pair lockout is not an alert.
	if err := repo.LockProviderModel("chutes", "deepseek-r1", time.Now().Add(-time.Minute).UTC(), "quota_exhausted", 429); err != nil {
		t.Fatalf("lock expired provider model: %v", err)
	}

	r := chi.NewRouter()
	RegisterRoutes(r, NewDashboardHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/alerts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	var payload alertsPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload.Counts.Total != 4 {
		t.Errorf("counts.total = %d, want 4 (one lock, one model lock, one error, one pair lock)", payload.Counts.Total)
	}
	if payload.Counts.Locked != 2 {
		t.Errorf("counts.locked = %d, want 2 (account cooldown plus pair lockout)", payload.Counts.Locked)
	}
	if payload.Counts.ModelLock != 1 {
		t.Errorf("counts.modelLock = %d, want 1", payload.Counts.ModelLock)
	}
	if payload.Counts.ProviderLock != 1 {
		t.Errorf("counts.providerLock = %d, want 1", payload.Counts.ProviderLock)
	}
	if payload.Counts.Errored != 1 {
		t.Errorf("counts.errored = %d, want 1", payload.Counts.Errored)
	}
	if payload.GeneratedAt == "" {
		t.Error("generatedAt is empty")
	}

	// The lock names the provider and the key, which is what the bell could not
	// say before.
	locked, ok := findAlert(payload, "c-locked")
	if !ok {
		t.Fatal("c-locked did not report an alert")
	}
	if locked.Provider != "mistral" {
		t.Errorf("c-locked provider = %q, want mistral", locked.Provider)
	}
	if locked.ConnectionName != "Key118" {
		t.Errorf("c-locked connectionName = %q, want Key118", locked.ConnectionName)
	}
	if locked.AlertType != "lock" {
		t.Errorf("c-locked alertType = %q, want lock", locked.AlertType)
	}
	if locked.Message != "Quota exceeded" {
		t.Errorf("c-locked message = %q, want %q", locked.Message, "Quota exceeded")
	}
	if locked.RateLimitedAt != lockUntil {
		t.Errorf("c-locked rateLimitedAt = %q, want %q", locked.RateLimitedAt, lockUntil)
	}
	if locked.BackoffLevel != 2 {
		t.Errorf("c-locked backoffLevel = %d, want 2", locked.BackoffLevel)
	}

	// The model lock names the model it holds.
	modelLocked, ok := findAlert(payload, "c-modellock")
	if !ok {
		t.Fatal("c-modellock did not report an alert")
	}
	if modelLocked.Model != "mistral-large-latest" {
		t.Errorf("c-modellock model = %q, want mistral-large-latest", modelLocked.Model)
	}
	if modelLocked.AlertType != "lock" {
		t.Errorf("c-modellock alertType = %q, want lock", modelLocked.AlertType)
	}

	// The failed probe carries the upstream's own error.
	failed, ok := findAlert(payload, "c-failed")
	if !ok {
		t.Fatal("c-failed did not report an alert")
	}
	if failed.AlertType != "error" {
		t.Errorf("c-failed alertType = %q, want error", failed.AlertType)
	}
	if failed.Message != "Invalid API key" {
		t.Errorf("c-failed message = %q, want %q", failed.Message, "Invalid API key")
	}
	if failed.Status != 401 {
		t.Errorf("c-failed status = %d, want 401", failed.Status)
	}
	if failed.Model != "llama-3" {
		t.Errorf("c-failed model = %q, want llama-3", failed.Model)
	}
	if failed.Source != "chat" {
		t.Errorf("c-failed source = %q, want chat", failed.Source)
	}
	if failed.TestStatus != "failed" {
		t.Errorf("c-failed testStatus = %q, want failed", failed.TestStatus)
	}

	// The four that must stay silent.
	for _, id := range []string{"c-healthy", "c-expired", "c-inactive", "c-unsupported"} {
		if _, ok := findAlert(payload, id); ok {
			t.Errorf("%s reported an alert, want none", id)
		}
	}

	// The pair lockout is reported once, at pair scope, naming the model and
	// the deadline the operator can act on.
	pair, ok := findScopeAlert(payload, "provider-model", "nvidia")
	if !ok {
		t.Fatal("nvidia did not report a provider-model alert")
	}
	if pair.ConnectionID != "" {
		t.Errorf("pair alert connectionId = %q, want empty (the lock is not tied to a key)", pair.ConnectionID)
	}
	if pair.Model != "nemotron-4-340b" {
		t.Errorf("pair alert model = %q, want nemotron-4-340b", pair.Model)
	}
	if pair.AlertType != "lock" {
		t.Errorf("pair alert type = %q, want lock", pair.AlertType)
	}
	if pair.Status != 429 {
		t.Errorf("pair alert status = %d, want 429", pair.Status)
	}
	if pair.RateLimitedAt != pairUntil.Format(time.RFC3339) {
		t.Errorf("pair alert rateLimitedAt = %q, want %q", pair.RateLimitedAt, pairUntil.Format(time.RFC3339))
	}
	// The expired pair lockout must stay silent.
	if _, ok := findScopeAlert(payload, "provider-model", "chutes"); ok {
		t.Error("an expired provider-model lockout reported an alert, want none")
	}
}

// TestHandleAlerts_NoRepo proves the handler answers with an empty, well-formed
// payload when the gateway has no database: the bell must render, not crash.
func TestHandleAlerts_NoRepo(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutes(r, NewDashboardHandler(nil))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/alerts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	var payload alertsPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Counts.Total != 0 {
		t.Errorf("counts.total = %d, want 0", payload.Counts.Total)
	}
	if payload.Alerts == nil {
		t.Error("alerts is null, want an empty list")
	}
}
