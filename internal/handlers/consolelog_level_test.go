package handlers

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nox9go/proxy/internal/log"
)

func TestConsoleLogsLevelGetPut(t *testing.T) {
	prev := log.GetLevel()
	defer log.SetLevel(prev)

	req := httptest.NewRequest(http.MethodGet, "/translator/console-logs/level", nil)
	rec := httptest.NewRecorder()
	HandleConsoleLogsLevelGet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET level expected 200, got %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal GET body: %v", err)
	}
	if got["success"] != true || got["level"] != log.LevelString() {
		t.Fatalf("unexpected GET body: %v", got)
	}

	for _, lvl := range []string{"debug", "warn", "error", "info"} {
		req = httptest.NewRequest(http.MethodPut, "/translator/console-logs/level",
			strings.NewReader(`{"level":"`+lvl+`"}`))
		rec = httptest.NewRecorder()
		HandleConsoleLogsLevelPut(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT level=%s expected 200, got %d: %s", lvl, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("failed to unmarshal PUT body: %v", err)
		}
		if out["level"] != lvl || log.LevelString() != lvl {
			t.Fatalf("level not applied: body=%v current=%s", out, log.LevelString())
		}
	}

	// Invalid level must be rejected without changing the current level.
	req = httptest.NewRequest(http.MethodPut, "/translator/console-logs/level",
		strings.NewReader(`{"level":"verbose"}`))
	rec = httptest.NewRecorder()
	HandleConsoleLogsLevelPut(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid level expected 400, got %d", rec.Code)
	}
	if log.LevelString() != "info" {
		t.Fatalf("invalid PUT changed level to %s", log.LevelString())
	}
}
