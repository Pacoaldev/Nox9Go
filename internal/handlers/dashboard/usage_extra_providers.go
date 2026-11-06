package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Additional live quota fetchers ported from upstream
// open-sse/services/usage/{github,glm,kimi,minimax,opencode-go,codebuddy-cn,xiaomi-mimo,zed}.js.
//
// Each provider below was listed in usageSupportedProviders but had no case in
// fetchProviderUsage, so its connections rendered "Account active. No quota
// limits tracked." while upstream did return real quota rows. These fetchers
// close that gap; the dashboard parsers for every one of them already exist in
// web/src/components/quota/types.ts.

// ---------- github (GitHub Copilot): GET copilot_internal/user ----------

// Vars, not consts, so tests can point every fetcher at a local server.
var (
	githubUsageURL      = "https://api.github.com/copilot_internal/user"
	githubUsageDo       = usageDo
	glmUsageURL         = "https://api.z.ai/api/monitor/usage/quota/limit"
	glmCnUsageURL       = "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
	kimiUsageURL        = "https://api.kimi.com/coding/v1/usages"
	openCodeGoUsageURL  = "https://opencode.ai/zen/go/v1/usage"
	codeBuddyCnUsageURL = "https://copilot.tencent.com/v2/billing/meter/get-user-resource"
	xiaomiMimoUsageURL  = "https://aistudio.xiaomimimo.com/open-apis/v1/user/usage"
	zedUsageURL         = "https://cloud.zed.dev/client/users/me"
)

func fetchGitHubUsage(ctx context.Context, accessToken string) usageResult {
	if strings.TrimSpace(accessToken) == "" {
		return usageResult{message: "No GitHub access token available. Please re-authorize the connection."}
	}
	status, _, out, err := githubUsageDo(ctx, http.MethodGet, githubUsageURL, map[string]string{
		// GitHub's quota endpoint expects the `token <pat>` scheme, not Bearer.
		"Authorization":         "token " + strings.TrimSpace(accessToken),
		"Accept":                "application/json",
		"X-GitHub-Api-Version":  "2022-11-28",
		"User-Agent":            "GitHubCopilotChat/0.26.7",
		"Editor-Version":        "vscode/1.100.0",
		"Editor-Plugin-Version": "copilot-chat/0.26.7",
	}, nil)
	if err != nil {
		return usageResult{message: fmt.Sprintf("Failed to fetch GitHub usage: %v", err)}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usageResult{message: "GitHub Copilot authentication failed. Please re-authorize the connection."}
	}
	if status < 200 || status >= 300 {
		return usageResult{plan: "GitHub Copilot", message: fmt.Sprintf("GitHub API error (%d).", status)}
	}
	data := usageJSON(out)
	if data == nil {
		return usageResult{message: "GitHub Copilot connected. Unable to parse quota data."}
	}
	plan := strings.TrimSpace(usageStr(data["copilot_plan"]))

	if snapshots, ok := data["quota_snapshots"].(map[string]any); ok {
		resetAt := usageResetTime(data["quota_reset_date"])
		quotas := map[string]any{}
		addGitHubSnapshot(quotas, "chat", "Chat", snapshots["chat"], resetAt)
		addGitHubSnapshot(quotas, "completions", "Completions", snapshots["completions"], resetAt)
		addGitHubSnapshot(quotas, "premium_interactions", "Premium Interactions", snapshots["premium_interactions"], resetAt)
		return usageResult{plan: firstNonEmptyStr(plan, "GitHub Copilot"), quotas: quotas}
	}

	if monthly, ok := data["monthly_quotas"].(map[string]any); ok {
		resetAt := usageResetTime(data["limited_user_reset_date"])
		used, _ := data["limited_user_quotas"].(map[string]any)
		quotas := map[string]any{}
		addGitHubCountedQuota(quotas, "Chat", monthly["chat"], used, "chat", resetAt)
		addGitHubCountedQuota(quotas, "Completions", monthly["completions"], used, "completions", resetAt)
		return usageResult{plan: firstNonEmptyStr(plan, "GitHub Copilot"), quotas: quotas}
	}

	return usageResult{plan: firstNonEmptyStr(plan, "GitHub Copilot"), message: "GitHub Copilot connected. Unable to parse quota data."}
}

// addGitHubSnapshot renders one paid-plan snapshot: {entitlement, remaining,
// unlimited}. `used` is derived because the API reports the remaining side.
func addGitHubSnapshot(quotas map[string]any, key, label string, raw any, resetAt string) {
	snap, ok := raw.(map[string]any)
	if !ok {
		return
	}
	unlimited, _ := snap["unlimited"].(bool)
	total := usageNum(snap["entitlement"], 0)
	remaining := usageNum(snap["remaining"], 0)
	quotas[key] = map[string]any{
		"name":                label,
		"used":                math.Max(0, total-remaining),
		"total":               total,
		"remaining":           remaining,
		"remainingPercentage": percentOrZero(remaining, total),
		"resetAt":             usageResetTimeToNil(resetAt),
		"unlimited":           unlimited,
	}
}

func addGitHubCountedQuota(quotas map[string]any, label string, totalRaw any, usedMap map[string]any, usedKey, resetAt string) {
	total := usageNum(totalRaw, 0)
	used := 0.0
	if usedMap != nil {
		used = usageNum(usedMap[usedKey], 0)
	}
	if total <= 0 && used <= 0 {
		return
	}
	key := strings.ToLower(strings.ReplaceAll(label, " ", "_"))
	quotas[key] = map[string]any{
		"name":                label,
		"used":                math.Max(0, used),
		"total":               total,
		"remaining":           math.Max(0, total-used),
		"remainingPercentage": percentOrZero(math.Max(0, total-used), total),
		"resetAt":             usageResetTimeToNil(resetAt),
		"unlimited":           false,
	}
}

func percentOrZero(remaining, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Max(0, math.Min(100, remaining/total*100))
}

// ---------- glm / glm-cn: GET monitor/usage/quota/limit ----------

func fetchGlmUsage(ctx context.Context, provider, apiKey string) usageResult {
	if strings.TrimSpace(apiKey) == "" {
		return usageResult{message: "GLM API key not available."}
	}
	url := glmUsageURL
	if provider == "glm-cn" {
		url = glmCnUsageURL
	}
	status, _, out, err := usageGet(ctx, url, map[string]string{
		"Authorization": "Bearer " + strings.TrimSpace(apiKey),
		"Accept":        "application/json",
	})
	if err != nil {
		return usageResult{message: fmt.Sprintf("GLM error: %v", err)}
	}
	if status == http.StatusUnauthorized {
		return usageResult{message: "GLM API key invalid or expired."}
	}
	if status < 200 || status >= 300 {
		return usageResult{plan: "GLM", message: fmt.Sprintf("GLM quota API error (%d).", status)}
	}
	return parseGlmQuota(usageJSON(out))
}

// parseGlmQuota ports parseGlmQuotaResponse: TOKENS_LIMIT / CREDIT_LIMIT rows,
// labelled dynamically by unit (3 → session, 6 → weekly).
func parseGlmQuota(data map[string]any) usageResult {
	inner, _ := data["data"].(map[string]any)
	limits, _ := inner["limits"].([]any)
	quotas := map[string]any{}
	for _, raw := range limits {
		limit, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := usageStr(limit["type"])
		if typ != "TOKENS_LIMIT" && typ != "CREDIT_LIMIT" {
			continue
		}
		usedPercent := usageNum(limit["percentage"], 0)
		resetMs := usageNum(limit["nextResetTime"], 0)
		remaining := math.Max(0, 100-usedPercent)

		unit := usageNum(limit["unit"], 0)
		number := usageNum(limit["number"], 0)
		key := "session"
		switch {
		case unit == 3:
			key = fmt.Sprintf("Session (%dh)", int(number))
		case unit == 6:
			key = "Weekly (7d)"
		case typ == "TOKENS_LIMIT":
			key = "Tokens"
		default:
			key = fmt.Sprintf("Limit (%d)", int(number))
		}

		resetAt := ""
		if resetMs > 0 {
			resetAt = time.UnixMilli(int64(resetMs)).UTC().Format(time.RFC3339)
		}
		quotas[key] = map[string]any{
			"used":                usedPercent,
			"total":               float64(100),
			"remaining":           remaining,
			"remainingPercentage": remaining,
			"resetAt":             usageResetTimeToNil(resetAt),
			"unlimited":           false,
		}
	}
	plan := "Unknown"
	if level := usageStr(inner["level"]); strings.TrimSpace(level) != "" {
		l := strings.TrimSpace(level)
		plan = strings.ToUpper(l[:1]) + strings.ToLower(l[1:])
	}
	return usageResult{plan: plan, quotas: quotas}
}

// ---------- kimi: GET coding/v1/usages ----------

var kimiPlanLevels = map[string]string{
	"LEVEL_BASIC":        "Moderato",
	"LEVEL_INTERMEDIATE": "Allegretto",
	"LEVEL_ADVANCED":     "Allegro",
	"LEVEL_STANDARD":     "Vivace",
}

func fetchKimiUsage(ctx context.Context, accessToken, apiKey string, psd map[string]any) usageResult {
	headers := map[string]string{"Accept": "application/json", "Content-Type": "application/json"}
	switch {
	case strings.TrimSpace(apiKey) != "":
		headers["x-api-key"] = strings.TrimSpace(apiKey)
	case strings.TrimSpace(accessToken) != "":
		headers["Authorization"] = "Bearer " + strings.TrimSpace(accessToken)
		for k, v := range buildKimiUsageHeaders(psdStr(psd, "deviceId")) {
			headers[k] = v
		}
	default:
		return usageResult{message: "Kimi access token or API key not available."}
	}

	status, _, out, err := usageGet(ctx, kimiUsageURL, headers)
	if err != nil {
		return usageResult{message: fmt.Sprintf("Kimi Coding connected. Unable to fetch usage: %v", err)}
	}
	if status < 200 || status >= 300 {
		return usageResult{plan: "Kimi Coding", message: formatKimiUsageError(status, string(out))}
	}
	data := usageJSON(out)
	if data == nil {
		return usageResult{plan: "Kimi Coding", message: "Kimi Coding connected. Invalid JSON response from API."}
	}

	quotas := map[string]any{}
	if u, ok := data["usage"].(map[string]any); ok {
		limit := usageNum(firstNonEmptyAny(u["limit"], u["Limit"]), 0)
		used := usageNum(firstNonEmptyAny(u["used"], u["Used"]), 0)
		remaining := -1.0
		if raw := firstNonEmptyAny(u["remaining"], u["Remaining"]); raw != nil {
			remaining = usageNum(raw, -1)
		}
		resetRaw := firstNonEmptyAny(u["resetTime"], u["ResetTime"], u["reset_at"], u["resetAt"])
		if limit > 0 {
			quotas["Weekly"] = kimiQuota(used, limit, remaining, usageResetTime(resetRaw))
		}
	}
	if limits, ok := data["limits"].([]any); ok {
		for _, raw := range limits {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			detail, _ := item["detail"].(map[string]any)
			limit := usageNum(firstNonEmptyAny(detail["limit"], detail["Limit"]), 0)
			remainingRaw := firstNonEmptyAny(detail["remaining"], detail["Remaining"])
			resetRaw := firstNonEmptyAny(detail["resetTime"], detail["reset_at"], detail["resetAt"])
			if limit > 0 {
				rem := limit
				if remainingRaw != nil {
					rem = usageNum(remainingRaw, limit)
				}
				quotas["Ratelimit"] = kimiQuota(math.Max(0, limit-rem), limit, rem, usageResetTime(resetRaw))
			}
		}
	}

	plan := "Kimi Coding"
	if user, ok := data["user"].(map[string]any); ok {
		if membership, ok := user["membership"].(map[string]any); ok {
			if name := getKimiPlanName(usageStr(membership["level"])); name != "" {
				plan = name
			}
		}
	}
	if len(quotas) == 0 {
		return usageResult{plan: plan, message: "Kimi Coding connected. Usage tracked per request."}
	}
	return usageResult{plan: plan, quotas: quotas}
}

// kimiQuota mirrors upstream makeQuota: never set absolute `remaining`.
func kimiQuota(used, total, remaining float64, resetAt string) map[string]any {
	var pct float64
	switch {
	case total > 0 && remaining >= 0:
		pct = math.Max(0, remaining) / total * 100
	case total > 0:
		pct = math.Max(0, total-used) / total * 100
	}
	return map[string]any{
		"used":                math.Max(0, used),
		"total":               math.Max(0, total),
		"remainingPercentage": pct,
		"resetAt":             usageResetTimeToNil(resetAt),
		"unlimited":           false,
	}
}

func getKimiPlanName(level string) string {
	level = strings.TrimSpace(level)
	if level == "" {
		return ""
	}
	if name, ok := kimiPlanLevels[level]; ok {
		return name
	}
	return strings.ToLower(strings.TrimPrefix(level, "LEVEL_"))
}

func formatKimiUsageError(status int, body string) string {
	parsed := usageJSON([]byte(body))
	if status == http.StatusUnauthorized {
		return "Kimi authentication expired. Please re-authorize."
	}
	if status == http.StatusForbidden {
		reason := ""
		msg := ""
		if parsed != nil {
			if dbg, ok := parsed["debug"].(map[string]any); ok {
				reason = usageStr(dbg["reason"])
			}
			msg = usageStr(parsed["message"])
		}
		if reason == "REASON_FEATURE_NO_PERMISSION" ||
			regexp.MustCompile(`(?i)permission_denied|do not have permission|subscribe`).MatchString(body) {
			if msg == "" {
				msg = "Kimi connected, but this account has no permission to view usage. Subscribe to Kimi Code to access quota."
			}
			return msg
		}
	}
	snippet := strings.TrimSpace(body)
	if len(snippet) > 100 {
		snippet = snippet[:100]
	}
	if snippet != "" {
		return fmt.Sprintf("Kimi Coding connected. API Error %d: %s", status, snippet)
	}
	return fmt.Sprintf("Kimi Coding connected. API Error %d", status)
}

// buildKimiUsageHeaders mirrors buildKimiHeaders (device identity for OAuth).
func buildKimiUsageHeaders(deviceID string) map[string]string {
	id := strings.TrimSpace(deviceID)
	if id == "" {
		id = fmt.Sprintf("kimi-%d", time.Now().UnixMilli())
	}
	return map[string]string{
		"X-Msh-Platform":    "9router",
		"X-Msh-Device-Name": "nox9go",
		"X-Msh-Device-Id":   id,
	}
}

// ---------- minimax / minimax-cn: coding_plan/remains ----------

var miniMaxUsageURLs = map[string][]string{
	"minimax": {
		"https://www.minimax.io/v1/token_plan/remains",
		"https://api.minimax.io/v1/api/openplatform/coding_plan/remains",
	},
	"minimax-cn": {
		"https://www.minimaxi.com/v1/api/openplatform/coding_plan/remains",
		"https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains",
	},
}

func fetchMiniMaxUsage(ctx context.Context, provider, apiKey string) usageResult {
	if strings.TrimSpace(apiKey) == "" {
		return usageResult{message: "MiniMax API key not available."}
	}
	urls := miniMaxUsageURLs[provider]
	var lastErr string
	for i, url := range urls {
		status, _, out, err := usageGet(ctx, url, map[string]string{
			"Authorization": "Bearer " + strings.TrimSpace(apiKey),
			"Accept":        "application/json",
			"Content-Type":  "application/json",
		})
		if err != nil {
			lastErr = err.Error()
			if i < len(urls)-1 {
				continue
			}
			break
		}
		payload := usageJSON(out)
		base, _ := payload["base_resp"].(map[string]any)
		if base == nil {
			base, _ = payload["baseResp"].(map[string]any)
		}
		apiStatus := usageNum(firstNonEmptyAny(base["status_code"], base["statusCode"]), 0)
		apiMsg := strings.TrimSpace(usageStr(firstNonEmptyAny(base["status_msg"], base["statusMsg"])))
		authLike := regexp.MustCompile(`(?i)token plan|coding plan|invalid api key|invalid key|unauthorized|inactive`).MatchString(apiMsg + " " + string(out))

		if status == http.StatusUnauthorized || status == http.StatusForbidden || apiStatus == 1004 || authLike {
			return usageResult{message: "MiniMax API key invalid or inactive. Use an active Token/Coding Plan key."}
		}
		if status < 200 || status >= 300 {
			lastErr = fmt.Sprintf("MiniMax usage endpoint error (%d)", status)
			if (status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status >= 500) && i < len(urls)-1 {
				continue
			}
			return usageResult{plan: "MiniMax", message: "MiniMax connected. " + lastErr}
		}
		if apiStatus != 0 {
			return usageResult{plan: "MiniMax", message: "MiniMax connected. " + firstNonEmptyStr(apiMsg, "Upstream quota API error")}
		}

		remains, _ := payload["model_remains"].([]any)
		if remains == nil {
			remains, _ = payload["modelRemains"].([]any)
		}
		countMeansRemaining := strings.Contains(url, "/coding_plan/remains")
		quotas := map[string]any{}
		for _, rawModel := range remains {
			model, ok := rawModel.(map[string]any)
			if !ok || !hasMiniMaxQuota(model) {
				continue
			}
			name := formatMiniMaxQuotaName(model)
			addMiniMaxQuotaRow(quotas, name+" (5h)", model,
				"current_interval_total_count", "currentIntervalTotalCount",
				"current_interval_usage_count", "currentIntervalUsageCount",
				"current_interval_remaining_percent", "currentIntervalRemainingPercent",
				"remains_time", "remainsTime", "end_time", "endTime", countMeansRemaining)
			addMiniMaxQuotaRow(quotas, name+" (7d)", model,
				"current_weekly_total_count", "currentWeeklyTotalCount",
				"current_weekly_usage_count", "currentWeeklyUsageCount",
				"current_weekly_remaining_percent", "currentWeeklyRemainingPercent",
				"weekly_remains_time", "weeklyRemainsTime", "weekly_end_time", "weeklyEndTime", countMeansRemaining)
		}
		if len(quotas) == 0 {
			return usageResult{plan: "MiniMax", message: "MiniMax connected. No quota data was returned."}
		}
		return usageResult{plan: "MiniMax", quotas: quotas}
	}
	if lastErr != "" {
		return usageResult{plan: "MiniMax", message: "MiniMax connected. Unable to fetch usage: " + lastErr}
	}
	return usageResult{plan: "MiniMax", message: "MiniMax connected. Unable to fetch usage."}
}

func miniMaxField(model map[string]any, snake, camel string) any {
	if v, ok := model[snake]; ok {
		return v
	}
	return model[camel]
}

func miniMaxProvidedPercent(model map[string]any, snake, camel string) (float64, bool) {
	raw := miniMaxField(model, snake, camel)
	if raw == nil {
		return 0, false
	}
	f, ok := usageFiniteNum(raw)
	if !ok {
		return 0, false
	}
	return math.Max(0, math.Min(100, f)), true
}

func hasMiniMaxQuota(model map[string]any) bool {
	if usageNum(miniMaxField(model, "current_interval_total_count", "currentIntervalTotalCount"), 0) > 0 {
		return true
	}
	if usageNum(miniMaxField(model, "current_weekly_total_count", "currentWeeklyTotalCount"), 0) > 0 {
		return true
	}
	if _, ok := miniMaxProvidedPercent(model, "current_interval_remaining_percent", "currentIntervalRemainingPercent"); ok {
		return true
	}
	if _, ok := miniMaxProvidedPercent(model, "current_weekly_remaining_percent", "currentWeeklyRemainingPercent"); ok {
		return true
	}
	return false
}

func formatMiniMaxQuotaName(model map[string]any) string {
	raw := strings.TrimSpace(usageStr(miniMaxField(model, "model_name", "modelName")))
	if raw == "" {
		return "MiniMax"
	}
	if raw == "MiniMax-M*" || raw == "general" {
		return "M-series"
	}
	return raw
}

// addMiniMaxQuotaRow ports addMiniMaxQuota + buildMiniMaxQuota. M-series buckets
// ship percent-only (count = 0), so the count fields are normalized to a 0..100
// total before the row is built.
func addMiniMaxQuotaRow(quotas map[string]any, key string, model map[string]any,
	totalSnake, totalCamel, countSnake, countCamel, pctSnake, pctCamel string,
	remainsSnake, remainsCamel, endSnake, endCamel string, countMeansRemaining bool) {

	total := math.Max(0, usageNum(miniMaxField(model, totalSnake, totalCamel), 0))
	providedPct, hasPct := miniMaxProvidedPercent(model, pctSnake, pctCamel)
	if total <= 0 && !hasPct {
		return
	}
	count := math.Max(0, usageNum(miniMaxField(model, countSnake, countCamel), 0))
	effectiveTotal, effectiveCount := total, count
	if total <= 0 {
		effectiveTotal = 100
		if countMeansRemaining {
			effectiveCount = math.Round(effectiveTotal * providedPct / 100)
		} else {
			effectiveCount = math.Round(effectiveTotal * (1 - providedPct/100))
		}
	}

	used := math.Min(math.Max(0, effectiveCount), effectiveTotal)
	if countMeansRemaining {
		used = math.Max(effectiveTotal-effectiveCount, 0)
	}
	remaining := math.Max(effectiveTotal-used, 0)
	pct := percentOrZero(remaining, effectiveTotal)
	if hasPct {
		pct = math.Max(0, math.Min(100, providedPct))
	}

	resetAt := ""
	if remainsMs := usageNum(miniMaxField(model, remainsSnake, remainsCamel), 0); remainsMs > 0 {
		resetAt = time.Now().Add(time.Duration(remainsMs) * time.Millisecond).UTC().Format(time.RFC3339)
	} else {
		resetAt = usageResetTime(miniMaxField(model, endSnake, endCamel))
	}

	quotas[key] = map[string]any{
		"used":                used,
		"total":               effectiveTotal,
		"remaining":           remaining,
		"remainingPercentage": pct,
		"resetAt":             usageResetTimeToNil(resetAt),
		"unlimited":           false,
	}
}

// ---------- opencode-go: GET zen/go/v1/usage ----------

func fetchOpenCodeGoUsage(ctx context.Context, apiKey string) usageResult {
	if strings.TrimSpace(apiKey) == "" {
		return usageResult{message: "OpenCode Go API key not available. Add a key to view usage."}
	}
	status, _, out, err := usageGet(ctx, openCodeGoUsageURL, map[string]string{
		"Authorization": "Bearer " + strings.TrimSpace(apiKey),
		"Accept":        "application/json",
	})
	if err != nil {
		return usageResult{message: fmt.Sprintf("OpenCode Go error: %v", err)}
	}
	switch status {
	case http.StatusUnauthorized:
		return usageResult{plan: "OpenCode Go", message: "OpenCode Go authentication failed. Check the API key."}
	case http.StatusForbidden:
		body := usageJSON(out)
		errType := ""
		if e, ok := body["error"].(map[string]any); ok {
			errType = usageStr(e["type"])
		}
		msg := "OpenCode Go access forbidden for this API key."
		if errType == "EntitlementError" {
			msg = "OpenCode Go subscription required for this API key."
		}
		return usageResult{plan: "OpenCode Go", message: msg}
	}
	if status < 200 || status >= 300 {
		return usageResult{plan: "OpenCode Go", message: fmt.Sprintf("OpenCode Go usage API error (%d).", status)}
	}
	data := usageJSON(out)
	usage, _ := data["usage"].(map[string]any)
	if usage == nil {
		return usageResult{plan: "OpenCode Go", message: "OpenCode Go usage response did not contain quota data."}
	}
	quotaNames := []struct{ period, label string }{
		{"rolling", "Rolling"}, {"weekly", "Weekly"}, {"monthly", "Monthly"},
	}
	quotas := map[string]any{}
	for _, qn := range quotaNames {
		entry, ok := usage[qn.period].(map[string]any)
		if !ok {
			continue
		}
		pct, ok := usageFiniteNum(entry["percent"])
		if !ok {
			continue
		}
		used := math.Max(0, math.Min(100, pct))
		quotas[qn.label] = map[string]any{
			"used":                used,
			"total":               float64(100),
			"remaining":           100 - used,
			"remainingPercentage": 100 - used,
			"resetAt":             usageResetTimeToNil(usageResetTime(entry["resetsAt"])),
			"unlimited":           false,
		}
	}
	if len(quotas) == 0 {
		return usageResult{plan: "OpenCode Go", message: "OpenCode Go usage response did not contain valid quota data."}
	}
	return usageResult{plan: "OpenCode Go", quotas: quotas}
}

// ---------- codebuddy-cn: POST copilot.tencent.com billing meter ----------

var codebuddyCnHeaders = map[string]string{
	"User-Agent":          "CLI/2.108.1 CodeBuddy/2.108.1",
	"X-Product":           "SaaS",
	"X-IDE-Type":          "CLI",
	"X-IDE-Name":          "CLI",
	"x-requested-with":    "XMLHttpRequest",
	"x-codebuddy-request": "1",
	"Content-Type":        "application/json",
	"Accept":              "application/json",
}

func fetchCodeBuddyCnUsage(ctx context.Context, accessToken, apiKey string) usageResult {
	token := firstNonEmptyStr(accessToken, apiKey)
	if token == "" {
		return usageResult{message: "CodeBuddy (codebuddy-cn) credential not available."}
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	for k, v := range codebuddyCnHeaders {
		headers[k] = v
	}
	status, _, out, err := usageDo(ctx, http.MethodPost, codeBuddyCnUsageURL, headers, []byte("{}"))
	if err != nil {
		return usageResult{message: fmt.Sprintf("CodeBuddy (codebuddy-cn) error: %v", err)}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usageResult{message: "CodeBuddy CN credential invalid or expired."}
	}
	if status < 200 || status >= 300 {
		return usageResult{message: fmt.Sprintf("CodeBuddy CN quota API error (%d).", status)}
	}
	body := usageJSON(out)
	if body == nil {
		return usageResult{message: "CodeBuddy CN quota error: invalid JSON"}
	}
	if code := usageNum(body["code"], -1); code != 0 {
		return usageResult{message: "CodeBuddy CN quota error: " + firstNonEmptyStr(usageStr(body["msg"]), "unknown")}
	}
	var data map[string]any
	if d, ok := body["data"].(map[string]any); ok {
		if r, ok := d["Response"].(map[string]any); ok {
			data, _ = r["Data"].(map[string]any)
		}
	}
	var accounts []any
	if a, ok := data["Accounts"].([]any); ok {
		accounts = a
	}
	if len(accounts) == 0 {
		return usageResult{message: "CodeBuddy CN connected. No credit package found."}
	}
	var refills, bonuses []map[string]any
	for _, a := range accounts {
		if acc, ok := a.(map[string]any); ok {
			if codebuddyIsRefill(acc) {
				refills = append(refills, acc)
			} else {
				bonuses = append(bonuses, acc)
			}
		}
	}
	sortByCycleEnd(refills)
	sortByCycleEnd(bonuses)

	quotas := map[string]any{}
	seen := map[string]int{}
	for _, acc := range refills {
		base := codebuddyCadence(acc)
		seen[base]++
		name := base
		if seen[base] > 1 {
			name = fmt.Sprintf("%s %d", base, seen[base])
		}
		quotas[name] = map[string]any{
			"used":      codebuddyNum(acc["CycleCapacityUsedPrecise"], acc["CycleCapacityUsed"]),
			"total":     codebuddyNum(acc["CycleCapacitySizePrecise"], acc["CycleCapacitySize"]),
			"resetAt":   usageResetTimeToNil(acc["CycleEndTime"]),
			"unlimited": false, "recurring": true,
		}
	}
	for i, acc := range bonuses {
		quotas[fmt.Sprintf("Bonus Pack %d", i+1)] = map[string]any{
			"used":      codebuddyNum(acc["CapacityUsedPrecise"], acc["CapacityUsed"]),
			"total":     codebuddyNum(acc["CapacitySizePrecise"], acc["CapacitySize"]),
			"resetAt":   usageResetTimeToNil(acc["CycleEndTime"]),
			"unlimited": false, "recurring": false,
		}
	}
	plan := "CodeBuddy"
	base := map[string]any{}
	if len(refills) > 0 {
		base = refills[0]
	} else if len(accounts) > 0 {
		if m, ok := accounts[0].(map[string]any); ok {
			base = m
		}
	}
	if p, _ := base["PackageName"].(string); p != "" {
		plan = p
	} else if p, _ := base["SubProductName"].(string); p != "" {
		plan = p
	}
	return usageResult{plan: plan, quotas: quotas}
}

func sortByCycleEnd(list []map[string]any) {
	// Insertion sort keeps this file dependency-free and the lists are tiny.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && codebuddyCycleEndMs(list[j]) < codebuddyCycleEndMs(list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// ---------- xiaomi-mimo: GET aistudio user/usage ----------

func fetchXiaomiMimoUsage(ctx context.Context, apiKey string) usageResult {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return usageResult{message: "Xiaomi MiMo Desktop not connected. Add credentials to view usage."}
	}
	status, _, out, err := usageGet(ctx, xiaomiMimoUsageURL, map[string]string{
		"Authorization": "Bearer " + key,
		"X-Mimo-Source": "mimocode-cli",
		"Accept":        "application/json",
	})
	if err != nil {
		return usageResult{message: fmt.Sprintf("Xiaomi MiMo Desktop usage error: %v", err)}
	}
	if status == http.StatusUnauthorized {
		return usageResult{plan: "Xiaomi MiMo Desktop", message: "Weekly quota requires Xiaomi account session. API key alone is insufficient."}
	}
	if status < 200 || status >= 300 {
		return usageResult{plan: "Xiaomi MiMo Desktop", message: fmt.Sprintf("Usage API error (%d)", status)}
	}
	data := usageJSON(out)
	inner, _ := data["data"].(map[string]any)
	if usageNum(data["code"], -1) != 0 || inner == nil {
		return usageResult{plan: "Xiaomi MiMo Desktop", message: "Usage endpoint returned unexpected response."}
	}
	percent, ok := usageFiniteNum(inner["percent"])
	if !ok {
		return usageResult{plan: "Xiaomi MiMo Desktop", message: "Usage data missing percent field."}
	}
	remaining := math.Max(0, math.Min(100, math.Round(percent)))
	used := 100 - remaining
	resetAt := ""
	if resetDate := usageStr(inner["resetDate"]); resetDate != "" {
		if tm, err := time.Parse("2006-01-02", resetDate); err == nil {
			resetAt = tm.UTC().Format(time.RFC3339)
		}
	}
	return usageResult{plan: "Xiaomi MiMo Desktop", quotas: map[string]any{
		"Weekly": map[string]any{
			"used":                used,
			"total":               float64(100),
			"remainingPercentage": remaining,
			"resetAt":             usageResetTimeToNil(resetAt),
			"unlimited":           false,
		},
	}}
}

// ---------- zed: GET cloud.zed.dev/client/users/me ----------

func fetchZedUsage(ctx context.Context, accessToken string, psd map[string]any) usageResult {
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return usageResult{message: "Zed access token not available. Re-connect Zed to view quota."}
	}
	userID := psdStr(psd, "userId")
	if userID == "" {
		return usageResult{message: "Zed credential is missing user id. Re-connect Zed to view quota."}
	}
	status, _, out, err := usageGet(ctx, zedUsageURL, map[string]string{
		// Zed authenticates with "<user_id> <access_token>", not Bearer.
		"Authorization": userID + " " + token,
		"Accept":        "application/json",
		"content-type":  "application/json",
	})
	if err != nil {
		return usageResult{message: fmt.Sprintf("Zed error: %v", err)}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usageResult{message: "Zed authentication failed. Sign in again from the dashboard or Zed editor."}
	}
	if status < 200 || status >= 300 {
		return usageResult{message: fmt.Sprintf("Zed error: Failed to fetch quota (%d)", status)}
	}
	data := usageJSON(out)
	if data == nil {
		return usageResult{message: "Zed error: Failed to parse quota"}
	}
	return parseZedUsage(data)
}

func parseZedUsage(userInfo map[string]any) usageResult {
	plan, _ := userInfo["plan"].(map[string]any)
	planID := firstNonEmptyStr(usageStr(plan["plan_v3"]), usageStr(plan["plan_v2"]), usageStr(plan["plan"]), usageStr(userInfo["plan_v3"]))
	resetAt := usageResetTime(nestedMap(plan, "subscription_period")["ended_at"])
	if resetAt == "" {
		resetAt = usageResetTime(nestedMap(plan, "subscriptionPeriod")["endedAt"])
	}

	usage, _ := plan["usage"].(map[string]any)
	quotas := map[string]any{}
	if ep, ok := firstMap(usage["edit_predictions"], usage["editPredictions"]); ok {
		quotas["Edit Predictions"] = zedQuotaRow(ep["used"], ep["limit"], resetAt)
	}
	if mr, ok := firstMap(usage["model_requests"], usage["modelRequests"]); ok {
		limitRaw := mr["limit"]
		if limitRaw == nil {
			if bucket, ok := mr["limit"].(map[string]any); ok {
				limitRaw = bucket["limit"]
			}
		}
		unlimited, total := parseZedLimit(limitRaw)
		if unlimited || total > 0 {
			quotas["Hosted Model Requests"] = zedQuotaRow(mr["used"], limitRaw, resetAt)
		}
	}

	planLabel := formatZedPlanLabel(planID)
	if plan["trial_started_at"] != nil || plan["trialStartedAt"] != nil {
		if !regexp.MustCompile(`(?i)trial`).MatchString(planLabel) {
			planLabel += " (Trial active)"
		}
	}
	var message string
	if plan["has_overdue_invoices"] == true || plan["hasOverdueInvoices"] == true {
		message = "This Zed account has overdue invoices. Usage may be blocked until billing is resolved."
	}
	return usageResult{plan: planLabel, quotas: quotas, message: message}
}

func zedQuotaRow(usedRaw, limitRaw any, resetAt string) map[string]any {
	used := math.Max(0, usageNum(usedRaw, 0))
	unlimited, total := parseZedLimit(limitRaw)
	if unlimited {
		return map[string]any{
			"used": used, "total": float64(0), "remainingPercentage": float64(100),
			"resetAt": usageResetTimeToNil(resetAt), "unlimited": true,
		}
	}
	if total <= 0 {
		return map[string]any{
			"used": used, "total": float64(0), "remainingPercentage": float64(0),
			"resetAt": usageResetTimeToNil(resetAt), "unlimited": false,
		}
	}
	clamped := math.Min(used, total)
	remaining := math.Max(0, total-clamped)
	return map[string]any{
		"used":                clamped,
		"total":               total,
		"remainingPercentage": remaining / total * 100,
		"resetAt":             usageResetTimeToNil(resetAt),
		"unlimited":           false,
	}
}

// parseZedLimit ports parseZedUsageLimit: "unlimited", a number, or {limited: N}.
func parseZedLimit(raw any) (unlimited bool, total float64) {
	switch v := raw.(type) {
	case nil:
		return false, 0
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "unlimited") {
			return true, 0
		}
		if f, ok := usageFiniteNum(v); ok {
			return false, math.Max(0, f)
		}
		return false, 0
	case map[string]any:
		if v["unlimited"] == true {
			return true, 0
		}
		if f, ok := usageFiniteNum(firstNonEmptyAny(v["limited"], v["Limited"])); ok {
			return false, math.Max(0, f)
		}
		return false, 0
	default:
		if f, ok := usageFiniteNum(raw); ok {
			return false, math.Max(0, f)
		}
		return false, 0
	}
}

func formatZedPlanLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "Zed"
	}
	switch strings.ToLower(raw) {
	case "zed_free":
		return "Zed Free"
	case "zed_pro":
		return "Zed Pro"
	case "zed_pro_trial":
		return "Zed Pro Trial"
	case "zed_student":
		return "Zed Student"
	case "zed_business":
		return "Zed Business"
	}
	parts := strings.Fields(strings.ReplaceAll(raw, "_", " "))
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, " ")
}

func firstMap(vals ...any) (map[string]any, bool) {
	for _, v := range vals {
		if m, ok := v.(map[string]any); ok {
			return m, true
		}
	}
	return nil, false
}
