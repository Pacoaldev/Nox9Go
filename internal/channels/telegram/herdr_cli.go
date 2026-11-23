package telegram

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
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
	cmd := exec.CommandContext(ctx, "herdr", "agent", "read", target, "--lines", "40", "--source", "visible")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		text := strings.TrimSpace(out.String())
		if text != "" {
			return text, nil
		}
		return "", fmt.Errorf("herdr agent read: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
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