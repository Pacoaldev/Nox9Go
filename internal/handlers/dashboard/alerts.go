package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"strings"
	"time"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlerutil"
)

// modelLockPrefix is the key prefix the chat path writes per-model locks
// under (db.LockConnectionModel). The same prefix is matched here so a model
// lock is reported as the alert it is.
const modelLockPrefix = "modelLock_"

// AlertCounts is the tally of the alerts in one answer.
type AlertCounts struct {
	Total        int `json:"total"`
	Locked       int `json:"locked"`
	Errored      int `json:"errored"`
	ModelLock    int `json:"modelLock"`
	ProviderLock int `json:"providerLock"`
}

// AlertDetail is one parked or failing connection, with the context the
// operator needs to act on it: which provider, which key, which model, and
// what the upstream said.
//
// The provider id travels raw. The display name is the dashboard's own
// catalog's business (web/src/lib/providers.ts), and duplicating that map
// here is how the two catalogs drift apart.
type AlertDetail struct {
	ConnectionID   string `json:"connectionId"`
	Scope          string `json:"scope,omitempty"` // "provider-model" for a pair lockout without a connection
	ConnectionName string `json:"connectionName"`
	Provider       string `json:"provider"`
	AlertType      string `json:"alertType"` // "lock" | "error"
	Message        string `json:"message"`
	Status         int    `json:"status,omitempty"`
	Model          string `json:"model,omitempty"`
	Source         string `json:"source,omitempty"`
	Timestamp      string `json:"timestamp"`
	RateLimitedAt  string `json:"rateLimitedAt,omitempty"`
	BackoffLevel   int    `json:"backoffLevel,omitempty"`
	TestStatus     string `json:"testStatus,omitempty"`
	APIKeyMasked   string `json:"apiKeyMasked,omitempty"`
	DisplayName    string `json:"displayName,omitempty"`
}

// AlertsResponse is the payload of GET /api/dashboard/alerts.
type AlertsResponse struct {
	Alerts      []AlertDetail `json:"alerts"`
	Counts      AlertCounts   `json:"counts"`
	GeneratedAt string        `json:"generatedAt"`
}

// HandleAlerts handles GET /api/dashboard/alerts.
//
// The status snapshot only counts the parked connections, so the bell could
// say "2 en bloqueo" and leave the operator to find which provider and which
// key were parked. This endpoint is the detail behind that count: one row per
// alert, naming the connection and carrying the error the upstream produced.
//
// The read is the same one the count already does — every connection row and
// its data blob — so the two cannot disagree about which connections alert.
func (h *DashboardHandler) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, counts := h.connectionAlerts(time.Now())

	handlerutil.WriteJSON(w, http.StatusOK, AlertsResponse{
		Alerts:      alerts,
		Counts:      counts,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// connectionAlerts collects every active alert, in the order the connections
// come back. A connection that is both rate-limited and model-locked reports
// both: they are separate parks, and hiding one behind the other would leave
// the operator fixing the wrong thing.
func (h *DashboardHandler) connectionAlerts(now time.Time) ([]AlertDetail, AlertCounts) {
	alerts := []AlertDetail{}
	var counts AlertCounts

	if h.Repo == nil {
		return alerts, counts
	}
	conns, err := h.Repo.GetProviderConnections("", false)
	if err != nil {
		return alerts, counts
	}

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

		connName := ""
		if c.Name != nil {
			connName = *c.Name
		}
		displayName := connName
		if dn, _ := data["displayName"].(string); dn != "" {
			displayName = dn
		}
		lastError, _ := data["lastError"].(map[string]any)

		// The account-scoped cooldown: a quota spent on the whole account.
		if until, ok := data["rateLimitedUntil"].(string); ok && futureTime(until, now) {
			alerts = append(alerts, AlertDetail{
				ConnectionID:   c.ID,
				ConnectionName: connName,
				Provider:       c.Provider,
				AlertType:      "lock",
				Message:        alertErrorMessage(lastError, "Rate limit exceeded"),
				Timestamp:      until,
				RateLimitedAt:  until,
				BackoffLevel:   intValue(data["backoffLevel"]),
				APIKeyMasked:   maskedKey(data),
				DisplayName:    displayName,
			})
			counts.Locked++
			counts.Total++
		}

		// The per-model locks: a quota spent on one model only.
		for key, value := range data {
			if !strings.HasPrefix(key, modelLockPrefix) {
				continue
			}
			stamp, _ := value.(string)
			if !futureTime(stamp, now) {
				continue
			}
			alerts = append(alerts, AlertDetail{
				ConnectionID:   c.ID,
				ConnectionName: connName,
				Provider:       c.Provider,
				AlertType:      "lock",
				Message:        alertErrorMessage(lastError, "Model locked due to quota/errors"),
				Model:          strings.TrimPrefix(key, modelLockPrefix),
				Timestamp:      stamp,
				RateLimitedAt:  stamp,
				BackoffLevel:   intValue(data["backoffLevel"]),
				APIKeyMasked:   maskedKey(data),
				DisplayName:    displayName,
			})
			counts.ModelLock++
			counts.Total++
		}

		// A failed probe: the credential did not pass its own test.
		status, _ := data["testStatus"].(string)
		if status != "error" && status != "failed" {
			continue
		}
		// Probe not supported is a limitation of the test suite, not a broken
		// credential — the same exclusion the count applies.
		if errText, _ := data["lastError"].(string); errText == "Provider test not supported" {
			continue
		}
		alerts = append(alerts, AlertDetail{
			ConnectionID:   c.ID,
			ConnectionName: connName,
			Provider:       c.Provider,
			AlertType:      "error",
			Message:        alertErrorMessage(lastError, "Connection test failed"),
			Status:         intValue(lastError["status"]),
			Model:          stringValue(lastError, "model"),
			Source:         stringValue(lastError, "source"),
			Timestamp:      stringValue(lastError, "timestamp"),
			TestStatus:     status,
			APIKeyMasked:   maskedKey(data),
			DisplayName:    displayName,
		})
		counts.Errored++
		counts.Total++
	}

	// Provider-model lockouts have no connection row by design: the quota is
	// shared across every key in the pair, so report one pair-level alert.
	if locks, err := h.Repo.ListProviderModelLocks(); err == nil {
		for _, lock := range locks {
			if !lock.LockedUntil.After(now) {
				continue
			}
			alerts = append(alerts, AlertDetail{
				Provider:      lock.Provider,
				AlertType:     "lock",
				Scope:         "provider-model",
				Message:       providerModelLockMessage(lock),
				Status:        lock.Status,
				Model:         lock.Model,
				Timestamp:     lock.LockedUntil.UTC().Format(time.RFC3339),
				RateLimitedAt: lock.LockedUntil.UTC().Format(time.RFC3339),
			})
			counts.ProviderLock++
			counts.Locked++
			counts.Total++
		}
	}

	return alerts, counts
}

func providerModelLockMessage(lock db.ProviderModelLock) string {
	if lock.Reason == "quota_exhausted" {
		return "Provider-model quota lockout"
	}
	return "Provider-model lockout"
}

// alertErrorMessage reads the message the upstream produced, falling back to
// the caller's description when the connection carries none.
func alertErrorMessage(lastError map[string]any, fallback string) string {
	if lastError == nil {
		return fallback
	}
	if message, _ := lastError["message"].(string); message != "" {
		return message
	}
	return fallback
}

func stringValue(source map[string]any, key string) string {
	if source == nil {
		return ""
	}
	value, _ := source[key].(string)
	return value
}

func intValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

// maskedKey returns the hint of the stored credential. The raw secret never
// leaves the gateway: the dashboard shows the same "sk-6…008f" shape the
// connections list already shows.
func maskedKey(data map[string]any) string {
	if masked, _ := data["apiKeyMasked"].(string); masked != "" {
		return masked
	}
	key, _ := data["apiKey"].(string)
	if len(key) <= 8 {
		return ""
	}
	return key[:4] + "..." + key[len(key)-4:]
}
