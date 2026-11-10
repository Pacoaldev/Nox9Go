package dashboard

import (
	"net/http"
	"net/http/httptest"
	json "encoding/json/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"nox9go/proxy/internal/db"
)

// A provider with no metered quota endpoint used to render only "no quota
// details". The card now carries what the gateway itself routed through the
// connection, which is a fact the gateway owns.

func newUsageTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	conn, err := db.OpenDatabase(filepath.Join(t.TempDir(), "data.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

func seedUsageConnection(t *testing.T, repo *db.Repo, id, provider string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := repo.RawDB().Exec(
		`INSERT INTO providerConnections (id, provider, authType, isActive, data, createdAt, updatedAt)
		 VALUES (?, ?, 'apikey', 1, '{"apiKey":"k"}', ?, ?)`, id, provider, now, now)
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
}

func seedUsageRow(t *testing.T, repo *db.Repo, connID string, at time.Time, prompt, compl int) {
	t.Helper()
	_, err := repo.RawDB().Exec(
		`INSERT INTO usageHistory (timestamp, provider, model, connectionId, apiKey, clientKey, endpoint, promptTokens, completionTokens, cost, status, tokens, meta)
		 VALUES (?, 'somefree', 'free-model', ?, 'k', 'c', '/v1/chat/completions', ?, ?, 0, 'success', '{}', '')`,
		at.UTC().Format(time.RFC3339), connID, prompt, compl)
	if err != nil {
		t.Fatalf("seed usage row: %v", err)
	}
}

func usageRequest(t *testing.T, h *DashboardHandler, connID string) map[string]any {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/api/usage/{connectionId}", h.HandleGetConnectionUsage)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage/"+connID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func TestHandleGetConnectionUsage_ReportsGatewayUsageWithoutMeteredQuota(t *testing.T) {
	repo := newUsageTestRepo(t)
	seedUsageConnection(t, repo, "conn-free", "somefree")
	now := time.Now().UTC()
	seedUsageRow(t, repo, "conn-free", now.Add(-2*time.Hour), 400, 100)
	seedUsageRow(t, repo, "conn-free", now.Add(-26*time.Hour), 200, 50)
	// Outside the 30-day window: it must not inflate the card.
	seedUsageRow(t, repo, "conn-free", now.Add(-40*24*time.Hour), 9000, 9000)

	out := usageRequest(t, NewDashboardHandler(repo), "conn-free")
	usage, ok := out["usage"].(map[string]any)
	if !ok {
		t.Fatalf("response has no gateway usage: %v", out)
	}
	if usage["requests"] != float64(2) {
		t.Errorf("requests = %v, want 2", usage["requests"])
	}
	if usage["tokens"] != float64(750) {
		t.Errorf("tokens = %v, want 750", usage["tokens"])
	}
	if usage["lastUsed"] == "" {
		t.Error("lastUsed must name the most recent request")
	}
}

func TestHandleGetConnectionUsage_NoGatewayUsageWhenASourceExists(t *testing.T) {
	repo := newUsageTestRepo(t)
	seedUsageConnection(t, repo, "conn-metered", "somefree")
	seedUsageRow(t, repo, "conn-metered", time.Now().UTC(), 10, 5)

	// A connection parked by a rate limit renders a real quota row from its
	// stored lock, so the gateway summary must stay out of the way.
	if err := repo.UpdateConnectionData("conn-metered", `{"rateLimitedUntil":"`+
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339)+`"}`); err != nil {
		t.Fatalf("park connection: %v", err)
	}

	out := usageRequest(t, NewDashboardHandler(repo), "conn-metered")
	if _, ok := out["usage"]; ok {
		t.Fatalf("a response with quota rows must not carry the gateway summary: %v", out)
	}
}
