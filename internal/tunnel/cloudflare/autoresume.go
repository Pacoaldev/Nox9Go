package cloudflare

import (
	"context"
	"time"

	"nox9go/proxy/internal/log"
)

// autoResumeDelay is how long the boot resume waits before reading settings.
//
// Upstream defers its heavy startup by STARTUP_DEFER_MS (3000ms,
// src/shared/services/initializeApp.js) so the first HTTP request is not
// starved, and it is the same window this process needs: cloudflared dials
// 127.0.0.1:<port>, so the listener has to be accepting before the spawn, and
// a cold binary download must not compete with the dashboard's boot requests.
const autoResumeDelay = 3 * time.Second

// StartAutoResume brings an enabled Cloudflare quick tunnel back after a
// gateway restart.
//
// Ported from upstream initializeApp.js (runHeavyStartup: when
// settings.tunnelEnabled, safeRestartTunnel("startup")). The fork only ever
// spawned cloudflared from the dashboard's Enable button, so after a restart —
// or a machine reboot, which kills the child process the gateway never got to
// track — settings kept saying "enabled" while no process existed. The
// dashboard then parked on "Tunnel checking..." forever, because that state
// has no re-enable path in the UI; the operator had to power-cycle the tunnel
// by hand on every boot.
//
// Guards mirror upstream's safeRestartTunnel:
//   - tunnelEnabled, otherwise there is nothing the operator asked to keep;
//   - Running(), because an orphaned cloudflared from a previous session still
//     serves the same trycloudflare URL, and respawning would rotate it;
//   - the shutdown context, so a gateway that stops during the deferred window
//     does not spawn a tunnel against a closed listener.
//
// A failed spawn is logged and left alone: the dashboard button remains the
// manual path, and the next start tries again.
//
// onEnabled, when not nil, receives the freshly spawned tunnel so the caller
// can persist the new URLs: Enable does not touch settings (that is the
// dashboard handler's job), and a resume that skipped it would leave the row
// advertising the direct URL of a process that no longer exists.
func StartAutoResume(
	ctx context.Context,
	getSettings func() (map[string]any, error),
	localPort int,
	onEnabled func(EnableResult),
) {
	if getSettings == nil {
		return
	}
	go resumeTunnel(ctx, localPort, getSettings, Running, Enable, onEnabled, func() time.Duration {
		return autoResumeDelay
	})
}

// resumeTunnel is the dependency-injected core of StartAutoResume. The seams
// exist so the guard table is testable without a real cloudflared process, a
// real settings row or a three-second boot wait.
func resumeTunnel(
	ctx context.Context,
	localPort int,
	getSettings func() (map[string]any, error),
	running func() bool,
	enable func(context.Context, int) (EnableResult, error),
	onEnabled func(EnableResult),
	delay func() time.Duration,
) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay()):
	}

	raw, err := getSettings()
	if err != nil || raw == nil {
		return
	}
	enabled, _ := raw["tunnelEnabled"].(bool)
	if !enabled {
		return
	}
	if running() {
		// An orphaned cloudflared from a previous gateway still serves the
		// registered URL; a respawn would only rotate it.
		return
	}

	log.Info("tunnel", "resuming enabled cloudflare tunnel")
	res, err := enable(ctx, localPort)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("tunnel", "auto-resume failed; the dashboard button remains the manual path", "error", err)
		}
		return
	}
	if onEnabled != nil && res.TunnelURL != "" {
		onEnabled(res)
	}
}
