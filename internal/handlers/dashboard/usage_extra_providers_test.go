package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These fetchers were added because every one of these providers was listed in
// usageSupportedProviders with no case in fetchProviderUsage, so the row read
// "Account active. No quota limits tracked." Each test pins the upstream JSON
// shape and the row it must produce.

func withURL(t *testing.T, target *string, url string) {
	t.Helper()
	prev := *target
	*target = url
	t.Cleanup(func() { *target = prev })
}

func TestFetchGitHubUsage_PaidSnapshots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "token gh-token" {
			t.Errorf("Authorization = %q, want the `token` scheme", got)
		}
		_, _ = w.Write([]byte(`{
			"copilot_plan":"individual",
			"quota_reset_date":"2026-12-01",
			"quota_snapshots":{
				"chat":{"entitlement":500,"remaining":480,"unlimited":false},
				"completions":{"entitlement":2000,"remaining":1500,"unlimited":false},
				"premium_interactions":{"entitlement":300,"remaining":300,"unlimited":false}
			}
		}`))
	}))
	defer srv.Close()
	withURL(t, &githubUsageURL, srv.URL)

	res := fetchGitHubUsage(t.Context(), "gh-token")
	if res.message != "" {
		t.Fatalf("unexpected message: %s", res.message)
	}
	if res.plan != "individual" {
		t.Errorf("plan = %q, want individual", res.plan)
	}
	chat, ok := res.quotas["chat"].(map[string]any)
	if !ok {
		t.Fatalf("missing chat row: %v", res.quotas)
	}
	if chat["used"] != float64(20) || chat["total"] != float64(500) {
		t.Errorf("chat used/total = %v/%v, want 20/500", chat["used"], chat["total"])
	}
	if _, ok := res.quotas["completions"]; !ok {
		t.Error("missing completions row")
	}
}

func TestFetchGitHubUsage_FreeCountedQuotas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"copilot_plan":"free",
			"limited_user_reset_date":"2026-12-01",
			"monthly_quotas":{"chat":200,"completions":2000},
			"limited_user_quotas":{"chat":50,"completions":500}
		}`))
	}))
	defer srv.Close()
	withURL(t, &githubUsageURL, srv.URL)

	res := fetchGitHubUsage(t.Context(), "gh-token")
	chat, ok := res.quotas["chat"].(map[string]any)
	if !ok {
		t.Fatalf("missing chat row: %v", res.quotas)
	}
	if chat["used"] != float64(50) || chat["total"] != float64(200) {
		t.Errorf("chat used/total = %v/%v, want 50/200", chat["used"], chat["total"])
	}
}

func TestFetchGlmUsage_ParsesLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer glm-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{
			"code":200,
			"data":{
				"level":"pro",
				"limits":[
					{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":43,"nextResetTime":1790000000000},
					{"type":"TOKENS_LIMIT","unit":6,"number":7,"percentage":63}
				]
			}
		}`))
	}))
	defer srv.Close()
	withURL(t, &glmUsageURL, srv.URL)

	res := fetchGlmUsage(t.Context(), "glm", "glm-key")
	if res.plan != "Pro" {
		t.Errorf("plan = %q, want Pro", res.plan)
	}
	session, ok := res.quotas["Session (5h)"].(map[string]any)
	if !ok {
		t.Fatalf("missing session row: %v", res.quotas)
	}
	if session["used"] != float64(43) || session["remainingPercentage"] != float64(57) {
		t.Errorf("session used/remaining = %v/%v, want 43/57", session["used"], session["remainingPercentage"])
	}
	if _, ok := res.quotas["Weekly (7d)"]; !ok {
		t.Error("missing weekly row")
	}
}

func TestFetchKimiUsage_APIKeyWeeklyAndRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "kimi-key" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		_, _ = w.Write([]byte(`{
			"usage":{"limit":1000,"used":250,"resetTime":"2026-12-01T00:00:00Z"},
			"limits":[{"detail":{"limit":60,"remaining":15}}],
			"user":{"membership":{"level":"LEVEL_STANDARD"}}
		}`))
	}))
	defer srv.Close()
	withURL(t, &kimiUsageURL, srv.URL)

	res := fetchKimiUsage(t.Context(), "", "kimi-key", nil)
	if res.plan != "Vivace" {
		t.Errorf("plan = %q, want Vivace", res.plan)
	}
	weekly, ok := res.quotas["Weekly"].(map[string]any)
	if !ok {
		t.Fatalf("missing weekly row: %v", res.quotas)
	}
	if weekly["used"] != float64(250) || weekly["total"] != float64(1000) {
		t.Errorf("weekly used/total = %v/%v, want 250/1000", weekly["used"], weekly["total"])
	}
	if _, ok := res.quotas["Ratelimit"]; !ok {
		t.Error("missing ratelimit row")
	}
}

func TestFetchMiniMaxUsage_CodingPlanPercent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"base_resp":{"status_code":0},
			"model_remains":[
				{"model_name":"MiniMax-M*","current_interval_remaining_percent":80,"current_weekly_remaining_percent":60}
			]
		}`))
	}))
	defer srv.Close()
	prev := miniMaxUsageURLs
	miniMaxUsageURLs = map[string][]string{"minimax": {srv.URL + "/coding_plan/remains"}}
	t.Cleanup(func() { miniMaxUsageURLs = prev })

	res := fetchMiniMaxUsage(t.Context(), "minimax", "mm-key")
	if res.message != "" {
		t.Fatalf("unexpected message: %s", res.message)
	}
	if _, ok := res.quotas["M-series (5h)"]; !ok {
		t.Errorf("missing M-series (5h) row: %v", res.quotas)
	}
	if _, ok := res.quotas["M-series (7d)"]; !ok {
		t.Errorf("missing M-series (7d) row: %v", res.quotas)
	}
}

func TestFetchOpenCodeGoUsage_PercentQuotas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"usage":{
				"rolling":{"percent":10,"resetsAt":"2026-12-01T00:00:00Z"},
				"weekly":{"percent":25},
				"monthly":{"percent":50}
			}
		}`))
	}))
	defer srv.Close()
	withURL(t, &openCodeGoUsageURL, srv.URL)

	res := fetchOpenCodeGoUsage(t.Context(), "oc-go-key")
	if res.message != "" {
		t.Fatalf("unexpected message: %s", res.message)
	}
	rolling, ok := res.quotas["Rolling"].(map[string]any)
	if !ok {
		t.Fatalf("missing rolling row: %v", res.quotas)
	}
	if rolling["used"] != float64(10) || rolling["remainingPercentage"] != float64(90) {
		t.Errorf("rolling used/remaining = %v/%v, want 10/90", rolling["used"], rolling["remainingPercentage"])
	}
}

func TestFetchCodeBuddyCnUsage_BonusAndRefill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("X-IDE-Type") != "CLI" {
			t.Errorf("X-IDE-Type = %q, want CLI", r.Header.Get("X-IDE-Type"))
		}
		_, _ = w.Write([]byte(`{
			"code":0,
			"data":{"Response":{"Data":{"Accounts":[
				{"CycleCapacityUsed":50,"CycleCapacitySize":100,
				 "CycleStartTime":"2026-11-01 00:00:00","CycleEndTime":"2026-12-01 00:00:00",
				 "DeductionEndTime":1798000000000,"PackageName":"Pro"},
				{"CapacityUsed":10,"CapacitySize":30,
				 "CycleStartTime":"2026-11-01 00:00:00","CycleEndTime":"2026-11-20 00:00:00",
				 "DeductionEndTime":1795000000000}
			]}}}
		}`))
	}))
	defer srv.Close()
	withURL(t, &codeBuddyCnUsageURL, srv.URL)

	res := fetchCodeBuddyCnUsage(t.Context(), "cb-token", "")
	if res.message != "" {
		t.Fatalf("unexpected message: %s", res.message)
	}
	if res.plan != "Pro" {
		t.Errorf("plan = %q, want Pro", res.plan)
	}
	if _, ok := res.quotas["Bonus Pack 1"]; !ok {
		t.Errorf("missing bonus pack row: %v", res.quotas)
	}
	if _, ok := res.quotas["Monthly"]; !ok {
		t.Errorf("missing refill cadence row: %v", res.quotas)
	}
}

func TestFetchXiaomiMimoUsage_WeeklyPercent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"percent":94,"resetDate":"2026-12-01"}}`))
	}))
	defer srv.Close()
	withURL(t, &xiaomiMimoUsageURL, srv.URL)

	res := fetchXiaomiMimoUsage(t.Context(), "mimo-key")
	weekly, ok := res.quotas["Weekly"].(map[string]any)
	if !ok {
		t.Fatalf("missing weekly row: %v", res.quotas)
	}
	if weekly["used"] != float64(6) || weekly["remainingPercentage"] != float64(94) {
		t.Errorf("weekly used/remaining = %v/%v, want 6/94", weekly["used"], weekly["remainingPercentage"])
	}
}

func TestFetchZedUsage_EditPredictions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "42 zed-token" {
			t.Errorf("Authorization = %q, want `42 zed-token`", got)
		}
		_, _ = w.Write([]byte(`{
			"plan":{
				"plan_v3":"zed_pro",
				"subscription_period":{"ended_at":"2026-12-01T00:00:00Z"},
				"usage":{"edit_predictions":{"used":200,"limit":1000}}
			}
		}`))
	}))
	defer srv.Close()
	withURL(t, &zedUsageURL, srv.URL)

	psd := map[string]any{"userId": "42"}
	res := fetchZedUsage(t.Context(), "zed-token", psd)
	if res.plan != "Zed Pro" {
		t.Errorf("plan = %q, want Zed Pro", res.plan)
	}
	ep, ok := res.quotas["Edit Predictions"].(map[string]any)
	if !ok {
		t.Fatalf("missing edit predictions row: %v", res.quotas)
	}
	if ep["used"] != float64(200) || ep["total"] != float64(1000) {
		t.Errorf("edit predictions used/total = %v/%v, want 200/1000", ep["used"], ep["total"])
	}
}

// A 401 on any of these must read as a dead credential, never as an empty
// table that implies the account has no quota.
func TestExtraFetchers_AuthFailureSaysReauthorize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	withURL(t, &githubUsageURL, srv.URL)
	withURL(t, &glmUsageURL, srv.URL)
	withURL(t, &kimiUsageURL, srv.URL)
	withURL(t, &openCodeGoUsageURL, srv.URL)

	if res := fetchGitHubUsage(t.Context(), "tok"); res.message == "" {
		t.Error("github 401 produced no message")
	}
	if res := fetchGlmUsage(t.Context(), "glm", "key"); res.message == "" {
		t.Error("glm 401 produced no message")
	}
	if res := fetchKimiUsage(t.Context(), "", "key", nil); res.message == "" {
		t.Error("kimi 401 produced no message")
	}
	if res := fetchOpenCodeGoUsage(t.Context(), "key"); res.message == "" {
		t.Error("opencode-go 401 produced no message")
	}
}
