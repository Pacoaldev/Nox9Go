package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleTraeExchange_pasteToken(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("POST", "/api/oauth/trae/exchange",
		strings.NewReader(`{"code":"some-cloudide-jwt-token-value"}`))
	rec := httptest.NewRecorder()
	handler.HandleTraeExchange(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"provider":"trae"`) {
		t.Errorf("missing provider: %s", rec.Body.String())
	}
}

func TestHandleTraeAuthorize_shape(t *testing.T) {
	// Guidance needs network; accept either success or clean gateway error.
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("GET", "/api/oauth/trae/authorize", nil)
	rec := httptest.NewRecorder()
	handler.HandleTraeAuthorize(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 200/502, got %d", rec.Code)
	}
}

func TestHandleWindsurfAuthorize_shape(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("GET", "/api/oauth/windsurf/authorize", nil)
	rec := httptest.NewRecorder()
	handler.HandleWindsurfAuthorize(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	for _, want := range []string{"windsurf.com/windsurf/signin", "response_type", "3GUryQ7ldAeKEuD2obYnppsnmj58eP5u"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %q: %s", want, rec.Body.String())
		}
	}
}

func TestZedRoundTrip(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("GET", "/api/oauth/zed/authorize", nil)
	rec := httptest.NewRecorder()
	handler.HandleZedAuthorize(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "zed.dev/native_app_signin") || !strings.Contains(body, `"codeVerifier":"zed-rsa-pkcs1:`) {
		t.Fatalf("bad authorize shape: %s", body)
	}
	// Simulate Zed: encrypt a token with the public key from the URL.
	verifier := body[strings.Index(body, "zed-rsa-pkcs1:"):]
	verifier = verifier[:strings.Index(verifier, `"`)]
	pemBytes, err := decodeZedVerifier(verifier)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemBytes)
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &priv.PublicKey, []byte("plain-token-123"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cb := "http://127.0.0.1:58443/?user_id=u1&access_token=" + base64.RawURLEncoding.EncodeToString(cipher)
	req2 := httptest.NewRequest("POST", "/api/oauth/zed/exchange",
		strings.NewReader(`{"code":"`+cb+`","codeVerifier":"`+verifier+`"}`))
	rec2 := httptest.NewRecorder()
	handler.HandleZedExchange(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"status":"authorized"`) {
		t.Errorf("not authorized: %s", rec2.Body.String())
	}
}
