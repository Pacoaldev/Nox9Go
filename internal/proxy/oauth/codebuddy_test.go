package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestTarget(url string) codebuddyRefreshTarget {
	return codebuddyRefreshTarget{provider: "codebuddy-intl", baseURL: url, domain: "www.codebuddy.ai", userAgent: "ua"}
}

func TestCodebuddyRefresh(t *testing.T) {
	var gotHeader http.Header
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader, gotPath = r.Header.Clone(), r.URL.Path
		_, _ = w.Write([]byte(`{"code":0,"data":{"accessToken":"new-a","refreshToken":"new-r","expiresIn":3600}}`))
	}))
	defer srv.Close()

	res, err := newTestTarget(srv.URL).refresh(context.Background(), &Params{Client: srv.Client(), RefreshToken: "old-r"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.AccessToken != "new-a" || res.RefreshToken != "new-r" || res.ExpiresIn != 3600 {
		t.Errorf("unexpected result %+v", res)
	}
	if gotPath != codebuddyRefreshPath {
		t.Errorf("path = %q", gotPath)
	}
	if gotHeader.Get("X-Refresh-Token") != "old-r" || gotHeader.Get("X-Domain") != "www.codebuddy.ai" {
		t.Errorf("headers = %v", gotHeader)
	}
}

func TestCodebuddyRefreshKeepsOldRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"accessToken":"new-a"}}`))
	}))
	defer srv.Close()

	res, err := newTestTarget(srv.URL).refresh(context.Background(), &Params{Client: srv.Client(), RefreshToken: "old-r"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.RefreshToken != "old-r" || res.ExpiresIn != 86400 {
		t.Errorf("unexpected result %+v", res)
	}
}

func TestCodebuddyRefreshErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		token  string
	}{
		{"missing refresh token", 200, `{}`, ""},
		{"http error", 401, `{"code":1}`, "r"},
		{"api error code", 200, `{"code":11217,"msg":"nope"}`, "r"},
		{"no access token", 200, `{"code":0,"data":{}}`, "r"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := newTestTarget(srv.URL).refresh(context.Background(), &Params{Client: srv.Client(), RefreshToken: tt.token})
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestCodebuddyRefreshersRegistered(t *testing.T) {
	for _, p := range []string{"codebuddy-cn", "codebuddy-intl"} {
		if Get(p) == nil {
			t.Errorf("no refresher registered for %s", p)
		}
	}
	if !strings.HasPrefix(codebuddyRefreshPath, "/v2/") {
		t.Error("unexpected refresh path")
	}
}
