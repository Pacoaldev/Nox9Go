package cloudflare

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"nox9go/proxy/internal/proc"
)

var tryCloudflareURL = regexp.MustCompile(`https://([a-z0-9-]+)\.trycloudflare\.com`)

var (
	procMu          sync.Mutex
	cloudflaredCmd  *exec.Cmd
	intentionalKill bool
	activeLocalPort int
)

// Running reports whether the tracked cloudflared PID is still alive.
func Running() bool {
	procMu.Lock()
	cmd := cloudflaredCmd
	procMu.Unlock()
	if cmd != nil && cmd.Process != nil && proc.Alive(cmd.Process.Pid) {
		return true
	}
	pid := loadPID()
	return pid > 0 && proc.Alive(pid)
}

// Kill stops the managed cloudflared (and any leftover PID from a prior run).
func Kill(localPort int) {
	procMu.Lock()
	intentionalKill = true
	cmd := cloudflaredCmd
	cloudflaredCmd = nil
	procMu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if pid := loadPID(); pid > 0 {
		_ = proc.ForceKill(pid)
		clearPID()
	}
	if localPort > 0 {
		killByPort(localPort)
	}
	activeLocalPort = 0

	procMu.Lock()
	intentionalKill = false
	procMu.Unlock()
}

// SpawnQuickTunnel starts cloudflared quick tunnel and waits for the public URL.
func SpawnQuickTunnel(ctx context.Context, localPort int) (tunnelURL string, err error) {
	bin, err := EnsureBinary()
	if err != nil {
		return "", err
	}

	cfgDir, err := os.MkdirTemp("", "cloudflared-quick-")
	if err != nil {
		return "", fmt.Errorf("cloudflare: temp config: %w", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("# quick-tunnel placeholder\n"), 0o644); err != nil {
		_ = os.RemoveAll(cfgDir)
		return "", err
	}
	cleanup := sync.OnceFunc(func() { _ = os.RemoveAll(cfgDir) })
	defer cleanup()

	protocol := strings.ToLower(strings.TrimSpace(os.Getenv("TUNNEL_TRANSPORT_PROTOCOL")))
	if protocol == "" {
		protocol = strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDFLARED_PROTOCOL")))
	}
	if protocol != "http2" && protocol != "quic" && protocol != "auto" {
		protocol = "http2"
	}

	args := []string{
		"tunnel",
		"--url", fmt.Sprintf("http://127.0.0.1:%d", localPort),
		"--config", cfgPath,
		"--no-autoupdate",
		"--retries", "99",
	}
	// Do not use CommandContext: the request context ends when Enable returns,
	// and cancelling it would kill the tunnel the dashboard just started.
	cmd := exec.Command(bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "TUNNEL_TRANSPORT_PROTOCOL="+protocol)
	hideConsole(cmd)
	_ = ctx // reserved for the wait timeout below

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("cloudflare: start cloudflared: %w", err)
	}

	procMu.Lock()
	cloudflaredCmd = cmd
	activeLocalPort = localPort
	intentionalKill = false
	procMu.Unlock()
	_ = savePID(cmd.Process.Pid)

	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)
	var logTail strings.Builder

	readLogs := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				msg := string(buf[:n])
				if logTail.Len() > 3500 {
					s := logTail.String()
					logTail.Reset()
					logTail.WriteString(s[len(s)-2000:])
				}
				logTail.WriteString(msg)
				if u := parseQuickTunnelURL(msg); u != "" {
					select {
					case urlCh <- u:
					default:
					}
				}
			}
			if readErr != nil {
				return
			}
		}
	}
	go readLogs(stdout)
	go readLogs(stderr)

	go func() {
		waitErr := cmd.Wait()
		procMu.Lock()
		wasIntentional := intentionalKill
		if cloudflaredCmd == cmd {
			cloudflaredCmd = nil
		}
		procMu.Unlock()
		clearPID()
		cleanup()
		if wasIntentional {
			select {
			case errCh <- fmt.Errorf("cloudflared killed"):
			default:
			}
			return
		}
		if waitErr != nil {
			select {
			case errCh <- fmt.Errorf("cloudflared exited: %w; last log: %s", waitErr, trimTail(logTail.String(), 600)):
			default:
			}
		}
	}()

	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	select {
	case u := <-urlCh:
		cleanup()
		return u, nil
	case err := <-errCh:
		_ = cmd.Process.Kill()
		return "", err
	case <-waitCtx.Done():
		Kill(localPort)
		return "", fmt.Errorf("cloudflare: quick tunnel timed out; last log: %s", trimTail(logTail.String(), 800))
	}
}

func parseQuickTunnelURL(message string) string {
	matches := tryCloudflareURL.FindAllStringSubmatch(message, -1)
	var last string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		if m[1] == "api" {
			continue
		}
		last = "https://" + m[1] + ".trycloudflare.com"
	}
	return last
}

func trimTail(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(empty)"
	}
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func killByPort(port int) {
	if port <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		ps := fmt.Sprintf(
			`Get-CimInstance Win32_Process -Filter "Name='cloudflared.exe'" | Where-Object { $_.CommandLine -match ':%d(\D|$)' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }`,
			port,
		)
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", ps)
		hideConsole(cmd)
		_ = cmd.Run()
		return
	}
	cmd := exec.Command("pkill", "-f", fmt.Sprintf(`cloudflared.*:%d([^0-9]|$)`, port))
	_ = cmd.Run()
}
