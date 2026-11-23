package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/notify"
)

const orderRunnerInterval = 5 * time.Second

// drainInboxOrders runs when DEV_ORDER_RUNNER points at an executable script.
// It drains the instruction inbox and runs each order on this machine, then
// appends order_done / order_failed to the development feed.
func (s *Supervisor) drainInboxOrders(ctx context.Context, repo Repo) {
	script := resolveOrderRunnerScript()
	if script == "" {
		return
	}

	ticker := time.NewTicker(orderRunnerInterval)
	defer ticker.Stop()

	var runMu sync.Mutex

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runMu.Lock()
			s.pollInboxOnce(ctx, repo, script)
			runMu.Unlock()
		}
	}
}

func (s *Supervisor) pollInboxOnce(ctx context.Context, repo Repo, script string) {
	inbox, err := repo.DevInbox()
	if err != nil {
		s.setError(err)
		return
	}
	events, err := inbox.Drain()
	if err != nil {
		s.setError(err)
		return
	}
	if len(events) == 0 {
		return
	}

	spool, err := repo.DevSpool()
	if err != nil {
		s.setError(err)
		return
	}

	for _, event := range events {
		if event.Kind != notify.KindOrder {
			continue
		}
		outcome, detail := runOrderScript(ctx, script, event)
		if spool == nil || spool.Path == "" {
			continue
		}
		switch outcome {
		case orderRunDone:
			if err := spool.Append(notify.Event{
				Kind:   notify.KindOrderDone,
				Title:  "Orden completada",
				Detail: strings.TrimSpace(event.Detail),
				TaskID: strings.TrimSpace(event.TaskID),
			}); err != nil {
				s.setError(fmt.Errorf("telegram: order outcome spool: %w", err))
			}
		case orderRunBlocked:
			if detail == "" {
				detail = "El agente Herdr está bloqueado (aprobación o pregunta en el panel)."
			}
			if err := spool.Append(notify.Event{
				Kind:   notify.KindBlocker,
				Title:  "Agente bloqueado",
				Detail: detail,
				TaskID: strings.TrimSpace(event.TaskID),
			}); err != nil {
				s.setError(fmt.Errorf("telegram: blocker spool: %w", err))
			}
		case orderRunFailed:
			if detail == "" {
				detail = "La orden no terminó en Herdr."
			}
			if err := spool.Append(notify.Event{
				Kind:   notify.KindOrderFailed,
				Title:  "Orden fallida",
				Detail: detail,
				TaskID: strings.TrimSpace(event.TaskID),
			}); err != nil {
				s.setError(fmt.Errorf("telegram: order outcome spool: %w", err))
			}
		}
	}
}

func resolveOrderRunnerScript() string {
	line := strings.TrimSpace(config.DevOrderRunner())
	if line == "" {
		return ""
	}
	if filepath.IsAbs(line) {
		return line
	}
	if root := strings.TrimSpace(config.DevRepoRoot()); root != "" {
		candidate := filepath.Join(root, filepath.FromSlash(line))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return line
}

type orderRunOutcome int

const (
	orderRunDone orderRunOutcome = iota
	orderRunBlocked
	orderRunFailed
)

func runOrderScript(ctx context.Context, script string, event notify.Event) (orderRunOutcome, string) {
	workDir := strings.TrimSpace(config.DevRepoRoot())
	if workDir == "" {
		workDir = "."
	}
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"NOX9GO_ORDER_ID="+strings.TrimSpace(event.ID),
		"NOX9GO_ORDER_DETAIL="+strings.TrimSpace(event.Detail),
		"NOX9GO_TASK_ID="+strings.TrimSpace(event.TaskID),
		"DEV_REPO_ROOT="+workDir,
		"HERDR_AGENT_TARGET="+strings.TrimSpace(config.HerdrAgentTarget()),
	)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return orderRunBlocked, firstNonEmpty(text, err.Error())
		}
		detail := text
		if detail == "" {
			detail = err.Error()
		}
		if strings.TrimSpace(event.Detail) != "" {
			detail = strings.TrimSpace(event.Detail) + "\n" + detail
		}
		return orderRunFailed, detail
	}
	return orderRunDone, text
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
}