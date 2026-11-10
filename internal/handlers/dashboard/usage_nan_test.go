package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// NaN.Builders rows were the loudest "no quota details" card: the provider does
// report per-model token caps, just on its cloud host. These tests pin the
// payload shape and the row each entry must produce.

func TestFetchNanUsage_TokenRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer nan-key" {
			t.Errorf("Authorization = %q, want the Bearer scheme", got)
		}
		_, _ = w.Write([]byte(`{
			"models":[
				{"model":"glm5.3","tokensUsed":2500000,"cap":500000000,"periodEnd":"2026-12-01T00:00:00Z"},
				{"model":"deepseek","tokensUsed":0,"cap":1000000000,"periodEnd":"2026-12-01T00:00:00Z"}
			]
		}`))
	}))
	defer srv.Close()
	withURL(t, &nanUsageURL, srv.URL)

	res := fetchNanUsage(t.Context(), "nan-key")
	if res.message != "" {
		t.Fatalf("unexpected message: %s", res.message)
	}
	if res.plan != nanPlanLabel {
		t.Errorf("plan = %q, want %q", res.plan, nanPlanLabel)
	}
	if len(res.quotas) != 2 {
		t.Fatalf("rows = %d, want 2: %v", len(res.quotas), res.quotas)
	}

	glm, ok := res.quotas["glm5.3"].(map[string]any)
	if !ok {
		t.Fatalf("missing glm5.3 row: %v", res.quotas)
	}
	if glm["used"] != float64(2500000) || glm["total"] != float64(500000000) {
		t.Errorf("glm5.3 used/total = %v/%v, want 2.5e6/5e8", glm["used"], glm["total"])
	}
	if glm["remainingPercentage"] != float64(99.5) {
		t.Errorf("glm5.3 remainingPercentage = %v, want 99.5", glm["remainingPercentage"])
	}
	if _, hasRemaining := glm["remaining"]; hasRemaining {
		t.Error("a token row must not carry `remaining`: the dashboard reads that field as a percentage")
	}
	if glm["displayName"] != "glm5.3 (tokens)" {
		t.Errorf("displayName = %v", glm["displayName"])
	}
	if glm["unit"] != "tokens" {
		t.Errorf("unit = %v, want tokens", glm["unit"])
	}
	if glm["resetAt"] != "2026-12-01T00:00:00Z" {
		t.Errorf("resetAt = %v, want the period end", glm["resetAt"])
	}

	deepseek, ok := res.quotas["deepseek"].(map[string]any)
	if !ok {
		t.Fatalf("missing deepseek row: %v", res.quotas)
	}
	if deepseek["remainingPercentage"] != float64(100) {
		t.Errorf("deepseek remainingPercentage = %v, want 100", deepseek["remainingPercentage"])
	}
}

func TestFetchNanUsage_MalformedRowIsUnavailable(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"cap zero", `{"models":[{"model":"glm5.3","tokensUsed":1,"cap":0}]}`},
		{"negative used", `{"models":[{"model":"glm5.3","tokensUsed":-1,"cap":10}]}`},
		{"missing model", `{"models":[{"tokensUsed":1,"cap":10}]}`},
		{"unparseable period", `{"models":[{"model":"glm5.3","tokensUsed":1,"cap":10,"periodEnd":"soon"}]}`},
		{"duplicate model", `{"models":[{"model":"glm5.3","tokensUsed":1,"cap":10},{"model":"glm5.3","tokensUsed":2,"cap":10}]}`},
		{"empty list", `{"models":[]}`},
		{"no models key", `{"ok":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			withURL(t, &nanUsageURL, srv.URL)

			res := fetchNanUsage(t.Context(), "nan-key")
			if len(res.quotas) != 0 {
				t.Fatalf("quotas = %v, want none on a malformed report", res.quotas)
			}
			if res.message == "" {
				t.Fatal("expected an unavailable message")
			}
		})
	}
}

func TestFetchNanUsage_AuthAndMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	withURL(t, &nanUsageURL, srv.URL)

	if res := fetchNanUsage(t.Context(), "bad-key"); res.message != "Invalid NaN.Builders API key." {
		t.Errorf("401 message = %q", res.message)
	}
	if res := fetchNanUsage(t.Context(), "  "); res.message == "" {
		t.Error("a connection with no key must explain why the card is empty")
	}
}

func TestFetchNanUsage_Non200IsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withURL(t, &nanUsageURL, srv.URL)

	res := fetchNanUsage(t.Context(), "nan-key")
	if res.message != "NaN.Builders quota API error (500)" {
		t.Errorf("message = %q", res.message)
	}
}
