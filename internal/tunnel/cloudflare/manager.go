package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	mgrMu    sync.Mutex
	enabling bool
)

// EnableResult is what POST /api/tunnel/enable returns to the dashboard.
type EnableResult struct {
	TunnelURL      string
	PublicURL      string
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

	return EnableResult{
		TunnelURL: url,
		PublicURL: url, // personal builds use the trycloudflare URL directly
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
}

// Snapshot builds status from persisted settings plus the live process.
func Snapshot(settingsEnabled bool, settingsURL string) StatusSnapshot {
	running := false
	if settingsEnabled {
		running = Running()
	}
	url := settingsURL
	return StatusSnapshot{
		Enabled:         settingsEnabled && running && url != "",
		SettingsEnabled: settingsEnabled,
		TunnelURL:       url,
		PublicURL:       url,
		ShortID:         "",
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
