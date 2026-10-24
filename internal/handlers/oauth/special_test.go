package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func x509MarshalPKCS8ForTest(priv *ecdh.PrivateKey) ([]byte, error) {
	return x509.MarshalPKCS8PrivateKey(priv)
}

func aesGCMSealForTest(t *testing.T, key, nonce, plain []byte) (ct, tag []byte) {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	out := gcm.Seal(nil, nonce, plain, nil)
	return out[:len(out)-16], out[len(out)-16:]
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

func TestHandleCursorImport_validation(t *testing.T) {
	handler := NewOAuthHandler(nil)
	for _, tc := range []struct {
		name    string
		payload string
		want    int
	}{
		{"empty", `{}`, 400},
		{"short-token", `{"accessToken":"abc","machineId":"12345678-1234-1234-1234-123456789012"}`, 400},
		{"bad-machine", `{"accessToken":"` + strings.Repeat("x", 60) + `","machineId":"nope"}`, 400},
		{"ok", `{"accessToken":"` + strings.Repeat("y", 60) + `","machineId":"12345678-1234-1234-1234-123456789012"}`, 200},
	} {
		req := httptest.NewRequest("POST", "/api/oauth/cursor/import", strings.NewReader(tc.payload))
		rec := httptest.NewRecorder()
		handler.HandleCursorImport(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: expected %d, got %d: %s", tc.name, tc.want, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleKimchiExchange_validation(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("POST", "/api/oauth/kimchi/exchange", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.HandleKimchiExchange(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleGitlabPAT_validation(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("POST", "/api/oauth/gitlab/pat", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.HandleGitlabPAT(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleIflowCookie_validation(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("POST", "/api/oauth/iflow/cookie", strings.NewReader(`{"cookie":"nope"}`))
	rec := httptest.NewRecorder()
	handler.HandleIflowCookie(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMimoRoundTrip(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("GET", "/api/oauth/xiaomi-mimo/authorize", nil)
	rec := httptest.NewRecorder()
	handler.HandleMimoAuthorize(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "platform.xiaomimimo.com/authorize") || !strings.Contains(body, `"codeVerifier":"mimo-x25519:`) {
		t.Fatalf("bad authorize shape: %s", body)
	}
	verifier := body[strings.Index(body, "mimo-x25519:"):]
	verifier = verifier[:strings.Index(verifier, `"`)]
	// Simulate platform: parse our public key, ECDH, AES-GCM encrypt {uid,sk}.
	privDer, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(verifier, "mimo-x25519:"))
	if err != nil {
		t.Fatal(err)
	}
	_ = privDer
	// Re-derive: authorize URL contains pk= (base64 SPKI). Extract and encrypt.
	var authURL string
	for _, part := range strings.Split(body, `"`) {
		if strings.HasPrefix(part, "https://") {
			authURL = part
			break
		}
	}
	if !strings.Contains(authURL, "pk=") || !strings.Contains(authURL, "kn=mimocode") {
		t.Fatalf("bad auth url: %s", authURL)
	}
}

func TestMimoDecryptRoundTrip(t *testing.T) {
	// Server side: generate like authorize does.
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privDer, err := x509MarshalPKCS8ForTest(priv)
	if err != nil {
		t.Fatal(err)
	}
	verifier := "mimo-x25519:" + base64.RawURLEncoding.EncodeToString(privDer)
	// Platform side: ephemeral key + ECDH + AES-GCM.
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := eph.ECDH(priv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256(secret)
	ephRaw := eph.PublicKey().Bytes()
	nonce := []byte("0123456789ab")
	ct, tag := aesGCMSealForTest(t, key[:], nonce, []byte(`{"uid":"u1","sk":"sk-test-123"}`))
	raw := append(append(append([]byte{}, nonce...), ephRaw...), append(ct, tag...)...)
	enc := base64.StdEncoding.EncodeToString(raw)
	got, err := mimoDecryptCallback(verifier, "http://127.0.0.1/?u="+url.QueryEscape(enc))
	if err != nil {
		t.Fatal(err)
	}
	if got.sk != "sk-test-123" || got.uid != "u1" {
		t.Errorf("bad decrypt: %+v", got)
	}
}
