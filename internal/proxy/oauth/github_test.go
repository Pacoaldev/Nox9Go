package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshGitHub_ExchangesCopilotToken(t *testing.T) {
	expUnix := time.Now().Add(25 * time.Minute).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "token gho_test_access" {
			t.Errorf("Authorization header = %q, want %q", got, "token gho_test_access")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"tid=fresh_copilot_token;exp=9999999999","expires_at":` + "1999999999" + `}`))
	}))
	defer srv.Close()

	prevURL := githubCopilotTokenURL
	githubCopilotTokenURL = srv.URL
	t.Cleanup(func() { githubCopilotTokenURL = prevURL })

	res, err := refreshGitHub(context.Background(), &Params{
		Client:      srv.Client(),
		Provider:    "github",
		AccessToken: "gho_test_access",
	})
	if err != nil {
		t.Fatalf("refreshGitHub error = %v", err)
	}
	if res.APIKey != "tid=fresh_copilot_token;exp=9999999999" {
		t.Errorf("res.APIKey = %q, want copilot token", res.APIKey)
	}
	if res.AccessToken != "gho_test_access" {
		t.Errorf("res.AccessToken = %q, want gho_test_access", res.AccessToken)
	}
	if got := res.ExtraPSD["copilotToken"]; got != "tid=fresh_copilot_token;exp=9999999999" {
		t.Errorf("ExtraPSD[copilotToken] = %v", got)
	}
	_ = expUnix
}

func TestCopilotTokenNeedsRefresh(t *testing.T) {
	tests := []struct {
		name string
		psd  map[string]any
		want bool
	}{
		{name: "nil psd", psd: nil, want: true},
		{name: "missing copilotToken", psd: map[string]any{}, want: true},
		{name: "expired copilotToken", psd: map[string]any{"copilotToken": "tid=1", "copilotTokenExpiresAt": float64(time.Now().Add(-10 * time.Minute).Unix())}, want: true},
		{name: "fresh copilotToken", psd: map[string]any{"copilotToken": "tid=1", "copilotTokenExpiresAt": float64(time.Now().Add(20 * time.Minute).Unix())}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CopilotTokenNeedsRefresh(tt.psd); got != tt.want {
				t.Errorf("CopilotTokenNeedsRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

