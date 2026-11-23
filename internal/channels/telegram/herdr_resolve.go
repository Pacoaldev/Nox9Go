package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// IsHerdrFocusedTarget reports whether the operator wants the active Herdr agent
// (gentle-shell Pi pane with focused:true) instead of a fixed name.
func IsHerdrFocusedTarget(spec string) bool {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "focused", "@focused", "*":
		return true
	default:
		return false
	}
}

// ResolveHerdrAgentName maps HERDR_AGENT_TARGET to a concrete agent name.
// Empty config defaults to focused. Literal names pass through unchanged.
func ResolveHerdrAgentName(ctx context.Context, configured string) (string, error) {
	spec := strings.TrimSpace(configured)
	if spec == "" {
		spec = "focused"
	}
	if !IsHerdrFocusedTarget(spec) {
		return spec, nil
	}
	return pickFocusedHerdrAgent(ctx)
}

type herdrAgentListPayload struct {
	Result struct {
		Agents []struct {
			Name    string `json:"name"`
			Focused bool   `json:"focused"`
		} `json:"agents"`
	} `json:"result"`
}

func pickFocusedHerdrAgentFromJSON(raw string) (string, error) {
	var payload herdrAgentListPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("herdr agent list json: %w", err)
	}
	var focused []string
	for _, a := range payload.Result.Agents {
		if a.Focused && strings.TrimSpace(a.Name) != "" {
			focused = append(focused, strings.TrimSpace(a.Name))
		}
	}
	switch len(focused) {
	case 0:
		return "", fmt.Errorf("herdr: no focused agent (open gentle-shell and focus that Pi pane)")
	case 1:
		return focused[0], nil
	default:
		return "", fmt.Errorf("herdr: multiple focused agents (%s); focus one Pi pane", strings.Join(focused, ", "))
	}
}

func pickFocusedHerdrAgent(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "herdr", "agent", "list")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return pickFocusedHerdrAgentFromJSON(text)
		}
		return "", fmt.Errorf("herdr agent list: %w", err)
	}
	return pickFocusedHerdrAgentFromJSON(text)
}