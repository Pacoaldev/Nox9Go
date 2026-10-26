package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"strings"
	"time"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlerutil"
)

// HandleStatus handles GET /api/dashboard/status.
//
// One compact snapshot feeds the whole top bar: the sidebar semaphore, the
// today widget and the alert badge. They used to poll separate endpoints, and
// two of those did not exist. Collapsing them into one request keeps the
// dashboard's poll budget on the 1 vCPU target (§4.G) — a snapshot is one small
// answer instead of three readers asking the same questions.
//
// Every field here is a cheap read. The database check is a ping, not a scan
// (the stats endpoint counts rows and computes reclaimable bytes); the tunnel
// state comes from the stored flags rather than HandleTunnelStatus, which
// shells out to `tailscale status` whenever that binary exists.
func (h *DashboardHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	// The handler answering at all is the gateway being up.
	databaseOK := h.Repo != nil
	if databaseOK {
		if err := h.Repo.RawDB().PingContext(r.Context()); err != nil {
			databaseOK = false
		}
	}

	tunnelEnabled, tunnelRunning := tunnelSnapshot(h.Repo)
	requestsToday, promptToday, completionToday := todayUsageTotals(h.Repo, time.Now())
	alertConnections, lockedConnections := connectionAlertCount(h.Repo)

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"gateway":  map[string]any{"ok": true},
		"database": map[string]any{"ok": databaseOK},
		"tunnel":   map[string]any{"enabled": tunnelEnabled, "running": tunnelRunning},
		"today": map[string]any{
			"requests":         requestsToday,
			"promptTokens":     promptToday,
			"completionTokens": completionToday,
		},
		"alerts": map[string]any{
			"connections": alertConnections,
			"locked":      lockedConnections,
		},
	})
}

// tunnelSnapshot reads the Cloudflare tunnel flags the dashboard stores. It
// never probes: a status poll must not spawn a process per tick.
func tunnelSnapshot(repo *db.Repo) (enabled, running bool) {
	if repo == nil {
		return false, false
	}
	raw, err := repo.GetSettingsRaw()
	if err != nil || raw == nil {
		return false, false
	}
	stored, _ := raw["tunnelEnabled"].(bool)
	tunnelURL, _ := raw["tunnelUrl"].(string)
	cf := cloudflareSnapshot(stored, tunnelURL)
	return cf.Enabled, cf.Running
}

// dailyUsageTotals is the numeric part of the daily rollup the request path
// already writes. Reading it is one indexed row, unlike folding usageHistory.
type dailyUsageTotals struct {
	Requests              int   `json:"requests"`
	PromptTokens          int64 `json:"promptTokens"`
	CompletionTokens      int64 `json:"completionTokens"`
	TotalRequests         int   `json:"totalRequests"`
	TotalPromptTokens     int64 `json:"totalPromptTokens"`
	TotalCompletionTokens int64 `json:"totalCompletionTokens"`
}

// todayUsageTotals reads today's rollup. The key is UTC, matching the writer in
// handlers/chat/usage.go. A missing row (no traffic yet today) is zero, not an
// error the caller has to distinguish from a real read failure.
//
// The payload carries both the short and the total* spellings; the newer total*
// names win when they are present, so a row written by either this build or the
// Next.js dashboard reports the same number.
func todayUsageTotals(repo *db.Repo, now time.Time) (requests int, promptTokens, completionTokens int64) {
	if repo == nil {
		return 0, 0, 0
	}
	data, err := repo.GetUsageDaily(now.UTC().Format("2006-01-02"))
	if err != nil || data == "" {
		return 0, 0, 0
	}
	var daily dailyUsageTotals
	if err := json.Unmarshal([]byte(data), &daily); err != nil {
		return 0, 0, 0
	}
	requests = daily.TotalRequests
	if requests == 0 {
		requests = daily.Requests
	}
	promptTokens = daily.TotalPromptTokens
	if promptTokens == 0 {
		promptTokens = daily.PromptTokens
	}
	completionTokens = daily.TotalCompletionTokens
	if completionTokens == 0 {
		completionTokens = daily.CompletionTokens
	}
	return requests, promptTokens, completionTokens
}

// connectionAlertCount counts the provider connections the data plane parked or
// flagged: a live rate-limit lock, or a failed probe. It mirrors what the
// provider page badges, and it is the honest source for the alert bell — there
// is no /api/providers/alerts route.
func connectionAlertCount(repo *db.Repo) (alerts, locked int) {
	if repo == nil {
		return 0, 0
	}
	conns, err := repo.GetProviderConnections("", false)
	if err != nil {
		return 0, 0
	}
	now := time.Now()
	for _, c := range conns {
		if c == nil || c.Data == "" {
			continue
		}
		// Inactive (disabled) connections are parked on purpose; they do not alert.
		if c.IsActive == 0 {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(c.Data), &data); err != nil || data == nil {
			continue
		}
		if connectionLockActive(data, now) {
			locked++
			continue
		}
		if status, _ := data["testStatus"].(string); status == "error" || status == "failed" {
			// Probe not supported is a limitation of the test suite, not a broken credential.
			if errText, _ := data["lastError"].(string); errText == "Provider test not supported" {
				continue
			}
			alerts++
		}
	}

	// Provider-model lockouts are the other reason a turn is refused.
	if locks, err := repo.ListProviderModelLocks(); err == nil {
		for _, lock := range locks {
			if lock.LockedUntil.After(now) {
				locked++
			}
		}
	}

	return alerts, locked
}

// connectionLockActive reports whether a connection carries a live lock. Two
// keys park one: rateLimitedUntil, and the per-model modelLock_* stamps the chat
// path writes. Both are future timestamps.
func connectionLockActive(data map[string]any, now time.Time) bool {
	if until, ok := data["rateLimitedUntil"].(string); ok && futureTime(until, now) {
		return true
	}
	for key, value := range data {
		if !strings.HasPrefix(key, "modelLock_") {
			continue
		}
		if stamp, ok := value.(string); ok && futureTime(stamp, now) {
			return true
		}
	}
	return false
}

func futureTime(value string, now time.Time) bool {
	if value == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return false
	}
	return t.After(now)
}