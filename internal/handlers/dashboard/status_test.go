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

func TestHandleStatus(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := database
	if err := db.EnsureCoreSchema(repo.RawDB()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	// Seed today's usage row
	todayKey := time.Now().UTC().Format("2006-01-02")
	if err := repo.UpsertUsageDaily(todayKey, `{"totalRequests": 42, "totalPromptTokens": 1000, "totalCompletionTokens": 500}`); err != nil {
		t.Fatalf("seed usageDaily: %v", err)
	}

	// Seed one healthy, one locked, one failed, one inactive-with-error, one unsupported-test
	lockUntil := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if err := repo.CreateProviderConnectionFull("c1", "openai", "apikey", "c1", nil, `{"testStatus": "active"}`); err != nil {
		t.Fatalf("create connection c1: %v", err)
	}
	if err := repo.CreateProviderConnectionFull("c2", "anthropic", "apikey", "c2", nil, `{"rateLimitedUntil": "`+lockUntil+`"}`); err != nil {
		t.Fatalf("create connection c2: %v", err)
	}
	if err := repo.CreateProviderConnectionFull("c3", "groq", "apikey", "c3", nil, `{"testStatus": "failed"}`); err != nil {
		t.Fatalf("create connection c3: %v", err)
	}
	// Inactive connection: should not alert
	if err := repo.CreateProviderConnectionFull("c4", "deepseek", "apikey", "c4", nil, `{"testStatus": "error"}`); err != nil {
		t.Fatalf("create connection c4: %v", err)
	}
	if err := repo.SetConnectionStatus("c4", false); err != nil {
		t.Fatalf("deactivate c4: %v", err)
	}
	// Unsupported test: should not alert
	if err := repo.CreateProviderConnectionFull("c5", "tavily", "apikey", "c5", nil, `{"testStatus": "error", "lastError": "Provider test not supported"}`); err != nil {
		t.Fatalf("create connection c5: %v", err)
	}

	r := chi.NewRouter()
	dashH := NewDashboardHandler(repo)
	RegisterRoutes(r, dashH)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Gateway  map[string]any `json:"gateway"`
		Database map[string]any `json:"database"`
		Tunnel   map[string]any `json:"tunnel"`
		Today    map[string]any `json:"today"`
		Alerts   map[string]any `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload.Gateway["ok"] != true {
		t.Errorf("gateway.ok = %v, want true", payload.Gateway["ok"])
	}
	if payload.Database["ok"] != true {
		t.Errorf("database.ok = %v, want true", payload.Database["ok"])
	}
	if reqCount, ok := payload.Today["requests"].(float64); !ok || int(reqCount) != 42 {
		t.Errorf("today.requests = %v, want 42", payload.Today["requests"])
	}
	if locked, ok := payload.Alerts["locked"].(float64); !ok || int(locked) != 1 {
		t.Errorf("alerts.locked = %v, want 1", payload.Alerts["locked"])
	}
	if alerts, ok := payload.Alerts["connections"].(float64); !ok || int(alerts) != 1 {
		t.Errorf("alerts.connections = %v, want 1", payload.Alerts["connections"])
	}
}
