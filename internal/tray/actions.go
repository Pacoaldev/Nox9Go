package tray

import (
	"fmt"
	"runtime"

	"nox9go/proxy/internal/service"
)

// Actions is the seam the GUI layer calls. Each method is one menu item, so the
// binary holds no logic of its own.
//
// Stop deliberately has two shapes rather than one: the supervised path sends
// no credential, and the verified path shells out to the sibling CLI so the
// tray never puts the admin token on the wire itself (§C3).
type Actions struct {
	// Snapshot reads the current facts and resolves them.
	Snapshot func() Snapshot
	// Start boots the gateway through the CLI's own daemon path.
	Start func() error
	// StopSupervised stops through the init supervisor. No token is sent.
	StopSupervised func() error
	// StopVerified stops through the sibling CLI (`nox9go stop`), which
	// verifies the PID against the recorded executable before signalling.
	StopVerified func() error
	// AutostartEnabled reads the supervisor's state rather than a cached value.
	AutostartEnabled func() bool
	// SetAutostart installs or removes the boot entry.
	SetAutostart func(bool) error
}

// Stop runs the permitted stop path, or explains why there is none.
//
// The refusal text goes back to the caller so the menu can show it verbatim;
// it never contains a token (§C6).
func (a Actions) Stop() error {
	snap := a.Snapshot()
	if !snap.CanStop() {
		return ErrUnknown
	}
	info, ok := service.Status()
	path, refusal := resolveStop(info, ok, socketVerified(), cliStopFallback())
	switch path {
	case StopSupervised:
		if err := a.StopSupervised(); err != nil {
			return fmt.Errorf("%s: %w", RefuseSupervisorStopFail, err)
		}
		return nil
	case StopVerified:
		return a.StopVerified()
	default:
		return fmt.Errorf("%s", refusalText(refusal, snap))
	}
}

// resolveStop wraps DecideStop with the Windows/macOS CLI fallback.
//
// DecideStop itself stays fail-closed when there is no socket-owner proof: that
// is the removed claim-only / in-process-token path. On platforms without L2,
// Start already shells out to the sibling CLI; Stop does the same so the menu
// item works without the tray ever holding the credential.
func resolveStop(info service.Info, ok, socketOK, allowCLIFallback bool) (StopPath, Refusal) {
	path, refusal := DecideStop(info, ok, socketOK)
	if path != StopRefused || !allowCLIFallback {
		return path, refusal
	}
	if refusal == RefuseNoSocketOwner || refusal == RefuseWindowsTask {
		return StopVerified, RefuseNone
	}
	return path, refusal
}

// cliStopFallback is true where L2 (socket-owner proof) does not exist, so the
// tray may shell out to `nox9go stop` instead of refusing the menu item.
func cliStopFallback() bool {
	return runtime.GOOS != "linux"
}

// refusalText renders a refusal for a specific snapshot. Kept here rather than
// in the constants so the port and PID interpolation lives next to the use.
func refusalText(r Refusal, snap Snapshot) string {
	switch r {
	case RefuseNoSocketOwner:
		return fmt.Sprintf(string(r), snap.Port)
	case RefuseForeignPortOwner:
		return fmt.Sprintf(string(r), snap.Port, snap.PID)
	default:
		return string(r)
	}
}

// socketVerified is the L2 seam: today only Linux can answer "who owns this
// socket", and the answer needs the port, so the real implementation lives in
// the platform file. This default is the fail-closed one.
var socketVerified = func() bool { return false }
