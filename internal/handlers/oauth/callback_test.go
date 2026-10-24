package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleCallbackPage_ok(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("GET", "/callback?code=abc123&state=s1", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallbackPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html, got %q", ct)
	}
	for _, want := range []string{"OAuth callback", `id="code"`, "Copy", "paste"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("callback page missing %q", want)
		}
	}
}

func TestHandleCallbackPage_noReflection(t *testing.T) {
	// Query values must never be reflected into the HTML (page is fully static;
	// extraction happens client-side via textContent).
	handler := NewOAuthHandler(nil)
	payload := "<script>alert(1)</script>"
	req := httptest.NewRequest("GET", "/callback?code="+payload, nil)
	rec := httptest.NewRecorder()
	handler.HandleCallbackPage(rec, req)
	if strings.Contains(rec.Body.String(), payload) {
		t.Errorf("callback page reflects query input (XSS)")
	}
}

func TestHandleCallbackPage_methodNotAllowed(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("POST", "/callback", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallbackPage(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
