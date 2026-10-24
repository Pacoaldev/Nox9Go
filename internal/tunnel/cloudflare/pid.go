package cloudflare

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/constants"
)

func tunnelDir() string {
	return filepath.Join(config.ResolveDataDir(), "tunnel")
}

func pidPath() string {
	return filepath.Join(tunnelDir(), "cloudflared.pid")
}

func savePID(pid int) error {
	if err := os.MkdirAll(tunnelDir(), constants.FilePermDir); err != nil {
		return err
	}
	return os.WriteFile(pidPath(), []byte(strconv.Itoa(pid)), constants.FilePermFile)
}

func loadPID() int {
	data, err := os.ReadFile(pidPath())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func clearPID() {
	_ = os.Remove(pidPath())
}
