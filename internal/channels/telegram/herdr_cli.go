package telegram

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"nox9go/proxy/internal/config"
)

func herdrAgentTarget(ctx context.Context) (string, error) {
	return ResolveHerdrAgentName(ctx, config.HerdrAgentTarget())
}

func readHerdrAgentScreen(ctx context.Context) (string, error) {
	if runtime.GOOS == "windows" {
		// Herdr CLI is an external tool on the operator workstation.
	}
	target, err := herdrAgentTarget(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	sources := []string{"detection", "recent-unwrapped", "visible"}
	var best string
	var lastErr error
	for _, src := range sources {
		text, err := readHerdrAgentScreenSource(ctx, target, src, 80)
		if err != nil {
			lastErr = err
			continue
		}
		if text == "" {
			continue
		}
		best = text
		if _, ch := ParseHerdrDialog(text); len(ch) > 0 {
			return text, nil
		}
	}
	if best != "" {
		return best, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("herdr agent read: empty output for %s", target)
}

func readHerdrAgentScreenSource(ctx context.Context, target, source string, lines int) (string, error) {
	cmd := exec.CommandContext(ctx, "herdr", "agent", "read", target, "--lines", strconv.Itoa(lines), "--source", source)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if strings.Contains(text, `"code"`) && strings.Contains(text, `"error"`) {
		return "", fmt.Errorf("herdr agent read %s: %s", source, text)
	}
	if text != "" {
		return text, nil
	}
	if err != nil {
		return "", fmt.Errorf("herdr agent read %s: %w", source, err)
	}
	return "", nil
}

func sendHerdrAgentKeys(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return fmt.Errorf("herdr send-keys: empty keys")
	}
	target, err := herdrAgentTarget(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := append([]string{"agent", "send-keys", target}, keys...)
	cmd := exec.CommandContext(ctx, "herdr", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("herdr agent send-keys: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}