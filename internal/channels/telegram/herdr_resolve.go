package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nox9go/proxy/internal/config"
)

// IsHerdrFocusedTarget reports whether the operator wants the active Herdr agent
// (gentle-shell Pi pane with focused:true) instead of a fixed name.
func IsHerdrFocusedTarget(spec string) bool {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "focused", "@focused", "*", "workdir", "@workdir":
		return true
	default:
		return false
	}
}

func isHerdrWorkdirTarget(spec string) bool {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "workdir", "@workdir":
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
	return pickHerdrAgent(ctx, isHerdrWorkdirTarget(spec))
}

type herdrAgentListPayload struct {
	Result struct {
		Agents []herdrAgentRow `json:"agents"`
	} `json:"result"`
}

type herdrAgentRow struct {
	Name    string `json:"name"`
	Focused bool   `json:"focused"`
	Cwd     string `json:"cwd"`
}

func herdrPathsEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func pickHerdrAgent(ctx context.Context, workdirOnly bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "herdr", "agent", "list")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return "", fmt.Errorf("herdr agent list: %w", err)
	}
	return pickHerdrAgentFromJSON(text, workdirOnly, config.HerdrAgentWorkCwd(), config.HerdrAgentExcludeCwd())
}

func pickHerdrAgentFromJSON(raw string, workdirOnly bool, preferCwd, excludeCwd string) (string, error) {
	var payload herdrAgentListPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("herdr agent list json: %w", err)
	}
	agents := payload.Result.Agents
	if workdirOnly {
		return pickAgentByWorkCwd(agents, preferCwd, excludeCwd)
	}
	return pickFocusedHerdrAgentRows(agents, preferCwd, excludeCwd)
}

func pickAgentByWorkCwd(agents []herdrAgentRow, preferCwd, excludeCwd string) (string, error) {
	if strings.TrimSpace(preferCwd) == "" {
		return "", fmt.Errorf("herdr workdir: HERDR_AGENT_WORK_CWD is empty")
	}
	var matches []herdrAgentRow
	for _, a := range agents {
		if !herdrPathsEqual(a.Cwd, preferCwd) {
			continue
		}
		if excludeCwd != "" && herdrPathsEqual(a.Cwd, excludeCwd) {
			continue
		}
		if strings.TrimSpace(a.Name) == "" {
			continue
		}
		matches = append(matches, a)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("herdr workdir: no agent with cwd %s", strings.TrimSpace(preferCwd))
	}
	focused := filterAgents(matches, func(a herdrAgentRow) bool { return a.Focused })
	if len(focused) == 1 {
		return strings.TrimSpace(focused[0].Name), nil
	}
	if len(focused) > 1 {
		return disambiguateHerdrAgentNames(focused)
	}
	if len(matches) == 1 {
		return strings.TrimSpace(matches[0].Name), nil
	}
	return disambiguateHerdrAgentNames(matches)
}

func pickFocusedHerdrAgentRows(agents []herdrAgentRow, preferCwd, excludeCwd string) (string, error) {
	focused := filterAgents(agents, func(a herdrAgentRow) bool {
		return a.Focused && strings.TrimSpace(a.Name) != ""
	})
	if len(focused) == 0 {
		return "", fmt.Errorf("herdr: no focused agent (open gentle-shell and focus that Pi pane)")
	}

	if preferCwd != "" {
		var cwdMatch []herdrAgentRow
		for _, a := range focused {
			if herdrPathsEqual(a.Cwd, preferCwd) {
				cwdMatch = append(cwdMatch, a)
			}
		}
		if len(cwdMatch) == 1 {
			return strings.TrimSpace(cwdMatch[0].Name), nil
		}
		if len(cwdMatch) > 1 {
			return disambiguateHerdrAgentNames(cwdMatch)
		}
	}

	if excludeCwd != "" && len(focused) > 1 {
		focused = filterAgents(focused, func(a herdrAgentRow) bool {
			return !herdrPathsEqual(a.Cwd, excludeCwd)
		})
	}

	switch len(focused) {
	case 0:
		return "", fmt.Errorf("herdr: no focused agent outside excluded cwd")
	case 1:
		return strings.TrimSpace(focused[0].Name), nil
	default:
		return disambiguateHerdrAgentNames(focused)
	}
}

// disambiguateHerdrAgentNames prefers gentle-shell's agent name (shell) over the
// legacy Telegram registration (nox) when both appear focused.
func disambiguateHerdrAgentNames(agents []herdrAgentRow) (string, error) {
	if len(agents) == 0 {
		return "", fmt.Errorf("herdr: no agents to choose")
	}
	if len(agents) == 1 {
		return strings.TrimSpace(agents[0].Name), nil
	}
	for _, a := range agents {
		if strings.EqualFold(strings.TrimSpace(a.Name), "shell") {
			return strings.TrimSpace(a.Name), nil
		}
	}
	var nonNox []herdrAgentRow
	for _, a := range agents {
		if !strings.EqualFold(strings.TrimSpace(a.Name), "nox") {
			nonNox = append(nonNox, a)
		}
	}
	if len(nonNox) == 1 {
		return strings.TrimSpace(nonNox[0].Name), nil
	}
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, strings.TrimSpace(a.Name))
	}
	return "", fmt.Errorf("herdr: multiple focused agents (%s); focus one Pi pane", strings.Join(names, ", "))
}

func filterAgents(in []herdrAgentRow, keep func(herdrAgentRow) bool) []herdrAgentRow {
	out := make([]herdrAgentRow, 0, len(in))
	for _, a := range in {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}

// pickFocusedHerdrAgentFromJSON is kept for table tests that pass raw JSON only.
func pickFocusedHerdrAgentFromJSON(raw string) (string, error) {
	return pickHerdrAgentFromJSON(raw, false, "", "")
}

func pickFocusedHerdrAgent(ctx context.Context) (string, error) {
	return pickHerdrAgent(ctx, false)
}