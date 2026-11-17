package cloudflare

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// resumeTunnelBeforeReads makes the delay deterministic: the guard table below
// must not wait out the boot defer.
var resumeTunnelBeforeReads = func() time.Duration { return 0 }

// TestResumeTunnelGuards pins the four guards upstream's safeRestartTunnel
// applies at boot: settings read, tunnelEnabled, and the running process.
// The "already running" case is the load-bearing one: an orphaned cloudflared
// from a previous session keeps serving the same trycloudflare URL, so
// respawning it would rotate the URL for no reason.
func TestResumeTunnelGuards(t *testing.T) {
	settingsErr := errors.New("db unavailable")

	tests := []struct {
		name        string
		getSettings func() (map[string]any, error)
		running     bool
		wantCalls   int32
	}{
		{
			name:        "settings read error resumes nothing",
			getSettings: func() (map[string]any, error) { return nil, settingsErr },
			wantCalls:   0,
		},
		{
			name:        "nil settings resumes nothing",
			getSettings: func() (map[string]any, error) { return nil, nil },
			wantCalls:   0,
		},
		{
			name:        "tunnel disabled resumes nothing",
			getSettings: func() (map[string]any, error) { return map[string]any{"tunnelEnabled": false}, nil },
			wantCalls:   0,
		},
		{
			name:        "missing tunnelEnabled resumes nothing",
			getSettings: func() (map[string]any, error) { return map[string]any{}, nil },
			wantCalls:   0,
		},
		{
			name:        "enabled and already running keeps the live url",
			getSettings: func() (map[string]any, error) { return map[string]any{"tunnelEnabled": true}, nil },
			running:     true,
			wantCalls:   0,
		},
		{
			name:        "enabled and not running resumes the tunnel",
			getSettings: func() (map[string]any, error) { return map[string]any{"tunnelEnabled": true}, nil },
			running:     false,
			wantCalls:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			enable := func(context.Context, int) (EnableResult, error) {
				calls.Add(1)
				return EnableResult{TunnelURL: "https://resumed.trycloudflare.com"}, nil
			}

			resumeTunnel(context.Background(), 20130, tt.getSettings,
				func() bool { return tt.running }, enable, nil, resumeTunnelBeforeReads)

			if got := calls.Load(); got != tt.wantCalls {
				t.Fatalf("enable calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

// TestResumeTunnelEnableFailureIsContained: a failed spawn at boot must not
// panic or spin; the operator still has the dashboard button, and the next
// gateway start tries again.
func TestResumeTunnelEnableFailureIsContained(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})

	go func() {
		defer close(done)
		resumeTunnel(context.Background(), 20130,
			func() (map[string]any, error) { return map[string]any{"tunnelEnabled": true}, nil },
			func() bool { return false },
			func(context.Context, int) (EnableResult, error) {
				calls.Add(1)
				return EnableResult{}, errors.New("cloudflared download failed")
			},
			nil,
			resumeTunnelBeforeReads)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resumeTunnel blocked on a failed enable")
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("enable calls = %d, want 1", got)
	}
}

// TestResumeTunnelHonoursContext stops the deferred boot attempt when the
// gateway shuts down before the delay expires: nothing should spawn against a
// closed listener.
func TestResumeTunnelHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls atomic.Int32
	resumeTunnel(ctx, 20130,
		func() (map[string]any, error) { return map[string]any{"tunnelEnabled": true}, nil },
		func() bool { return false },
		func(context.Context, int) (EnableResult, error) {
			calls.Add(1)
			return EnableResult{}, nil
		},
		nil,
		func() time.Duration { return time.Hour })

	if got := calls.Load(); got != 0 {
		t.Fatalf("enable calls = %d, want 0 after a cancelled context", got)
	}
}

// TestResumeTunnelPassesPort pins the port the spawn targets: the tunnel must
// dial the gateway's own listener, not the package default.
func TestResumeTunnelPassesPort(t *testing.T) {
	var gotPort atomic.Int32
	resumeTunnel(context.Background(), 21234,
		func() (map[string]any, error) { return map[string]any{"tunnelEnabled": true}, nil },
		func() bool { return false },
		func(_ context.Context, port int) (EnableResult, error) {
			gotPort.Store(int32(port))
			return EnableResult{}, nil
		},
		nil,
		resumeTunnelBeforeReads)

	if got := gotPort.Load(); got != 21234 {
		t.Fatalf("enable port = %d, want 21234", got)
	}
}

// TestResumeTunnelReportsSpawnedUrls pins the persistence hook: Enable does not
// write settings, so a resume that skipped onEnabled would leave the row
// advertising the direct URL of a process that no longer exists.
func TestResumeTunnelReportsSpawnedUrls(t *testing.T) {
	var got EnableResult
	resumeTunnel(context.Background(), 20130,
		func() (map[string]any, error) { return map[string]any{"tunnelEnabled": true}, nil },
		func() bool { return false },
		func(context.Context, int) (EnableResult, error) {
			return EnableResult{
				TunnelURL: "https://fresh.trycloudflare.com",
				PublicURL: "https://abc123.abc-tunnel.us",
				ShortID:   "abc123",
			}, nil
		},
		func(res EnableResult) { got = res },
		resumeTunnelBeforeReads)

	if got.TunnelURL != "https://fresh.trycloudflare.com" {
		t.Fatalf("onEnabled tunnel url = %q, want the spawned one", got.TunnelURL)
	}
	if got.PublicURL != "https://abc123.abc-tunnel.us" {
		t.Fatalf("onEnabled public url = %q, want the registered one", got.PublicURL)
	}
}

// TestResumeTunnelSkipsHookOnFailure: a failed spawn has no URLs to persist.
func TestResumeTunnelSkipsHookOnFailure(t *testing.T) {
	called := false
	resumeTunnel(context.Background(), 20130,
		func() (map[string]any, error) { return map[string]any{"tunnelEnabled": true}, nil },
		func() bool { return false },
		func(context.Context, int) (EnableResult, error) {
			return EnableResult{TunnelURL: "https://half.trycloudflare.com"}, errors.New("spawn failed")
		},
		func(EnableResult) { called = true },
		resumeTunnelBeforeReads)

	if called {
		t.Fatal("onEnabled ran for a failed spawn")
	}
}
