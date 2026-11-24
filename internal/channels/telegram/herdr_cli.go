package telegram

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

const herdrPassiveReadLines = 120

func readHerdrAgentScreen(ctx context.Context) (string, error) {
	return readHerdrAgentScreenLines(ctx, 80)
}

func readHerdrAgentScreenForWatch(ctx context.Context) (string, error) {
	return readHerdrAgentScreenLines(ctx, herdrPassiveReadLines)
}

func readHerdrAgentScreenLines(ctx context.Context, lines int) (string, error) {
	if runtime.GOOS == "windows" {
		// Herdr CLI is an external tool on the operator workstation.
	}
	target, err := resolveHerdrIOTarget(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	sources := []string{"detection", "recent-unwrapped", "visible"}
	var best string
	var lastErr error
	for _, src := range sources {
		text, err := readHerdrScreenSource(ctx, target, src, lines)
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
	return "", fmt.Errorf("herdr read: empty output")
}

func sendHerdrAgentKeys(ctx context.Context, keys ...string) error {
	target, err := resolveHerdrIOTarget(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return sendHerdrKeys(ctx, target, keys...)
}