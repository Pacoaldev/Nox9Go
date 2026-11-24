package telegram

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func readHerdrScreenSource(ctx context.Context, target herdrIOTarget, source string, lines int) (string, error) {
	var cmd *exec.Cmd
	if target.isPane() {
		cmd = exec.CommandContext(ctx, "herdr", "pane", "read", target.pane,
			"--lines", strconv.Itoa(lines), "--source", source)
	} else {
		cmd = exec.CommandContext(ctx, "herdr", "agent", "read", target.agent,
			"--lines", strconv.Itoa(lines), "--source", source)
	}
	out, err := runHerdrCapture(cmd)
	text := strings.TrimSpace(out)
	if strings.Contains(text, `"code"`) && strings.Contains(text, `"error"`) {
		return "", fmt.Errorf("herdr read %s: %s", source, text)
	}
	if text != "" {
		return text, nil
	}
	if err != nil {
		return "", fmt.Errorf("herdr read %s: %w", source, err)
	}
	return "", nil
}

func sendHerdrKeys(ctx context.Context, target herdrIOTarget, keys ...string) error {
	if len(keys) == 0 {
		return fmt.Errorf("herdr send-keys: empty keys")
	}
	var args []string
	if target.isPane() {
		args = append([]string{"pane", "send-keys", target.pane}, keys...)
	} else {
		args = append([]string{"agent", "send-keys", target.agent}, keys...)
	}
	cmd := exec.CommandContext(ctx, "herdr", args...)
	out, err := runHerdrCapture(cmd)
	if err != nil {
		return fmt.Errorf("herdr send-keys: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func runHerdrCapture(cmd *exec.Cmd) (string, error) {
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}