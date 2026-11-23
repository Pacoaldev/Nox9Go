package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/notify"
)

// reconcileTaskFromDevEvent moves a running Telegram task to a terminal state
// when the development session reports the matching order outcome through the
// feed spool.
func (s *Supervisor) reconcileTaskFromDevEvent(ctx context.Context, bot *tgbot.Bot, chatID int64, event notify.Event) {
	task, err := s.repo.GetTelegramAgentTask(chatID)
	if err != nil {
		s.setError(err)
		return
	}
	current, _ := parseTaskStatus(task.Status)
	if current != TaskRunning {
		return
	}
	if !devEventMatchesTask(event, task) {
		return
	}

	if devEventFailsOrder(event) {
		if !taskTransitionAllowed(current, TaskFailed) {
			return
		}
		reason := strings.TrimSpace(event.Detail)
		if reason == "" {
			reason = strings.TrimSpace(event.Title)
		}
		if err := s.repo.SetTelegramAgentTask(chatID, task.Instruction, encodeTaskStatus(TaskFailed, reason), ""); err != nil {
			s.setError(err)
			return
		}
		s.send(ctx, bot, chatID, fmt.Sprintf("Tarea [%s] fallida: %s", taskCompletionTag(event, task), reason))
		s.updatePanel(ctx, bot, chatID)
		return
	}
	if !devEventCompletesOrder(event) {
		return
	}
	if !taskTransitionAllowed(current, TaskDone) {
		return
	}
	if err := s.repo.SetTelegramAgentTask(chatID, task.Instruction, string(TaskDone), ""); err != nil {
		s.setError(err)
		return
	}
	s.send(ctx, bot, chatID, fmt.Sprintf("Tarea [%s] completada.", taskCompletionTag(event, task)))
	s.updatePanel(ctx, bot, chatID)
}

func devEventCompletesOrder(event notify.Event) bool {
	switch event.Kind {
	case notify.KindOrderDone:
		return true
	case notify.KindTask:
		t := strings.ToLower(strings.TrimSpace(event.Title))
		return strings.Contains(t, "complet") || t == "done" || strings.Contains(t, "terminad")
	case notify.KindInfo:
		t := strings.ToLower(strings.TrimSpace(event.Title))
		return t == "tarea completada" || strings.Contains(t, "orden completada")
	default:
		return false
	}
}

func devEventFailsOrder(event notify.Event) bool {
	if event.Kind == notify.KindOrderFailed {
		return true
	}
	if event.Kind == notify.KindTask {
		t := strings.ToLower(strings.TrimSpace(event.Title))
		return strings.Contains(t, "fallid") || strings.Contains(t, "failed")
	}
	return false
}

func devEventMatchesTask(event notify.Event, task *db.TelegramAgentTask) bool {
	if tid := strings.TrimSpace(event.TaskID); tid != "" && task.CorrelationID != "" {
		return tid == task.CorrelationID
	}
	return devEventMatchesInstruction(event, task.Instruction)
}

// devEventMatchesInstruction ties a feed event to the running task. An empty
// detail applies to whatever task is running in the chat.
func devEventMatchesInstruction(event notify.Event, instruction string) bool {
	detail := strings.TrimSpace(event.Detail)
	if detail == "" {
		return true
	}
	instruction = strings.TrimSpace(instruction)
	if detail == instruction {
		return true
	}
	firstLine := strings.SplitN(instruction, "\n", 2)[0]
	if detail == firstLine {
		return true
	}
	return strings.HasPrefix(instruction, detail) || strings.HasPrefix(detail, firstLine)
}

func taskCompletionTag(event notify.Event, task *db.TelegramAgentTask) string {
	if task.CorrelationID != "" {
		return task.CorrelationID
	}
	if id := strings.TrimSpace(event.TaskID); id != "" {
		return id
	}
	if id := strings.TrimSpace(event.ID); len(id) >= 8 {
		return id[:8]
	}
	return "----"
}