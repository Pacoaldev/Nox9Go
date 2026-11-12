package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Token Harbor has no usage API beyond the allowance read its own CLI uses
// (`GET /api/cli/free-tier`); these tests pin that payload shape and the
// percentage rows it must produce.
//
// The live payload this was written against, 2026-11-12, for an account with no
// Pass: {"plan_name":null,"models":["deepseek-v4-flash",...],"on_plan":false,
// "req_used":88,"reset_at":"2026-11-19T09:13:26.225039+00:00","used_pct":41,
// "rpm_limit":1000000,"plan_used_pct":0,"exhausted":false,
// "period_ends_at":"2026-11-19T09:13:26.225039+00:00"}.

func TestFetchTokenHarborUsage_FreeAllowanceRow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer thk_live_key" {
			t.Errorf("Authorization = %q, want the Bearer scheme", got)
		}
		_, _ = w.Write([]byte(`{"plan_name":null,"on_plan":false,"req_used":88,"used_pct":41,` +
			`"reset_at":"2026-11-19T09:13:26.225039+00:00","plan_used_pct":0,"exhausted":false}`))
	}))
	defer srv.Close()
	withURL(t, &tokenHarborUsageURL, srv.URL)

	res := fetchTokenHarborUsage(t.Context(), "thk_live_key")
	if res.message != "" {
		t.Fatalf("unexpected message: %s", res.message)
	}
	if res.plan != tokenHarborPlanLabel {
		t.Errorf("plan = %q, want %q", res.plan, tokenHarborPlanLabel)
	}
	if len(res.quotas) != 1 {
		t.Fatalf("rows = %d, want only the free allowance: %v", len(res.quotas), res.quotas)
	}
	row, ok := res.quotas["Free allowance"].(map[string]any)
	if !ok {
		t.Fatalf("missing free allowance row: %v", res.quotas)
	}
	if row["remainingPercentage"] != float64(59) {
		t.Errorf("remainingPercentage = %v, want 59", row["remainingPercentage"])
	}
	if row["resetAt"] != "2026-11-19T09:13:26Z" {
		t.Errorf("resetAt = %v, want the period reset", row["resetAt"])
	}
}

func TestFetchTokenHarborUsage_PassRowWhenOnPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"plan_name":"Office Pass","on_plan":true,"used_pct":0,` +
			`"reset_at":"2026-11-19T09:13:26+00:00","plan_used_pct":12.5,` +
			`"period_ends_at":"2026-11-18T10:00:00+00:00","plan_exhausted":false}`))
	}))
	defer srv.Close()
	withURL(t, &tokenHarborUsageURL, srv.URL)

	res := fetchTokenHarborUsage(t.Context(), "thk_live_key")
	if len(res.quotas) != 2 {
		t.Fatalf("rows = %d, want the free allowance plus the Pass: %v", len(res.quotas), res.quotas)
	}
	pass, ok := res.quotas["Office Pass"].(map[string]any)
	if !ok {
		t.Fatalf("missing the Pass row named after the plan: %v", res.quotas)
	}
	if pass["remainingPercentage"] != float64(87.5) {
		t.Errorf("Pass remainingPercentage = %v, want 87.5", pass["remainingPercentage"])
	}
	if pass["resetAt"] != "2026-11-18T10:00:00Z" {
		t.Errorf("Pass resetAt = %v, want the period end", pass["resetAt"])
	}
}

// A whole-percentage field that overshoots must not paint a negative
// remainder, and an exhausted account must read as fully spent.
func TestFetchTokenHarborUsage_ClampsOutOfRangeShares(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantRow string
		wantRem float64
	}{
		{"exhausted", `{"on_plan":false,"used_pct":100,"exhausted":true}`, "Free allowance", 0},
		{"exhausted with lagging share", `{"on_plan":false,"used_pct":80,"exhausted":true}`, "Free allowance", 0},
		{"overflowing", `{"on_plan":false,"used_pct":140}`, "Free allowance", 0},
		{"negative", `{"on_plan":false,"used_pct":-5}`, "Free allowance", 100},
		{"pass exhausted", `{"on_plan":true,"plan_name":"Office Pass","used_pct":0,"plan_used_pct":40,"plan_exhausted":true}`, "Office Pass", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			withURL(t, &tokenHarborUsageURL, srv.URL)

			res := fetchTokenHarborUsage(t.Context(), "thk_live_key")
			row, ok := res.quotas[tt.wantRow].(map[string]any)
			if !ok {
				t.Fatalf("missing %q row: %v", tt.wantRow, res.quotas)
			}
			if row["remainingPercentage"] != tt.wantRem {
				t.Errorf("remainingPercentage = %v, want %v", row["remainingPercentage"], tt.wantRem)
			}
		})
	}
}

func TestFetchTokenHarborUsage_UnusablePayloadIsUnavailable(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing used_pct", `{"on_plan":false}`},
		{"non-numeric used_pct", `{"on_plan":false,"used_pct":"lots"}`},
		{"plan without used share", `{"on_plan":true,"plan_name":"Office Pass","used_pct":5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			withURL(t, &tokenHarborUsageURL, srv.URL)

			res := fetchTokenHarborUsage(t.Context(), "thk_live_key")
			if len(res.quotas) != 0 {
				t.Fatalf("quotas = %v, want none on an unusable payload", res.quotas)
			}
			if res.message == "" {
				t.Fatal("expected an unavailable message")
			}
		})
	}
}

func TestFetchTokenHarborUsage_AuthAndMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	withURL(t, &tokenHarborUsageURL, srv.URL)

	if res := fetchTokenHarborUsage(t.Context(), "bad-key"); res.message != "Invalid Token Harbor API key." {
		t.Errorf("401 message = %q", res.message)
	}
	if res := fetchTokenHarborUsage(t.Context(), "  "); res.message == "" {
		t.Error("a connection with no key must explain why the card is empty")
	}
}

func TestFetchTokenHarborUsage_Non200IsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withURL(t, &tokenHarborUsageURL, srv.URL)

	res := fetchTokenHarborUsage(t.Context(), "thk_live_key")
	if res.message != "Token Harbor quota API error (500)" {
		t.Errorf("message = %q", res.message)
	}
}

func TestFetchProviderUsage_DispatchesTokenHarbor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"on_plan":false,"used_pct":25}`))
	}))
	defer srv.Close()
	withURL(t, &tokenHarborUsageURL, srv.URL)

	res, ok := fetchProviderUsage(t.Context(), "tokenharbor", map[string]any{"apiKey": "thk_live_key"})
	if !ok {
		t.Fatal("tokenharbor must be handled by a live fetcher, not the unmetered fallback")
	}
	row, ok := res.quotas["Free allowance"].(map[string]any)
	if !ok {
		t.Fatalf("missing free allowance row: %v", res.quotas)
	}
	if row["remainingPercentage"] != float64(75) {
		t.Errorf("remainingPercentage = %v, want 75", row["remainingPercentage"])
	}
}
