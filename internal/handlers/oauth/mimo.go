package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"

	"nox9go/proxy/internal/handlerutil"
)

var (
	mimoPlatformURL = "https://platform.xiaomimimo.com"
	mimoKn          = "mimocode"
)

// HandleMimoAuthorize generates an X25519 keypair and returns the authorize URL.
// GET /api/oauth/xiaomi-mimo/authorize — mirrors upstream ECDH flow.
func (h *OAuthHandler) HandleMimoAuthorize(w http.ResponseWriter, r *http.Request) {
	redirectURI := callbackRedirectURI(r)
	keyName := r.URL.Query().Get("key_name")
	if keyName == "" {
		hn, _ := os.Hostname()
		sum := sha256.Sum256([]byte(runtime.GOOS + "-" + hn))
		keyName = fmt.Sprintf("nox9go-xmd-%x", sum[:4])
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "generate keypair failed")
		return
	}
	pubDer, err := x509.MarshalPKIXPublicKey(priv.PublicKey())
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "marshal public key failed")
		return
	}
	privDer, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "marshal private key failed")
		return
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubDer)
	verifier := "mimo-x25519:" + base64.RawURLEncoding.EncodeToString(privDer)
	p := url.Values{"pk": {pubB64}, "redirect_uri": {redirectURI}, "kn": {mimoKn}}
	if keyName != "" {
		p.Set("key_name", keyName)
	}
	authURL := mimoPlatformURL + "/authorize?" + p.Encode()
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"url": authURL, "authUrl": authURL, "codeVerifier": verifier,
		"redirectUri": redirectURI, "flowType": "ecdh", "provider": "xiaomi-mimo",
	})
}

// HandleMimoExchange decrypts the callback payload and stores the connection.
// POST /api/oauth/xiaomi-mimo/exchange
func (h *OAuthHandler) HandleMimoExchange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"codeVerifier"`
		Name         string `json:"name"`
	}
	if raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	u, err := mimoDecryptCallback(body.CodeVerifier, body.Code)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if u.sk == "" {
		handlerutil.WriteJSONError(w, http.StatusBadGateway, "missing sk in decrypted payload")
		return
	}
	name := body.Name
	if name == "" {
		name = "Xiaomi MiMo"
		if u.uid != "" {
			name += " (" + u.uid + ")"
		}
	}
	connID := "xiaomi-mimo-" + shortHash(u.sk)
	dataMap := map[string]any{"apiKey": u.sk, "accessToken": u.sk}
	if u.uid != "" {
		dataMap["email"] = u.uid
	}
	dataMap["providerSpecificData"] = map[string]any{"authMethod": "ecdh", "uid": u.uid}
	if u.url != "" {
		dataMap["providerSpecificData"].(map[string]any)["url"] = u.url
	}
	h.saveSpecialConnection(w, "xiaomi-mimo", connID, name, u.uid, dataMap, nil)
}

type mimoPayload struct {
	uid, sk, url string
}

func mimoDecryptCallback(verifier, code string) (mimoPayload, error) {
	const prefix = "mimo-x25519:"
	if !strings.HasPrefix(verifier, prefix) {
		return mimoPayload{}, fmt.Errorf("missing key verifier; restart the login flow")
	}
	privDer, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(verifier, prefix))
	if err != nil {
		return mimoPayload{}, fmt.Errorf("bad verifier: %v", err)
	}
	privAny, err := x509.ParsePKCS8PrivateKey(privDer)
	if err != nil {
		return mimoPayload{}, fmt.Errorf("bad private key: %v", err)
	}
	priv, ok := privAny.(*ecdh.PrivateKey)
	if !ok {
		return mimoPayload{}, fmt.Errorf("bad private key type")
	}
	enc := extractMimoU(code)
	if enc == "" {
		return mimoPayload{}, fmt.Errorf("missing u parameter in callback")
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		if raw2, err2 := base64.RawURLEncoding.DecodeString(enc); err2 == nil {
			raw = raw2
		} else {
			return mimoPayload{}, fmt.Errorf("bad payload encoding")
		}
	}
	if len(raw) < 12+32+16+1 {
		return mimoPayload{}, fmt.Errorf("encrypted payload too short: %d bytes", len(raw))
	}
	nonce, ephRaw := raw[:12], raw[12:44]
	ctAndTag := raw[44:]
	tag, ct := ctAndTag[len(ctAndTag)-16:], ctAndTag[:len(ctAndTag)-16]
	// X25519 NewPublicKey takes the raw 32-byte key (Node prepends an SPKI
	// prefix only for its own API; Go wants the raw coordinate).
	ephPub, err := ecdh.X25519().NewPublicKey(ephRaw)
	if err != nil {
		return mimoPayload{}, fmt.Errorf("bad ephemeral key: %v", err)
	}
	secret, err := priv.ECDH(ephPub)
	if err != nil {
		return mimoPayload{}, fmt.Errorf("ECDH failed: %v", err)
	}
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return mimoPayload{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return mimoPayload{}, err
	}
	plain, err := gcm.Open(nil, nonce, append(ct, tag...), nil)
	if err != nil {
		return mimoPayload{}, fmt.Errorf("decrypt failed: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(plain, &obj); err != nil {
		return mimoPayload{}, fmt.Errorf("bad payload JSON")
	}
	out := mimoPayload{url: "https://api.xiaomimimo.com/v1"}
	if v, _ := obj["uid"].(string); v != "" {
		out.uid = v
	}
	if v, _ := obj["sk"].(string); v != "" {
		out.sk = v
	}
	if v, _ := obj["url"].(string); v != "" {
		out.url = v
	}
	return out, nil
}

// x25519SPKIPrefix is the DER prefix for a raw 32-byte X25519 public key.
func x25519SPKIPrefix() []byte {
	return []byte{0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x6e, 0x03, 0x21, 0x00}
}

func extractMimoU(code string) string {
	text := strings.TrimSpace(code)
	if text == "" {
		return ""
	}
	// Raw base64 blob (the `u` value itself).
	if !strings.ContainsAny(text, "?&=#{") && !strings.Contains(text, " ") {
		return text
	}
	if strings.HasPrefix(text, "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(text), &obj) == nil {
			if v, _ := obj["u"].(string); v != "" {
				return v
			}
		}
		return ""
	}
	q := text
	if i := strings.Index(text, "?"); i >= 0 {
		q = text[i+1:]
	}
	vals, _ := url.ParseQuery(q)
	// Upstream names the encrypted blob `u`; accept common aliases.
	return firstNonEmpty(vals.Get("u"), vals.Get("access_token"), vals.Get("accessToken"), vals.Get("token"))
}
