package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// Token Harbor (catalog id `tokenharbor`, aliases `th` and `thh`) publishes the
// allowance its own CLI reads, and nothing else:
//
//	GET https://tokenharbor.ai/api/cli/free-tier
//	{ "plan_name": null, "on_plan": false, "used_pct": 41,
//	  "req_used": 88, "rpm_limit": 1000000, "exhausted": false,
//	  "reset_at": "2026-11-19T09:13:26.225039+00:00",
//	  "period_ends_at": "2026-11-19T09:13:26.225039+00:00",
//	  "plan_used_pct": 0, "plan_exhausted": false }
//
// Their docs are explicit that no key-management API exists (`no /v1/keys/...
// endpoints`) and the session-guarded dashboard routes (`/api/wallet/live`,
// `/api/keys`) reject an API key with 401, so this read is the only metered
// source reachable with a connection's credential. It reports a percentage:
// `used_pct` is the share of the free allowance spent in the current period,
// and `plan_used_pct` the share of the Pass window when the account holds one.
const tokenHarborPlanLabel = "Token Harbor"

// var, not const, so tests can point the fetcher at a local server.
var tokenHarborUsageURL = "https://tokenharbor.ai/api/cli/free-tier"

// fetchTokenHarborUsage reads the free-tier and Pass allowance of one Token
// Harbor API key.
func fetchTokenHarborUsage(ctx context.Context, apiKey string) usageResult {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return usageResult{plan: tokenHarborPlanLabel, message: "Token Harbor API key not available. Add a key to view usage."}
	}
	status, _, out, err := usageGet(ctx, tokenHarborUsageURL, map[string]string{
		"Authorization": "Bearer " + key,
		"Accept":        "application/json",
	})
	if err != nil {
		return usageResult{plan: tokenHarborPlanLabel, message: fmt.Sprintf("Token Harbor connected. Unable to fetch usage: %v", err)}
	}
	switch {
	case status == http.StatusUnauthorized:
		return usageResult{plan: tokenHarborPlanLabel, message: "Invalid Token Harbor API key."}
	case status < 200 || status >= 300:
		return usageResult{plan: tokenHarborPlanLabel, message: fmt.Sprintf("Token Harbor quota API error (%d)", status)}
	}
	return parseTokenHarborUsage(usageJSON(out))
}

// parseTokenHarborUsage maps the `/api/cli/free-tier` payload into percentage
// rows. The free allowance is always reported; the Pass row is added only when
// the account is on a plan, so an account without one does not show an empty
// window it can never fill.
func parseTokenHarborUsage(report map[string]any) usageResult {
	unavailable := usageResult{plan: tokenHarborPlanLabel, message: "Token Harbor quota unavailable: unusable allowance payload."}
	if report == nil {
		return unavailable
	}
	usedPct, ok := usageFiniteNum(report["used_pct"])
	if !ok {
		return unavailable
	}
	spent, _ := report["exhausted"].(bool)
	resetAt := usageResetTimeToNil(usageResetTime(report["reset_at"]))

	quotas := map[string]any{
		"Free allowance": tokenHarborQuotaRow(usedPct, spent, resetAt),
	}

	if onPlan, _ := report["on_plan"].(bool); onPlan {
		planPct, ok := usageFiniteNum(report["plan_used_pct"])
		if !ok {
			return unavailable
		}
		planSpent, _ := report["plan_exhausted"].(bool)
		label := strings.TrimSpace(usageStr(report["plan_name"]))
		if label == "" {
			label = "Pass"
		}
		quotas[label] = tokenHarborQuotaRow(planPct, planSpent, usageResetTimeToNil(usageResetTime(report["period_ends_at"])))
	}
	return usageResult{plan: tokenHarborPlanLabel, quotas: quotas}
}

// tokenHarborQuotaRow converts a 0..100 used share into the row shape the
// dashboard draws. `used_pct` arrives as a whole percentage and is clamped, so
// a payload that overshoots can never paint a negative remainder. The spent flag
// wins over the share: the docs define it as "this allowance is used up", and a
// percentage that lags behind it would otherwise show room that no request can
// spend.
func tokenHarborQuotaRow(usedPct float64, spent bool, resetAt any) map[string]any {
	used := math.Round(math.Max(0, math.Min(100, usedPct))*100) / 100
	if spent {
		used = 100
	}
	return map[string]any{
		"used":                used,
		"total":               float64(100),
		"remainingPercentage": 100 - used,
		"resetAt":             resetAt,
		"unlimited":           false,
	}
}
