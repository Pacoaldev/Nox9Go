package dashboard

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/dbtest"
	"nox9go/proxy/internal/vault"
)

// newTelegramDashboardRepo builds a repository with the Telegram tables and a
// vault, so these tests exercise the real storage contract.
func newTelegramDashboardRepo(t *testing.T) *db.Repo {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := dbtest.CreateTables(conn); err != nil {
		t.Fatalf("CreateTables() = %v", err)
	}
	if err := db.EnsureTelegramChannelTables(conn); err != nil {
		t.Fatalf("EnsureTelegramChannelTables() = %v", err)
	}
	key := make([]byte, vault.KeySize)
	for i := range key {
		key[i] = byte(i + 11)
	}
	v, err := vault.New(key)
	if err != nil {
		t.Fatalf("vault.New() = %v", err)
	}
	repo := db.NewRepo(conn)
	repo.SetVault(v)
	return repo
}

func getTelegramStatus(t *testing.T, h *DashboardHandler) telegramStatus {
	t.Helper()
	rec := httptest.NewRecorder()
	h.HandleGetTelegramChannel(rec, httptest.NewRequest(http.MethodGet, "/api/channels/telegram", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got telegramStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return got
}

// S1: a fresh channel reports the reason it is stopped, so the tab tells the
// operator what to do instead of showing a bare "off".
func TestTelegramChannel_StateReportsMissingPiece(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	got := getTelegramStatus(t, h)
	if got.State != "no_token" {
		t.Errorf("State = %q on a fresh channel, want no_token", got.State)
	}
	if got.Enabled {
		t.Errorf("Enabled = true on a fresh channel, want false")
	}
}

// S3: with a token but no allowlist the channel is stopped and says so. This is
// the fail-closed gate surfaced to the operator.
func TestTelegramChannel_NoAllowlistIsReported(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	body := `{"token":"123456:secret"}`
	rec := httptest.NewRecorder()
	h.HandleSetTelegramToken(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/token", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("token save status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	got := getTelegramStatus(t, h)
	if got.State != "no_allowlist" {
		t.Errorf("State = %q with a token but no allowlist, want no_allowlist", got.State)
	}
	if !got.TokenPresent {
		t.Errorf("TokenPresent = false after saving a token")
	}
}

// S2: the token is never returned in clear. A dashboard read must not be able to
// exfiltrate the secret it just stored.
func TestTelegramChannel_TokenIsMaskedInResponse(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	rec := httptest.NewRecorder()
	h.HandleSetTelegramToken(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/token",
		strings.NewReader(`{"token":"123456:super-secret-value"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("token save status = %d, want 200", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "super-secret-value") {
		t.Errorf("response body contains the raw token: %s", rec.Body.String())
	}
}

// S2: a malformed token is rejected at the form, not accepted and then failing
// as a confusing 401 from Telegram minutes later.
func TestTelegramChannel_RejectsMalformedToken(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	for _, token := range []string{"", "   ", "not-a-token", "123456", "123456:", ":secret", "abc:def"} {
		rec := httptest.NewRecorder()
		h.HandleSetTelegramToken(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/token",
			strings.NewReader(`{"token":"`+token+`"}`)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("token %q status = %d, want 400", token, rec.Code)
		}
	}
}

// S3: an allowlist alone does not enable the channel; both conditions are needed.
func TestTelegramChannel_AllowlistAloneDoesNotEnable(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	rec := httptest.NewRecorder()
	h.HandleSetTelegramAllowlist(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/allowlist",
		strings.NewReader(`{"userIds":[42]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowlist save status = %d, want 200", rec.Code)
	}

	got := getTelegramStatus(t, h)
	if got.Enabled {
		t.Errorf("Enabled = true with an allowlist but no token, want false")
	}
	if got.State != "no_token" {
		t.Errorf("State = %q, want no_token", got.State)
	}
}

// S1: both conditions met enables the channel and reports it running.
func TestTelegramChannel_EnabledWithTokenAndAllowlist(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	rec := httptest.NewRecorder()
	h.HandleSetTelegramToken(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/token",
		strings.NewReader(`{"token":"123456:secret"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("token save status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleSetTelegramAllowlist(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/allowlist",
		strings.NewReader(`{"userIds":[42]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowlist save status = %d", rec.Code)
	}

	got := getTelegramStatus(t, h)
	if !got.Enabled {
		t.Errorf("Enabled = false with a token and an allowlist, want true")
	}
	if got.State != "running" {
		t.Errorf("State = %q, want running", got.State)
	}
	if len(got.AllowedUserIDs) != 1 || got.AllowedUserIDs[0] != 42 {
		t.Errorf("AllowedUserIDs = %v, want [42]", got.AllowedUserIDs)
	}
}

// S3: the allowlist can be emptied. Refusing an empty list would leave no way to
// revoke everyone, which is the first thing an operator needs on a leak.
func TestTelegramChannel_AllowlistCanBeEmptied(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	rec := httptest.NewRecorder()
	h.HandleSetTelegramAllowlist(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/allowlist",
		strings.NewReader(`{"userIds":[42,43]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowlist save status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.HandleSetTelegramAllowlist(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/allowlist",
		strings.NewReader(`{"userIds":[]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty allowlist save status = %d, want 200", rec.Code)
	}

	got := getTelegramStatus(t, h)
	if len(got.AllowedUserIDs) != 0 {
		t.Errorf("AllowedUserIDs = %v after emptying, want none", got.AllowedUserIDs)
	}
}

// S3: a negative or zero user id is rejected. Telegram ids are positive, so a
// zero would authorize nobody while looking like a configured entry.
func TestTelegramChannel_RejectsNonPositiveUserID(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	for _, body := range []string{`{"userIds":[0]}`, `{"userIds":[-5]}`, `{"userIds":[42,0]}`} {
		rec := httptest.NewRecorder()
		h.HandleSetTelegramAllowlist(rec, httptest.NewRequest(http.MethodPut, "/api/channels/telegram/allowlist",
			strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s status = %d, want 400", body, rec.Code)
		}
	}
}

// S5: with no checkout configured the endpoint answers a plain explanation
// instead of an empty report a client would render as a healthy, idle repo.
func TestHandleDevStatus_Unconfigured(t *testing.T) {
	t.Setenv("DEV_REPO_ROOT", "")
	h := &DashboardHandler{}

	rec := httptest.NewRecorder()
	h.HandleDevStatus(rec, httptest.NewRequest(http.MethodGet, "/api/dev/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if configured, _ := body["configured"].(bool); configured {
		t.Errorf("configured = true with no DEV_REPO_ROOT")
	}
}

// S5: a configured checkout returns the branch, the head commit and the
// rendered report the dashboard shows.
func TestHandleDevStatus_ReportsRepository(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")
	writeAt(t, dir, "a.txt", "one")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "feat: dashboard visible")
	t.Setenv("DEV_REPO_ROOT", dir)
	t.Setenv("DEV_FEATURE", "")

	h := &DashboardHandler{}
	rec := httptest.NewRecorder()
	h.HandleDevStatus(rec, httptest.NewRequest(http.MethodGet, "/api/dev/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if configured, _ := body["configured"].(bool); !configured {
		t.Fatalf("configured = false with a real checkout")
	}
	if got, _ := body["headSubject"].(string); got != "feat: dashboard visible" {
		t.Errorf("headSubject = %q, want the head commit", got)
	}
	if got, _ := body["rendered"].(string); !strings.Contains(got, "Rama:") {
		t.Errorf("rendered = %q, want the report body", got)
	}
}

// S5: the feature can be selected per request, so the dashboard can show a
// different ODD document without restarting the gateway.
func TestHandleDevStatus_FeatureQuery(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")
	writeAt(t, dir, "odd/tasks/picked.md", "# Picked\n\n## Tasks\n\n| ID | Specs | Route | Commit |\n| :- | :---- | :---- | :----- |\n| T1 | S1 | work | abc123 |\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "docs: feature")
	t.Setenv("DEV_REPO_ROOT", dir)
	t.Setenv("DEV_FEATURE", "ignored")

	h := &DashboardHandler{}
	rec := httptest.NewRecorder()
	h.HandleDevStatus(rec, httptest.NewRequest(http.MethodGet, "/api/dev/status?feature=picked", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	feature, ok := body["feature"].(map[string]any)
	if !ok {
		t.Fatalf("feature missing from the payload: %v", body)
	}
	if got, _ := feature["name"].(string); got != "picked" {
		t.Errorf("feature.name = %q, want the query parameter to win", got)
	}
	if got, _ := feature["done"].(float64); got != 1 {
		t.Errorf("feature.done = %v, want 1", got)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeAt(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// S10: the dashboard names the active mode, so the operator can see which
// behaviour free text gets without opening the bot.
func TestTelegramChannel_ModeReported(t *testing.T) {
	h := NewDashboardHandler(newTelegramDashboardRepo(t))

	// Defaults to chat.
	if got := getTelegramStatus(t, h); got.Mode != "chat" {
		t.Errorf("default mode = %q, want chat", got.Mode)
	}

	putMode := func(body string) {
		rec := httptest.NewRecorder()
		h.HandleSetTelegramMode(rec, httptest.NewRequest(http.MethodPut,
			"/api/channels/telegram/mode", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("mode save status = %d", rec.Code)
		}
	}

	putMode(`{"mode":"agent"}`)
	if got := getTelegramStatus(t, h); got.Mode != "agent" {
		t.Errorf("mode = %q after setting agent, want agent", got.Mode)
	}

	putMode(`{"mode":"chat"}`)
	if got := getTelegramStatus(t, h); got.Mode != "chat" {
		t.Errorf("mode = %q after setting chat, want chat", got.Mode)
	}
}
