package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
)

// NaN.Builders (catalog id `nan`, alias `nanbuilders`) publishes the per-model
// token counters its dashboard shows:
//
//	GET https://cloud-api.nan.builders/api/usage/quota
//	{ "models": [ { "model": "glm5.3", "tokensUsed": 2500, "cap": 10000,
//	                "periodEnd": "2026-12-01T00:00:00Z" }, ... ] }
//
// The `/v1/usage` history endpoint on the API host can answer empty for an
// active key, so these dashboard counters are the source of truth. Every entry
// becomes one token quota that resets at `periodEnd`. A malformed entry makes
// the whole report unavailable: a partially-guessed panel is worse than saying
// the read failed.
const nanPlanLabel = "NaN.Builders"

// var, not const, so tests can point the fetcher at a local server.
var nanUsageURL = "https://cloud-api.nan.builders/api/usage/quota"

// fetchNanUsage reads the per-model token caps of one NaN.Builders API key.
func fetchNanUsage(ctx context.Context, apiKey string) usageResult {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return usageResult{plan: nanPlanLabel, message: "NaN.Builders API key not available. Add a key to view usage."}
	}
	status, _, out, err := usageGet(ctx, nanUsageURL, map[string]string{
		"Authorization": "Bearer " + key,
		"Accept":        "application/json",
	})
	if err != nil {
		return usageResult{plan: nanPlanLabel, message: fmt.Sprintf("NaN.Builders connected. Unable to fetch usage: %v", err)}
	}
	switch {
	case status == http.StatusUnauthorized:
		return usageResult{plan: nanPlanLabel, message: "Invalid NaN.Builders API key."}
	case status < 200 || status >= 300:
		return usageResult{plan: nanPlanLabel, message: fmt.Sprintf("NaN.Builders quota API error (%d)", status)}
	}
	return parseNanUsage(usageJSON(out))
}

// parseNanUsage maps the `/api/usage/quota` payload into per-model token rows.
// Rows are keyed and ordered by model id so the card does not reshuffle between
// reads; a provider map has no order of its own.
func parseNanUsage(report map[string]any) usageResult {
	unavailable := usageResult{plan: nanPlanLabel, message: "NaN.Builders quota unavailable: missing or invalid model counters."}
	if report == nil {
		return unavailable
	}
	rows, ok := report["models"].([]any)
	if !ok || len(rows) == 0 {
		return unavailable
	}

	quotas := make(map[string]any, len(rows))
	for _, raw := range rows {
		model, row, ok := nanQuotaRow(raw, quotas)
		if !ok {
			return unavailable
		}
		quotas[model] = row
	}
	names := make([]string, 0, len(quotas))
	for name := range quotas {
		names = append(names, name)
	}
	sort.Strings(names)
	ordered := make(map[string]any, len(names))
	for _, name := range names {
		ordered[name] = quotas[name]
	}
	return usageResult{plan: nanPlanLabel, quotas: ordered}
}

// nanQuotaRow validates one `models[]` entry and converts it to a quota row. A
// duplicate model id is malformed rather than an overwrite, so the first count a
// key reports cannot be replaced by a second one for the same model.
func nanQuotaRow(raw any, seen map[string]any) (string, map[string]any, bool) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return "", nil, false
	}
	model := strings.TrimSpace(usageStr(entry["model"]))
	if model == "" {
		return "", nil, false
	}
	if _, dup := seen[model]; dup {
		return "", nil, false
	}
	used, ok := usageFiniteNum(entry["tokensUsed"])
	if !ok || used < 0 {
		return "", nil, false
	}
	cap, ok := usageFiniteNum(entry["cap"])
	if !ok || cap <= 0 {
		return "", nil, false
	}
	resetAt := ""
	if rawEnd, present := entry["periodEnd"]; present && rawEnd != nil {
		resetAt = usageResetTime(rawEnd)
		if resetAt == "" {
			return "", nil, false
		}
	}
	remaining := math.Max(0, cap-used)
	// No `remaining` key: every fetcher reports a share here, and the dashboard
	// reads `remaining` as a percentage (Codex) — a token count in that field
	// would paint 497500000%. The counts ride on used/total.
	return model, map[string]any{
		"used":                used,
		"total":               cap,
		"remainingPercentage": math.Round(remaining/cap*10_000) / 100,
		"resetAt":             usageResetTimeToNil(resetAt),
		"displayName":         model + " (tokens)",
		"unit":                "tokens",
		"unlimited":           false,
	}, true
}
