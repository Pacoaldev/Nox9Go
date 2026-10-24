package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleAuthCodeAuthorize_shapes(t *testing.T) {
	handler := NewOAuthHandler(nil)
	cases := map[string][]string{
		"gemini-cli": {"accounts.google.com/o/oauth2/v2/auth", "681255809395", "access_type=offline", "cloud-platform"},
		"iflow":      {"iflow.cn/oauth", "loginMethod=phone", "type=phone", "10009311001"},
	}
	for provider, wants := range cases {
		req := httptest.NewRequest("GET", "/api/oauth/authcode/authorize?provider="+provider, nil)
		rec := httptest.NewRecorder()
		handler.HandleAuthCodeAuthorize(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", provider, rec.Code, rec.Body.String())
		}
		for _, want := range wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s: response missing %q: %s", provider, want, rec.Body.String())
			}
		}
	}
}

func TestHandleAuthCodeExchange_iflow(t *testing.T) {
	var sawBasic, sawUserinfo bool
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			u, p, ok := r.BasicAuth()
			sawBasic = ok && u == "10009311001" && p != ""
			w.Write([]byte(`{"access_token":"ia","refresh_token":"ir","expires_in":3600}`))
			return
		}
		sawUserinfo = true
		w.Write([]byte(`{"success":true,"data":{"apiKey":"if-api-key","email":"u@iflow.cn","nickname":"U"}}`))
	}))
	defer mock.Close()

	old := authcodeProviders["iflow"]
	authcodeProviders["iflow"] = &authcodeConfig{
		providers: []string{"iflow"}, clientID: "10009311001", secret: "s",
		tokenURL: mock.URL + "/oauth/token", useBasic: true,
		userInfoURL: mock.URL + "/api/oauth/getUserInfo",
		connPrefix:  "if-", display: "iFlow",
	}
	defer func() { authcodeProviders["iflow"] = old }()

	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("POST", "/api/oauth/authcode/exchange",
		strings.NewReader(`{"provider":"iflow","code":"c"}`))
	rec := httptest.NewRecorder()
	handler.HandleAuthCodeExchange(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !sawBasic {
		t.Error("expected Basic auth on token call")
	}
	if !sawUserinfo {
		t.Error("expected userinfo call for iflow")
	}
	if !strings.Contains(rec.Body.String(), `"status":"authorized"`) {
		t.Errorf("not authorized: %s", rec.Body.String())
	}
}
