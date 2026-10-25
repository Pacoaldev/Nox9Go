package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"nox9go/proxy/internal/db"
)

func setupProvidersClientRouter(h *DashboardHandler) chi.Router {
	r := chi.NewRouter()
	r.Get("/api/providers/client", h.HandleGetProvidersClient)
	return r
}

func seedProvidersClient(t *testing.T, repo *db.Repo) {
	t.Helper()
	seed := []struct {
		id, provider, authType, name, key string
	}{
		{"c-codex", "codex", "oauth", "Codex Work", "secret-codex"},
		{"c-kimi", "kimi", "apikey", "Kimi Key", "secret-kimi"},
		{"c-openai", "openai", "apikey", "OpenAI", "secret-openai"}, // No metered endpoint; still listed.
		{"c-long", "trae", "oauth", "abcdefghijklmnopqrstuvwxyz0123456789ABCD", "secret-trae"},
	}
	for _, s := range seed {
		if err := repo.CreateProviderConnection(s.id, s.provider, s.authType, s.name, s.key); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}
}

func getProvidersClient(t *testing.T, r chi.Router, query string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/providers/client"+query, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return out
}

func TestProvidersClientPagination(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	seedProvidersClient(t, repo)
	r := setupProvidersClientRouter(h)

	out := getProvidersClient(t, r, "")
	conns, _ := out["connections"].([]any)
	if len(conns) != 4 {
		t.Fatalf("expected all 4 connections, including providers without a metered endpoint; got %d", len(conns))
	}
	pag, _ := out["pagination"].(map[string]any)
	if pag["total"] != float64(4) || pag["totalPages"] != float64(1) {
		t.Errorf("unexpected pagination: %v", pag)
	}
	tot, _ := out["totals"].(map[string]any)
	if tot["eligibleConnections"] != float64(4) {
		t.Errorf("unexpected totals: %v", tot)
	}
	opts, _ := out["providerOptions"].([]any)
	if len(opts) != 4 {
		t.Errorf("expected all 4 providers, got %v", opts)
	}
	foundOpenAI := false
	for _, c := range conns {
		m, _ := c.(map[string]any)
		if m["provider"] == "openai" {
			foundOpenAI = true
		}
	}
	if !foundOpenAI {
		t.Error("a provider without a metered endpoint must still be listed")
	}
	// Secrets must never leak; long token-like names masked.
	for _, c := range conns {
		m, _ := c.(map[string]any)
		if m["provider"] == "trae" && m["name"] != "abcdefgh***" {
			t.Errorf("expected masked trae name, got %v", m["name"])
		}
		if id, _ := m["id"].(string); id == "c-kimi" {
			if m["authType"] != "apikey" {
				t.Errorf("kimi apikey connection must stay eligible, got %v", m)
			}
		}
	}
	// No raw secrets anywhere in the payload.
	if raw, err := json.Marshal(out); err != nil {
		t.Fatalf("marshal: %v", err)
	} else if strings.Contains(string(raw), "secret-") {
		t.Errorf("response leaks secrets")
	}
}

func TestProvidersClientFilters(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	seedProvidersClient(t, repo)
	// Deactivate kimi via repo update path.
	if _, err := repo.RawDB().Exec(`UPDATE providerConnections SET isActive = 0 WHERE id = 'c-kimi'`); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	r := setupProvidersClientRouter(h)

	out := getProvidersClient(t, r, "?provider=kimi")
	if conns, _ := out["connections"].([]any); len(conns) != 1 {
		t.Fatalf("provider filter: expected 1, got %d", len(conns))
	}
	out = getProvidersClient(t, r, "?accountStatus=inactive")
	if conns, _ := out["connections"].([]any); len(conns) != 1 {
		t.Fatalf("inactive filter: expected 1, got %d", len(conns))
	} else if m, _ := conns[0].(map[string]any); m["id"] != "c-kimi" {
		t.Errorf("inactive filter returned %v, want c-kimi", m["id"])
	}
	out = getProvidersClient(t, r, "?accountStatus=active")
	if conns, _ := out["connections"].([]any); len(conns) != 3 {
		t.Fatalf("active filter: expected 3, got %d", len(conns))
	}
}

func TestProvidersClientPaging(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	seedProvidersClient(t, repo)
	r := setupProvidersClientRouter(h)

	out := getProvidersClient(t, r, "?pageSize=2&page=2")
	conns, _ := out["connections"].([]any)
	if len(conns) != 2 {
		t.Fatalf("page 2 of 4 with size 2: expected 2, got %d", len(conns))
	}
	pag, _ := out["pagination"].(map[string]any)
	if pag["page"] != float64(2) || pag["totalPages"] != float64(2) || pag["total"] != float64(4) {
		t.Errorf("unexpected pagination: %v", pag)
	}
	// Out-of-range page clamps to last page.
	out = getProvidersClient(t, r, "?pageSize=2&page=99")
	if conns, _ := out["connections"].([]any); len(conns) != 2 {
		t.Fatalf("clamped page: expected 2, got %d", len(conns))
	}
}

func TestProvidersClientProviderSort(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	seedProvidersClient(t, repo)
	r := setupProvidersClientRouter(h)

	out := getProvidersClient(t, r, "?sort=provider")
	conns, _ := out["connections"].([]any)
	if len(conns) != 4 {
		t.Fatalf("expected all 4 connections, got %d", len(conns))
	}
	// Metered providers keep usageSupportedProviders order (codex < kimi < trae);
	// a provider without a metered endpoint sorts after them, alphabetically.
	want := []string{"codex", "kimi", "trae", "openai"}
	for i, id := range want {
		m, _ := conns[i].(map[string]any)
		if m["provider"] != id {
			t.Errorf("position %d: got %v, want %s", i, m["provider"], id)
		}
	}
}

func TestSanitizeProviderConnection_LastErrorString(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	connID := "c-err"
	if err := repo.CreateProviderConnection(connID, "codex", "oauth", "Codex Err", "secret-codex"); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := repo.LockConnectionRateLimit(connID, time.Now().Add(time.Minute), 1, 429, "Rate limit reached"); err != nil {
		t.Fatalf("lock rate limit: %v", err)
	}

	r := setupProvidersClientRouter(h)
	out := getProvidersClient(t, r, "?provider=codex")
	conns, _ := out["connections"].([]any)
	if len(conns) == 0 {
		t.Fatalf("expected codex connection, got none")
	}
	first, _ := conns[0].(map[string]any)
	lastErr, ok := first["lastError"].(string)
	if !ok {
		t.Fatalf("expected lastError to be string, got %T: %v", first["lastError"], first["lastError"])
	}
	if lastErr != "Rate limit reached" {
		t.Errorf("lastError = %q, want %q", lastErr, "Rate limit reached")
	}
}

// The quota tracker lists every visible connection so the operator can find and
// manage all of them; hidden providers stay out of the provider options list.
func TestHiddenProvidersStayOutOfTheQuotaList(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	for _, s := range []struct{ id, provider, authType string }{
		{"c-claude", "claude", "oauth"},
		{"c-deepseek", "deepseek", "apikey"},
		// mmf/mimo-free is the registry's one hidden chat provider; the other
		// four hidden entries are TTS-only.
		{"c-mmf", "mmf", "apikey"},
	} {
		if err := repo.CreateProviderConnection(s.id, s.provider, s.authType, s.id, "sk-"+s.provider); err != nil {
			t.Fatalf("seed %s: %v", s.provider, err)
		}
	}

	out := getProvidersClient(t, setupProvidersClientRouter(h), "")
	rawOpts, _ := out["providerOptions"].([]any)
	opts := make([]string, 0, len(rawOpts))
	for _, o := range rawOpts {
		s, _ := o.(string)
		opts = append(opts, s)
	}
	if len(opts) != 3 {
		t.Errorf("providerOptions = %v, want visible claude, deepseek, and mmf providers", opts)
	}
}
