package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"nox9go/proxy/internal/config"
)

type herdrIOTarget struct {
	agent string
	pane  string
}

func (t herdrIOTarget) isPane() bool { return strings.TrimSpace(t.pane) != "" }

type herdrPaneListPayload struct {
	Result struct {
		Panes []herdrPaneRow `json:"panes"`
	} `json:"result"`
}

type herdrPaneRow struct {
	PaneID  string `json:"pane_id"`
	Focused bool   `json:"focused"`
	Cwd     string `json:"cwd"`
}

// resolveHerdrIOTarget picks an agent when Herdr has one; otherwise the focused pane
// (gentle-shell can run without agent list rows after a restart).
func resolveHerdrIOTarget(ctx context.Context) (herdrIOTarget, error) {
	name, err := pickHerdrAgent(ctx, false)
	if err == nil {
		return herdrIOTarget{agent: name}, nil
	}
	pane, perr := pickFocusedHerdrPane(ctx)
	if perr != nil {
		return herdrIOTarget{}, err
	}
	if name, berr := ensureHerdrAgentOnPane(ctx, pane); berr == nil && strings.TrimSpace(name) != "" {
		return herdrIOTarget{agent: name}, nil
	}
	return herdrIOTarget{pane: pane}, nil
}

func pickFocusedHerdrPane(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "herdr", "pane", "list")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return "", fmt.Errorf("herdr pane list: %w", err)
	}
	return pickFocusedHerdrPaneFromJSON(text, config.HerdrAgentExcludeCwd())
}

func pickFocusedHerdrPaneFromJSON(raw, excludeCwd string) (string, error) {
	var payload herdrPaneListPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("herdr pane list json: %w", err)
	}
	var focused []herdrPaneRow
	for _, p := range payload.Result.Panes {
		if p.Focused && strings.TrimSpace(p.PaneID) != "" {
			focused = append(focused, p)
		}
	}
	if len(focused) == 0 {
		return "", fmt.Errorf("herdr: no focused pane (click your gentle-shell Pi pane)")
	}
	if excludeCwd != "" && len(focused) > 1 {
		focused = filterPanes(focused, func(p herdrPaneRow) bool {
			return !herdrPathsEqual(p.Cwd, excludeCwd)
		})
	}
	switch len(focused) {
	case 0:
		return "", fmt.Errorf("herdr: no focused pane outside excluded cwd")
	case 1:
		return strings.TrimSpace(focused[0].PaneID), nil
	default:
		ids := make([]string, 0, len(focused))
		for _, p := range focused {
			ids = append(ids, p.PaneID)
		}
		return "", fmt.Errorf("herdr: multiple focused panes (%s); focus one Pi pane", strings.Join(ids, ", "))
	}
}

func filterPanes(in []herdrPaneRow, keep func(herdrPaneRow) bool) []herdrPaneRow {
	out := make([]herdrPaneRow, 0, len(in))
	for _, p := range in {
		if keep(p) {
			out = append(out, p)
		}
	}
	return out
}