package cloudflare

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/constants"
)

const defaultWorkerURL = "https://abc-tunnel.us"

// WorkerURL is the public short-URL service that turns a trycloudflare URL
// into a stable `https://r<shortId>.abc-tunnel.us` hostname.
//
// Upstream decolua/9router registers the quick-tunnel URL with this worker
// (src/lib/tunnel/cloudflare/manager.js → registerTunnelUrl) and hands the
// dashboard the short hostname. The direct trycloudflare.com URL still changes
// on every respawn, so a client configured with it breaks on the next
// restart; the short hostname survives because the worker keeps the mapping.
//
// It is a var so a test can point it at a local server.
var WorkerURL = defaultWorkerURL

// shortIDLength and shortIDChars mirror upstream shared/state.js
// generateShortId(): 6 characters from an alphabet without look-alike glyphs
// (no 0/o, no 1/l/i).
const (
	shortIDLength = 6
	shortIDChars  = "abcdefghijklmnpqrstuvwxyz23456789"
)

// registerClient is the HTTP client used to talk to the worker. A dedicated
// client with a short timeout keeps a hung worker from stalling the tunnel
// enable path, which the dashboard waits on.
var registerClient = &http.Client{Timeout: 10 * time.Second}

// generateShortID returns a random short id in upstream's format.
func generateShortID() string {
	b := make([]byte, shortIDLength)
	for i := range b {
		b[i] = shortIDChars[rand.Intn(len(shortIDChars))]
	}
	return string(b)
}

// statePath is where the short id is persisted, next to the cloudflared PID
// file. The mapping lives on the worker's side, so this file only has to
// remember WHICH id we registered — otherwise every respawn would hand the
// dashboard a different hostname.
func statePath() string {
	return filepath.Join(config.ResolveDataDir(), "tunnel", "state.json")
}

// tunnelState is the persisted worker registration.
type tunnelState struct {
	ShortID   string `json:"shortId"`
	TunnelURL string `json:"tunnelUrl"`
}

// loadState reads the persisted worker registration. A missing or corrupt file
// returns ok=false so the caller generates a fresh id.
func loadState() (tunnelState, bool) {
	data, err := os.ReadFile(statePath())
	if err != nil {
		return tunnelState{}, false
	}
	var st tunnelState
	if err := json.Unmarshal(data, &st); err != nil || st.ShortID == "" {
		return tunnelState{}, false
	}
	return st, true
}

// saveState persists the worker registration.
func saveState(st tunnelState) error {
	dir := filepath.Dir(statePath())
	if err := os.MkdirAll(dir, constants.FilePermDir); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), data, constants.FilePermFile)
}

// clearState removes the persisted worker registration.
func clearState() {
	_ = os.Remove(statePath())
}

// PublicURLFor returns the short public hostname for a short id.
func PublicURLFor(shortID string) string {
	if shortID == "" {
		return ""
	}
	worker := strings.TrimSuffix(WorkerURL, "/")
	scheme, host, ok := strings.Cut(worker, "://")
	if !ok {
		scheme, host = "https", worker
	}
	return scheme + "://r" + shortID + "." + host
}

// RegisterShortURL registers tunnelURL with the worker under shortID and
// returns the public short hostname.
//
// The worker is a third-party service. When it is unreachable the caller
// still gets a working tunnel: the direct trycloudflare.com URL remains
// usable, so a registration failure degrades the public URL rather than
// failing the whole enable.
func RegisterShortURL(ctx context.Context, shortID, tunnelURL string) (string, error) {
	if shortID == "" || tunnelURL == "" {
		return "", fmt.Errorf("cloudflare: short id and tunnel url are required")
	}
	body, err := json.Marshal(map[string]string{
		"shortId":   shortID,
		"tunnelUrl": tunnelURL,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		WorkerURL+"/api/tunnel/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := registerClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloudflare: register short url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("cloudflare: register short url: status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return PublicURLFor(shortID), nil
}
