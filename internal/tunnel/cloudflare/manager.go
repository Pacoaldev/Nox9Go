package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nox9go/proxy/internal/log"
)

var (
	mgrMu    sync.Mutex
	enabling bool
)

// EnableResult is what POST /api/tunnel/enable returns to the dashboard.
type EnableResult struct {
	TunnelURL      string
	PublicURL      string
	ShortID        string
	AlreadyRunning bool
}

// StatusSnapshot is the Cloudflare half of GET /api/tunnel/status.
type StatusSnapshot struct {
	Enabled         bool
	SettingsEnabled bool
	TunnelURL       string
	PublicURL       string
	ShortID         string
	Running         bool
}

// Enable starts a Cloudflare quick tunnel to localPort (trycloudflare.com).
// No Cloudflare account is required.
func Enable(ctx context.Context, localPort int) (EnableResult, error) {
	if localPort <= 0 {
		localPort = 20130
	}

	mgrMu.Lock()
	if enabling {
		mgrMu.Unlock()
		return EnableResult{}, fmt.Errorf("cloudflare: tunnel enable already in progress")
	}
	enabling = true
	mgrMu.Unlock()
	defer func() {
		mgrMu.Lock()
		enabling = false
		mgrMu.Unlock()
	}()

	if Running() {
		// Best-effort reuse is left to the caller via settings URL; a fresh
		// spawn always replaces a stale process so the dashboard gets a live URL.
	}

	Kill(localPort)

	url, err := SpawnQuickTunnel(ctx, localPort)
	if err != nil {
		return EnableResult{}, err
	}

	// The trycloudflare.com hostname changes on every respawn, so a client
	// configured with it breaks on the next restart. The short public hostname
	// survives: the worker keeps the short-id → URL mapping, and the id itself
	// is persisted so a respawn reuses it instead of handing out a new one.
	shortID := ""
	if st, ok := loadState(); ok {
		shortID = st.ShortID
	}
	if shortID == "" {
		shortID = generateShortID()
	}

	publicURL := url
	if registered, regErr := RegisterShortURL(ctx, shortID, url); regErr == nil {
		publicURL = registered
		_ = saveState(tunnelState{ShortID: shortID, TunnelURL: url})
	} else {
		// A worker outage degrades the public URL, not the tunnel: the direct
		// trycloudflare.com URL still routes traffic.
		log.Warn("tunnel", "short url registration failed, using direct url", "error", regErr)
	}

	return EnableResult{
		TunnelURL: url,
		PublicURL: publicURL,
		ShortID:   shortID,
	}, nil
}

// Disable stops cloudflared. Settings clearing is the caller's job.
func Disable(localPort int) {
	mgrMu.Lock()
	defer mgrMu.Unlock()
	if localPort <= 0 {
		localPort = activeLocalPort
	}
	Kill(localPort)
	// The short id survives a disable (upstream shared/state.js: disableTunnel
	// keeps the id and only clears the URL) so the next enable reuses the same
	// public hostname instead of handing the client a new one.
	if st, ok := loadState(); ok {
		_ = saveState(tunnelState{ShortID: st.ShortID})
	}
}

// Snapshot builds status from persisted settings plus the live process.
func Snapshot(settingsEnabled bool, settingsURL string) StatusSnapshot {
	running := false
	if settingsEnabled {
		running = Running()
	}
	url := settingsURL
	shortID := ""
	publicURL := url
	if st, ok := loadState(); ok {
		shortID = st.ShortID
		if p := PublicURLFor(shortID); p != "" {
			publicURL = p
		}
	}
	return StatusSnapshot{
		Enabled:         settingsEnabled && running && url != "",
		SettingsEnabled: settingsEnabled,
		TunnelURL:       url,
		PublicURL:       publicURL,
		ShortID:         shortID,
		Running:         running,
	}
}

// ProbeAlive checks whether url answers GET /api/health (dashboard client ping).
func ProbeAlive(ctx context.Context, url string) bool {
	if url == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/api/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
