package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type herdrAgentStartPayload struct {
	Result struct {
		Agent struct {
			Name string `json:"name"`
		} `json:"agent"`
	} `json:"result"`
}

func agentNameForPaneFromAgents(raw, paneID string) string {
	var wrap struct {
		Result struct {
			Agents []map[string]any `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		return ""
	}
	for _, a := range wrap.Result.Agents {
		pid, _ := a["pane_id"].(string)
		if !strings.EqualFold(strings.TrimSpace(pid), paneID) {
			continue
		}
		name, _ := a["name"].(string)
		return strings.TrimSpace(name)
	}
	return ""
}

func herdrBootstrapDisabled() bool {
	return strings.TrimSpace(os.Getenv("HERDR_BOOTSTRAP_AGENT")) == "0"
}

func herdrBootstrapAgentName() string {
	if n := strings.TrimSpace(os.Getenv("HERDR_BOOTSTRAP_AGENT_NAME")); n != "" {
		return n
	}
	return "shell"
}

func herdrBootstrapAgentKind() string {
	if k := strings.TrimSpace(os.Getenv("HERDR_AGENT_KIND")); k != "" {
		return k
	}
	return "pi"
}

// ensureHerdrAgentOnPane registers an existing Pi/gentle-shell pane when agent list is empty.
// pane-only reads often see only the outer shell prompt; agent read needs a list row.
func ensureHerdrAgentOnPane(ctx context.Context, paneID string) (string, error) {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return "", fmt.Errorf("herdr bootstrap: empty pane id")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	listOut, err := exec.CommandContext(ctx, "herdr", "agent", "list").CombinedOutput()
	listText := strings.TrimSpace(string(listOut))
	if err != nil && listText == "" {
		return "", fmt.Errorf("herdr agent list: %w", err)
	}
	if name := agentNameForPaneFromAgents(listText, paneID); name != "" {
		return name, nil
	}
	if herdrBootstrapDisabled() {
		return "", nil
	}

	name := herdrBootstrapAgentName()
	kind := herdrBootstrapAgentKind()
	timeoutMs := 30000
	if v := strings.TrimSpace(os.Getenv("HERDR_BOOTSTRAP_TIMEOUT_MS")); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			timeoutMs = n
		}
	}

	cmd := exec.CommandContext(ctx, "herdr", "agent", "start", name,
		"--kind", kind, "--pane", paneID, "--timeout", strconv.Itoa(timeoutMs))
	out, startErr := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if startErr != nil && text == "" {
		return "", fmt.Errorf("herdr agent start: %w", startErr)
	}
	var started herdrAgentStartPayload
	if err := json.Unmarshal([]byte(text), &started); err == nil {
		if n := strings.TrimSpace(started.Result.Agent.Name); n != "" {
			return n, nil
		}
	}
	if strings.Contains(text, `"type":"agent_started"`) || strings.Contains(text, "agent_started") {
		return name, nil
	}
	if strings.Contains(text, "agent_name_taken") {
		listOut2, _ := exec.CommandContext(ctx, "herdr", "agent", "list").CombinedOutput()
		if n := agentNameForPaneFromAgents(strings.TrimSpace(string(listOut2)), paneID); n != "" {
			return n, nil
		}
		return name, nil
	}
	return "", fmt.Errorf("herdr agent start: %s", firstLine(text))
}

func herdrAgentStatusForTarget(ctx context.Context, target herdrIOTarget) string {
	if target.isPane() || strings.TrimSpace(target.agent) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "herdr", "agent", "get", target.agent).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil || text == "" {
		return ""
	}
	var payload struct {
		Result struct {
			Agent struct {
				AgentStatus string `json:"agent_status"`
			} `json:"agent"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(text), &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Result.Agent.AgentStatus)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}