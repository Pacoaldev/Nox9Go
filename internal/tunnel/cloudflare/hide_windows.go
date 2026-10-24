//go:build windows

package cloudflare

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideConsole prevents cloudflared (console subsystem) from flashing a Terminal.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
