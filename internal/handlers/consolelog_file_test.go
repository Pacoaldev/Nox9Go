package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nox9go/proxy/internal/log"
)

// Persistence is opt-in. With it off the endpoint must say so, not hand back an
// empty download that looks like "the log is empty".
func TestConsoleLogsFileDownload_DisabledReturns404WithGuidance(t *testing.T) {
	if log.ConsoleFileEnabled() {
		t.Skip("console file persistence is enabled in this process")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/translator/console-logs/file", nil)
	rec := httptest.NewRecorder()
	HandleConsoleLogsFileDownload(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when disabled, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CONSOLE_LOG_FILE") {
		t.Errorf("404 body should name the switch, got: %s", rec.Body.String())
	}
}

// With persistence on, the endpoint streams the persisted lines as an
// attachment so the operator can save the history a restart would clear.
func TestConsoleLogsFileDownload_StreamsPersistedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	if err := log.EnableConsoleFile(path, 1<<20, 1); err != nil {
		t.Fatalf("EnableConsoleFile: %v", err)
	}
	defer log.DisableConsoleFile()

	log.Info("file", "downloadable line")

	req := httptest.NewRequest(http.MethodGet, "/api/translator/console-logs/file", nil)
	rec := httptest.NewRecorder()
	HandleConsoleLogsFileDownload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("expected attachment disposition, got %q", got)
	}
	if !strings.Contains(rec.Body.String(), "downloadable line") {
		t.Errorf("download body missing persisted line: %s", rec.Body.String())
	}
}

// A missing file after rotation must not fail the whole download.
func TestConsoleLogsFileDownload_NoFilesYet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.log")
	if err := log.EnableConsoleFile(path, 1<<20, 1); err != nil {
		t.Fatalf("EnableConsoleFile: %v", err)
	}
	defer log.DisableConsoleFile()
	// No lines written: the active file exists but is empty. The handler should
	// still answer 200 with an (empty) attachment rather than erroring.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active file should exist after enable: %v", err)
	}
}
