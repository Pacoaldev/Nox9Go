//go:build !windows

package cloudflare

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
