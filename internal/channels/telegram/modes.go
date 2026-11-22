package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	models "github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/db"
)

// panelState builds the panel state from the database. It is read on every
// render so the panel never reports a state the channel has already left.
func (s *Supervisor) panelState(chatID int64) PanelState {
	state := PanelState{Mode: db.ModeChat}

	mode, err := s.repo.TelegramMode()
	if err != nil {
		s.setError(err)
	} else {
		state.Mode = mode
	}

	if model, err := s.repo.TelegramChannelModel(); err == nil {
		state.ModelOrCombo = model
	}

	if task, err := s.repo.GetTelegramAgentTask(chatID); err == nil {
		state.TaskStatus = TaskStatus(task.Status)
		if state.TaskStatus == TaskPendingApproval {
			state.PendingApproval = task.Instruction
		}
		// A task under supervision keeps its instruction on the panel while it
		// runs, which is what the operator is watching for.
		if state.TaskStatus == TaskRunning {
			state.CurrentInstruction = task.Instruction
		}
		if state.TaskStatus == "" {
			state.TaskStatus = TaskIdle
		}
	}

	if auto, err := s.repo.TelegramAutoApprove(chatID); err == nil {
		state.AutoApprove = auto
	}
	return state
}

// cmdPanel opens the mode panel. The message id is remembered so the next
// render edits this message instead of stacking a second panel in the chat.
func (s *Supervisor) cmdPanel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	text, kb := RenderModePanel(s.panelState(chatID))
	sent, err := bot.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: kb,
	})
	if err != nil {
		s.setError(fmt.Errorf("telegram: send panel: %w", err))
		s.send(ctx, bot, chatID, "No pude abrir el panel.")
		return
	}
	s.rememberPanel(chatID, sent.ID)
}

// updatePanel redraws the panel in place (S4). When no panel message is known
// for the chat — the operator never opened one, or the message was deleted — a
// new one is sent, which is the only honest recovery: editing an id that no
// longer exists would fail silently.
func (s *Supervisor) updatePanel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	text, kb := RenderModePanel(s.panelState(chatID))
	id := s.panelMessage(chatID)
	if id == 0 {
		s.cmdPanel(ctx, bot, chatID)
		return
	}
	_, err := bot.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   id,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: kb,
	})
	switch {
	case err == nil:
	case strings.Contains(err.Error(), "message is not modified"):
		// Redrawing an identical panel is the normal result of a refresh; it is
		// not an error and must not surface as one.
	default:
		s.setError(fmt.Errorf("telegram: edit panel: %w", err))
		s.cmdPanel(ctx, bot, chatID)
	}
}

// handleCallbackQuery routes an inline button press (S4-S7).
//
// S9: authorization runs before any state is read or written, exactly as it does
// for text. A button is not a bypass.
func (s *Supervisor) handleCallbackQuery(ctx context.Context, bot *tgbot.Bot, q *models.CallbackQuery) {
	if q == nil || q.From.ID == 0 {
		return
	}
	chatID := q.Message.Message.Chat.ID
	if !s.Allowed(q.From.ID) {
		return
	}

	// The query is answered in every path: an unanswered one leaves the button
	// spinning for the operator, which reads as a hang.
	answer := func(note string) {
		if _, err := bot.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: q.ID,
			Text:            note,
		}); err != nil {
			s.setError(fmt.Errorf("telegram: answer callback: %w", err))
		}
	}

	switch q.Data {
	case cbModeAgent:
		if err := s.repo.SetTelegramMode(db.ModeAgent); err != nil {
			s.setError(err)
			answer("No pude cambiar de modo.")
			return
		}
		answer("Modo Agente")
		s.updatePanel(ctx, bot, chatID)

	case cbModeChat:
		if err := s.repo.SetTelegramMode(db.ModeChat); err != nil {
			s.setError(err)
			answer("No pude cambiar de modo.")
			return
		}
		answer("Modo Chat")
		s.updatePanel(ctx, bot, chatID)

	case cbChatModel:
		answer("Usá /model <nombre> para elegir modelo.")
		s.send(ctx, bot, chatID, s.modelChoicesText())

	case cbChatReset:
		answer("Hilo nuevo")
		s.cmdNew(ctx, bot, chatID)

	case cbRefresh:
		answer("Actualizado")
		s.updatePanel(ctx, bot, chatID)

	case cbApprove:
		answer("Aprobada")
		s.resolveTask(ctx, bot, chatID, TaskRunning, "Tarea aprobada, ejecutando.")

	case cbReject:
		answer("Rechazada")
		s.resolveTask(ctx, bot, chatID, TaskRejected, "Tarea rechazada, no se ejecuta.")

	case cbCancelTask:
		answer("Cancelada")
		s.resolveTask(ctx, bot, chatID, TaskCancelled, "Tarea cancelada.")

	case cbToggleAuto:
		auto, err := s.repo.TelegramAutoApprove(chatID)
		if err != nil {
			s.setError(err)
			answer("No pude leer el ajuste.")
			return
		}
		if err := s.repo.SetTelegramAutoApprove(chatID, !auto); err != nil {
			s.setError(err)
			answer("No pude guardar el ajuste.")
			return
		}
		answer("Auto actualizado")
		s.updatePanel(ctx, bot, chatID)

	case cbTaskStatus:
		answer("Estado")
		s.send(ctx, bot, chatID, s.taskStatusText(chatID))

	case cbAutonomy:
		answer("Autonomía")
		s.send(ctx, bot, chatID, s.autonomyText(chatID))

	default:
		answer("Botón desconocido.")
	}
}

// resolveTask moves the chat's task to a terminal or running state and redraws
// the panel. A transition the current state does not allow is refused instead
// of applied, so the panel can never claim a task is running when it is not.
func (s *Supervisor) resolveTask(ctx context.Context, bot *tgbot.Bot, chatID int64, target TaskStatus, note string) {
	task, err := s.repo.GetTelegramAgentTask(chatID)
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude leer la tarea.")
		return
	}

	current := TaskStatus(task.Status)
	if current == "" {
		current = TaskIdle
	}
	if !taskTransitionAllowed(current, target) {
		s.send(ctx, bot, chatID, fmt.Sprintf(
			"No puedo hacer eso: la tarea está en %s.", taskStatusLabel(current)))
		s.updatePanel(ctx, bot, chatID)
		return
	}

	if err := s.repo.SetTelegramAgentTask(chatID, task.Instruction, string(target)); err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude actualizar la tarea.")
		return
	}
	s.send(ctx, bot, chatID, note)
	s.updatePanel(ctx, bot, chatID)
}

// taskTransitionAllowed is the state machine for the panel controls.
func taskTransitionAllowed(from, to TaskStatus) bool {
	switch to {
	case TaskRunning:
		return from == TaskPendingApproval
	case TaskRejected:
		// Only a waiting task can be rejected. A task already running is
		// cancelled, not rejected: the operator approved it, they do not
		// un-approve it.
		return from == TaskPendingApproval
	case TaskCancelled:
		return from == TaskPendingApproval || from == TaskRunning
	default:
		return false
	}
}

// panelMessages remembers the panel message id per chat. It is bounded by the
// number of chats the bot has spoken to, which the allowlist already bounds.
func (s *Supervisor) rememberPanel(chatID int64, messageID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.panels == nil {
		s.panels = make(map[int64]int)
	}
	s.panels[chatID] = messageID
}

func (s *Supervisor) panelMessage(chatID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panels[chatID]
}

// modelChoicesText explains how to change the model. It names the command
// instead of drawing a model list: the catalogue is large and provider-specific,
// and a stale inline list would offer models the deployment cannot serve.
func (s *Supervisor) modelChoicesText() string {
	current, err := s.repo.TelegramChannelModel()
	if err != nil {
		s.setError(err)
	}
	if current == "" {
		current = "sin elegir"
	}
	return strings.Join([]string{
		"Modelo actual: " + current,
		"Para cambiarlo: /model <proveedor/modelo>",
	}, "\n")
}

// taskStatusText reports the task state in detail, for the "Estado / Paso"
// button. It reads the task rather than the cached panel state so the answer
// cannot lag behind it.
func (s *Supervisor) taskStatusText(chatID int64) string {
	task, err := s.repo.GetTelegramAgentTask(chatID)
	if err != nil {
		s.setError(err)
		return "No pude leer la tarea."
	}
	status := TaskStatus(task.Status)
	if status == "" {
		status = TaskIdle
	}

	lines := []string{"Estado: " + taskStatusLabel(status)}
	if task.Instruction != "" {
		lines = append(lines, "Instrucción: "+task.Instruction)
	}
	if task.UpdatedAt != "" {
		lines = append(lines, "Actualizada: "+task.UpdatedAt)
	}
	return strings.Join(lines, "\n")
}

// autonomyText explains what auto-approve does, so the toggle is not a bare
// switch the operator has to guess the meaning of.
func (s *Supervisor) autonomyText(chatID int64) string {
	auto, err := s.repo.TelegramAutoApprove(chatID)
	if err != nil {
		s.setError(err)
		return "No pude leer el ajuste de autonomía."
	}

	state := "OFF — cada tarea espera tu aprobación."
	if auto {
		state = "ON — las tareas arrancan sin esperar aprobación."
	}
	return "Autonomía: " + state + "\nTocá el botón Auto para cambiarla."
}
