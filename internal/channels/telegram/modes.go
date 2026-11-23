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
		status, reason := parseTaskStatus(task.Status)
		state.TaskStatus = status
		state.CorrelationID = task.CorrelationID
		switch status {
		case TaskPendingApproval:
			state.PendingApproval = task.Instruction
		case TaskRunning, TaskDone:
			// A task under supervision keeps its instruction on the panel while it
			// runs or after it completes, which is what the operator is watching for.
			state.CurrentInstruction = task.Instruction
		case TaskFailed:
			state.CurrentInstruction = task.Instruction
			state.FailureReason = reason
		}
	}

	if auto, err := s.repo.TelegramAutoApprove(chatID); err == nil {
		state.AutoApprove = auto
	}
	if hidden, err := s.repo.TelegramDockHidden(chatID); err == nil {
		state.DockHidden = hidden
	}
	if scope, err := s.repo.TelegramFeedScope(chatID); err == nil {
		state.FeedScope = scope
	}
	if compact, err := s.repo.TelegramAgentCompactDock(chatID); err == nil {
		state.AgentCompactDock = compact
	}
	state.LastDevEvent = s.lastDevFeedLine(chatID)
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
	s.pinPanelMessage(ctx, bot, chatID, sent.ID)
}

// openPanel re-opens the panel as a new message at the bottom of the thread and
// removes the previous one, so the hub's "Menú" button always produces a change
// the operator can see. Redrawing an unchanged panel in place is silent:
// Telegram answers "message is not modified", which reads as a dead button.
func (s *Supervisor) openPanel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	previous := s.panelMessage(chatID)
	s.cmdPanel(ctx, bot, chatID)
	// cmdPanel records the new id; deleting the previous one only after it
	// exists means a failed send leaves the old panel on screen instead of
	// erasing the only copy.
	if previous == 0 || s.panelMessage(chatID) == previous {
		return
	}
	if _, err := bot.DeleteMessage(ctx, &tgbot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: previous,
	}); err != nil {
		// A panel the operator already deleted, or one too old for the bot to
		// remove, is not a fault worth a chat message: the new panel is on
		// screen either way.
		if !strings.Contains(err.Error(), "message to delete not found") &&
			!strings.Contains(err.Error(), "message can't be deleted") {
			s.setError(fmt.Errorf("telegram: delete previous panel: %w", err))
		}
	}
}

func (s *Supervisor) pinPanelMessage(ctx context.Context, bot *tgbot.Bot, chatID int64, messageID int) {
	if messageID == 0 {
		return
	}
	_, err := bot.PinChatMessage(ctx, &tgbot.PinChatMessageParams{
		ChatID:              chatID,
		MessageID:           messageID,
		DisableNotification: true,
	})
	if err == nil {
		return
	}
	// Private chats without pin permission still work; only report unexpected faults.
	if strings.Contains(err.Error(), "not enough rights") ||
		strings.Contains(err.Error(), "CHAT_ADMIN_REQUIRED") ||
		strings.Contains(err.Error(), "can't pin") {
		return
	}
	s.setError(fmt.Errorf("telegram: pin panel: %w", err))
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

	s.dispatchCallback(ctx, bot, chatID, q)

	// The keyboard Telegram shows persists between messages; re-sending it on
	// every press only spams the thread. A refresh travels when the dock is
	// actually new to the chat (never drawn since the process started, or the
	// operator removed it) or when the press itself changed the keyboard —
	// switchMode, the dock router and the hide/show action already re-send in
	// those cases, so the catch-all here only fills the gap.
	s.sendDockIfNeeded(ctx, bot, chatID, dockActNone)
}

// dispatchCallback runs one callback's effect.
//
// It is separate from the gate so the dock re-set above happens on every path,
// including the ones that end in an error answer: a forgotten keyboard is a
// worse failure than a refused action.
func (s *Supervisor) dispatchCallback(ctx context.Context, bot *tgbot.Bot, chatID int64, q *models.CallbackQuery) {
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

	// The pick callbacks carry an argument, so they are matched by prefix before
	// the exact values below; an exact value can never be mistaken for a prefix
	// of itself because the argument is what the prefix does not cover.
	switch {
	case strings.HasPrefix(q.Data, cbModelPick):
		s.pickModel(ctx, bot, chatID, strings.TrimPrefix(q.Data, cbModelPick), answer)
		return
	case strings.HasPrefix(q.Data, cbSessionPick):
		s.retakeSession(ctx, bot, chatID, strings.TrimPrefix(q.Data, cbSessionPick), answer)
		return
	case strings.HasPrefix(q.Data, cbFeaturePick):
		s.renderFeature(ctx, bot, chatID, strings.TrimPrefix(q.Data, cbFeaturePick), answer)
		return
	}

	switch q.Data {
	case cbModeAgent:
		answer("Modo Agente")
		s.switchMode(ctx, bot, chatID, db.ModeAgent)

	case cbModeChat:
		answer("Modo Chat")
		s.switchMode(ctx, bot, chatID, db.ModeChat)

	case cbChatModel, cbModelsPanel:
		// Both entry points open the same subpanel: the panel's own button and
		// the dock's, so there is one model list and one place to keep it right.
		answer("Modelos")
		s.showModelPanel(ctx, bot, chatID)

	case cbSessionsPanel:
		answer("Sesiones")
		s.showSessionsPanel(ctx, bot, chatID)

	case cbFeaturesPanel:
		answer("Features ODD")
		s.showFeaturePanel(ctx, bot, chatID)

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

	case cbDockHide:
		answer("Teclado oculto")
		s.setDockHidden(ctx, bot, chatID, true)

	case cbDockShow:
		answer("Teclado visible")
		s.setDockHidden(ctx, bot, chatID, false)

	case cbFeedCycle:
		scope, err := s.repo.TelegramFeedScope(chatID)
		if err != nil {
			s.setError(err)
			answer("No pude leer el feed.")
			return
		}
		next := int(feedScope(scope).next())
		if err := s.repo.SetTelegramFeedScope(chatID, next); err != nil {
			s.setError(err)
			answer("No pude guardar el feed.")
			return
		}
		answer(feedScopeLabel(feedScope(next)))
		s.updatePanel(ctx, bot, chatID)

	case cbAgentCompact:
		on, err := s.repo.TelegramAgentCompactDock(chatID)
		if err != nil {
			s.setError(err)
			answer("No pude leer compacto.")
			return
		}
		if err := s.repo.SetTelegramAgentCompactDock(chatID, !on); err != nil {
			s.setError(err)
			answer("No pude guardar compacto.")
			return
		}
		answer("Compacto actualizado")
		s.updatePanel(ctx, bot, chatID)

	case cbChatStatus:
		answer("Estado")
		s.cmdStatus(ctx, bot, chatID)

	case cbChatEnd:
		answer("Cerrar")
		s.cmdEnd(ctx, bot, chatID)

	case cbDevPanel:
		answer("Dev")
		s.cmdDevStatus(ctx, bot, chatID, "")

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

	current, _ := parseTaskStatus(task.Status)
	if !taskTransitionAllowed(current, target) {
		s.send(ctx, bot, chatID, fmt.Sprintf(
			"No puedo hacer eso: la tarea está en %s.", taskStatusLabel(current)))
		s.updatePanel(ctx, bot, chatID)
		return
	}

	// S1/S3: approving is what hands the instruction to the development session,
	// and it is the only thing that does. The status is written after the spool
	// accepted the order, so a submit that failed can never leave the panel
	// claiming progress that is not happening.
	if target == TaskRunning {
		if err := s.submitOrder(chatID, task.Instruction, task.CorrelationID); err != nil {
			s.recordFailedTask(ctx, bot, chatID, task.Instruction, task.CorrelationID, err)
			return
		}
	}

	// S4: a cancelled task may already be queued in the session, so the caveat
	// travels with the cancellation instead of leaving the operator believing
	// the instruction was recalled.
	if target == TaskCancelled && current == TaskRunning {
		note = cancelRunningNote
	}

	if err := s.repo.SetTelegramAgentTask(chatID, task.Instruction, string(target), ""); err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude actualizar la tarea.")
		return
	}
	s.send(ctx, bot, chatID, note)
	s.updatePanel(ctx, bot, chatID)
}

// cancelRunningNote is what cancelling an already-submitted task says.
const cancelRunningNote = "Tarea cancelada. La orden puede estar ya en la cola de la sesión de desarrollo."

// taskTransitionAllowed is the state machine for the task's states.
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
	case TaskDone:
		return from == TaskRunning
	case TaskFailed:
		// S3: failure is the recorded outcome of a submit the spool refused, or
		// of a session reporting back. Both are reachable from the states a task
		// can be submitted from, and neither is offered as a button: an operator
		// does not fail a task, it fails on its own.
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

// taskStatusText reports the task state in detail, for the "Estado / Paso"
// button. It reads the task rather than the cached panel state so the answer
// cannot lag behind it, and it names the reason a failed task carries.
func (s *Supervisor) taskStatusText(chatID int64) string {
	task, err := s.repo.GetTelegramAgentTask(chatID)
	if err != nil {
		s.setError(err)
		return "No pude leer la tarea."
	}
	status, reason := parseTaskStatus(task.Status)

	lines := []string{"Estado: " + taskStatusLabel(status)}
	if task.Instruction != "" {
		lines = append(lines, "Instrucción: "+task.Instruction)
	}
	if reason != "" {
		lines = append(lines, "Motivo: "+reason)
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
