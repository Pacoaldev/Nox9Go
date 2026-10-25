package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nox9go/proxy/internal/upstreamwatch"
)

// stubGitHub points the watcher's API root at a local server. A unit test must
// never reach GitHub (AGENTS.md §5.C.3): the routes below answer both sources,
// so the handler tests exercise the real decode path with no network involved.
func stubGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/decolua/9router/tags"):
			_, _ = w.Write([]byte(`[{"name":"v0.5.109"}]`))
		case strings.HasPrefix(r.URL.Path, "/repos/luqman-v1/9router-go/releases"):
			_, _ = w.Write([]byte(`[{"tag_name":"v1.9.14","html_url":"https://example.test/v1.9.14"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	previous := upstreamwatch.APIBase
	upstreamwatch.APIBase = srv.URL
	t.Cleanup(func() { upstreamwatch.APIBase = previous })
	return srv
}

func TestUpstreamVersionsRouteAnswers(t *testing.T) {
	stubGitHub(t)
	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	h := NewDashboardHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/upstream-versions", nil)
	h.HandleGetUpstreamVersions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// Two tracked sources, always both present: the dashboard renders one row per
	// source, and a missing entry would look like a source that does not exist.
	var payload struct {
		Sources []struct {
			ID   string `json:"id"`
			Repo string `json:"repo"`
		} `json:"sources"`
		CheckedAt string `json:"checkedAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %+v", payload.Sources)
	}
	if payload.Sources[0].Repo != "decolua/9router" || payload.Sources[1].Repo != "luqman-v1/9router-go" {
		t.Errorf("repos = %+v", payload.Sources)
	}
	if payload.CheckedAt == "" {
		t.Error("checkedAt must be set")
	}
}

// Without a build-time baseline there is nothing to compare against, so the
// answer must be "nothing new" rather than every version ever published.
func TestUpstreamVersionsWithoutBaselineReportsNothingNew(t *testing.T) {
	stubGitHub(t)
	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	h := NewDashboardHandler(repo)

	rec := httptest.NewRecorder()
	h.HandleGetUpstreamVersions(rec, httptest.NewRequest(http.MethodGet, "/api/upstream-versions", nil))

	var payload struct {
		Sources []struct {
			HasNew bool `json:"hasNew"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %+v", payload.Sources)
	}
	for _, src := range payload.Sources {
		if src.HasNew {
			t.Errorf("no baseline must not report a new version: %+v", src)
		}
	}
}

func TestUpstreamChangelogRequiresParameters(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	h := NewDashboardHandler(repo)

	for name, query := range map[string]string{
		"no parameters": "",
		"no version":    "?source=upstream",
		"no source":     "?version=v1.0.0",
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.HandleGetUpstreamChangelog(rec, httptest.NewRequest(http.MethodGet, "/api/upstream-changelog"+query, nil))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

// A version that is not tag-shaped never reaches a URL, and that is a caller
// error rather than a gateway one.
func TestUpstreamChangelogRefusesAWalkableVersion(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	h := NewDashboardHandler(repo)

	rec := httptest.NewRecorder()
	h.HandleGetUpstreamChangelog(rec, httptest.NewRequest(http.MethodGet,
		"/api/upstream-changelog?source=upstream&version=..%2F..%2Fsecrets", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid version") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// A version the source does not have is a 404, not a gateway failure: the modal
// shows the difference between "you asked for something that does not exist" and
// "GitHub is unreachable".
func TestUpstreamChangelogReportsAnUnpublishedVersionAsNotFound(t *testing.T) {
	stubGitHub(t)
	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	h := NewDashboardHandler(repo)

	rec := httptest.NewRecorder()
	h.HandleGetUpstreamChangelog(rec, httptest.NewRequest(http.MethodGet,
		"/api/upstream-changelog?source=upstream&version=v9.9.9", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestUpstreamChangelogRefusesAnUntrackedSource(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	h := NewDashboardHandler(repo)

	rec := httptest.NewRecorder()
	h.HandleGetUpstreamChangelog(rec, httptest.NewRequest(http.MethodGet,
		"/api/upstream-changelog?source=somewhere-else&version=v1.0.0", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// The notes come from a repository this server does not control, and the modal
// renders them with {@html}. Raw tags must not survive the trip.
func TestUpstreamChangelogEscapesRawHTML(t *testing.T) {
	notes := escapeAngleBrackets("### Fix\n- <script>alert(1)</script> and a < b")
	if strings.Contains(notes, "<script") {
		t.Fatalf("raw tag survived: %q", notes)
	}
	if !strings.Contains(notes, "&lt;script") {
		t.Errorf("expected an escaped tag, got %q", notes)
	}
	// Markdown structure and a bare operator compare are untouched.
	if !strings.Contains(notes, "### Fix") || !strings.Contains(notes, "a &lt; b") {
		t.Errorf("markdown mangled: %q", notes)
	}
}
