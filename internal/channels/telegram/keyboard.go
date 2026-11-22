package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/db"
)

// The dock is the reply keyboard the chat keeps on screen: one tap per action
// instead of a command someone has to remember. The labels are the second
// design (L0b); the first-design spellings below are accepted as aliases so a
// keyboard already sitting in a chat keeps working after the rename.
const (
	dockChatNew      = "💬 Nueva sesión"
	dockChatModels   = "🧠 Modelos IA"
	dockChatSessions = "🗂️ Sesiones"
	dockStatus       = "📡 Estado canal"
	dockCancel       = "🛑 Cancelar turno"
	dockChatEnd      = "🔒 Cerrar sesión"
	dockChatToAgent  = "🤖 Cambiar a Modo Agente"

	dockAgentTask     = "📋 Tarea actual"
	dockAgentDev      = "📊 Estado dev"
	dockAgentFeature  = "🎯 Feature ODD"
	dockAgentSessions = "🗂️ Ver sesiones"
	dockAgentToChat   = "💬 Cambiar a Modo Chat"
)

// Text the dock answers with when a button has nothing to run (L0b). Both name
// the command that does the job, so a button never explains nothing.
const (
	featureGuidance = "Usá /estado <feature> para consultar una feature ODD concreta."
)

// Input field placeholders, one per mode (S1): the field itself says what to
// type here, which is the only place the two modes differ before a key exists.
const (
	dockChatPlaceholder  = "Chateá o elegí una opción del menú..."
	dockAgentPlaceholder = "Escribí una orden para Pi o tocá un botón..."
)

// dockAction is what a dock button does. It is a closed set so the router can be
// checked against the keyboard: a button the dock draws with no action here is
// a dead button.
type dockAction int

const (
	dockActNone dockAction = iota
	dockActNew
	dockActModels
	dockActSessions
	dockActStatus
	dockActCancel
	dockActEnd
	dockActTask
	dockActDev
	dockActFeatures
	dockActToAgent
	dockActToChat
)

// dockActions is the routing table: button label to action.
//
// "Cancelar" is the bare label of the pre-dock keyboard. handleMessage answers it
// before it reaches this table, so a cancel still replies exactly once; it is
// listed here anyway because the label is part of the contract this table pins.
var dockActions = map[string]dockAction{
	dockChatNew:       dockActNew,
	dockChatModels:    dockActModels,
	dockChatSessions:  dockActSessions,
	dockStatus:        dockActStatus,
	dockCancel:        dockActCancel,
	dockChatEnd:       dockActEnd,
	dockChatToAgent:   dockActToAgent,
	dockAgentTask:     dockActTask,
	dockAgentDev:      dockActDev,
	dockAgentFeature:  dockActFeatures,
	dockAgentSessions: dockActSessions,
	dockAgentToChat:   dockActToChat,

	// First-design spellings (S1), still on screen in older chats.
	"💬 Nueva sesion de chat":     dockActNew,
	"🗂️ Historial sesiones":      dockActSessions,
	"🔒 Cerrar y archivar sesion": dockActEnd,
	"🤖 Cambiar a Panel Agente":   dockActToAgent,
	"⚡ Orden a Pi":               dockActTask,
	"📊 Estado desarrollo":        dockActDev,
	"💬 Cambiar a Panel Chat":     dockActToChat,
	"Cancelar":                   dockActCancel,
}

// dockRows is the keyboard layout per mode. One action per row where it deserves
// the width, two where they pair, and the way to the other mode last.
func dockRows(mode db.BotMode) [][]string {
	if mode == db.ModeAgent {
		return [][]string{
			{dockAgentTask},
			{dockAgentDev, dockAgentFeature},
			{dockStatus, dockCancel},
			{dockAgentSessions},
			{dockAgentToChat},
		}
	}
	return [][]string{
		{dockChatNew},
		{dockChatModels, dockChatSessions},
		{dockStatus, dockCancel},
		{dockChatEnd},
		{dockChatToAgent},
	}
}

// dockPlaceholder is the text drawn in the empty input field for a mode.
func dockPlaceholder(mode db.BotMode) string {
	if mode == db.ModeAgent {
		return dockAgentPlaceholder
	}
	return dockChatPlaceholder
}

// dockHint is the line that travels with the keyboard. A reply keyboard only
// lives on the message that carried it, so it is re-sent with a line saying
// which panel the chat is in.
func dockHint(mode db.BotMode) string {
	if mode == db.ModeAgent {
		return "Panel Agente — " + dockAgentPlaceholder
	}
	return "Panel Chat — " + dockChatPlaceholder
}

// dockMarkup builds the reply keyboard for a mode.
//
// The struct is handed to the library, never a marshaled string: the parameter
// is an interface, and a string arrives quoted, which Telegram rejects with
// "can't parse reply keyboard markup JSON object".
func dockMarkup(mode db.BotMode) models.ReplyKeyboardMarkup {
	rows := dockRows(mode)
	keyboard := make([][]models.KeyboardButton, 0, len(rows))
	for _, row := range rows {
		buttons := make([]models.KeyboardButton, 0, len(row))
		for _, label := range row {
			buttons = append(buttons, models.KeyboardButton{Text: label})
		}
		keyboard = append(keyboard, buttons)
	}
	return models.ReplyKeyboardMarkup{
		Keyboard:              keyboard,
		ResizeKeyboard:        true,
		IsPersistent:          true,
		InputFieldPlaceholder: dockPlaceholder(mode),
	}
}

// resolveDock maps a button's text to its action. The match is exact on the
// whole label, emojis included: a label is a contract, and a prefix match would
// capture ordinary messages that happen to start the same way.
func resolveDock(text string) (dockAction, bool) {
	action, ok := dockActions[strings.TrimSpace(text)]
	return action, ok
}

// currentMode reads the mode the chat is in. An unreadable value falls back to
// chat, the same reading ParseBotMode gives a corrupt row: a storage fault must
// not turn the channel into an agent that records tasks.
func (s *Supervisor) currentMode() db.BotMode {
	mode, err := s.repo.TelegramMode()
	if err != nil {
		s.setError(err)
		return db.ModeChat
	}
	return mode
}

// sendDock puts the mode's keyboard back on screen. It is called after every
// press (S3) and on first contact, because the keyboard an operator can see is
// the only entry point that does not need a command to be memorized.
func (s *Supervisor) sendDock(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	mode := s.currentMode()
	if _, err := bot.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        dockHint(mode),
		ReplyMarkup: dockMarkup(mode),
	}); err != nil {
		s.setError(fmt.Errorf("telegram: send dock: %w", err))
	}
}

// handleDock runs the action a button names and re-sends the dock.
//
// Every branch delegates to the command that already implements the action
// (S10): the button is a second entry point, never a second implementation, so
// /status typed and 📡 Estado canal tapped cannot drift apart.
func (s *Supervisor) handleDock(ctx context.Context, bot *tgbot.Bot, chatID int64, action dockAction) {
	switch action {
	case dockActNew:
		s.cmdNew(ctx, bot, chatID)
	case dockActEnd:
		s.cmdEnd(ctx, bot, chatID)
	case dockActCancel:
		s.cmdCancel(ctx, bot, chatID)
	case dockActStatus:
		s.cmdStatus(ctx, bot, chatID)
	case dockActTask:
		// The task panel already reports state and reason; the button just
		// opens it, so the operator can check on the work without sending
		// anything to the development session.
		s.showTaskPanel(ctx, bot, chatID)
	case dockActDev:
		s.cmdDevStatus(ctx, bot, chatID, "")
	case dockActModels:
		s.showModelPanel(ctx, bot, chatID)
	case dockActSessions:
		s.showSessionsPanel(ctx, bot, chatID)
	case dockActFeatures:
		s.showFeaturePanel(ctx, bot, chatID)
	case dockActToAgent:
		s.switchMode(ctx, bot, chatID, db.ModeAgent)
	case dockActToChat:
		s.switchMode(ctx, bot, chatID, db.ModeChat)
	}
	// S3: the keyboard is re-sent after handling, so the dock survives the
	// message the action produced and the next press is still one tap away.
	s.sendDock(ctx, bot, chatID)
}

// switchMode stores the mode and redraws the panel in place for it.
func (s *Supervisor) switchMode(ctx context.Context, bot *tgbot.Bot, chatID int64, mode db.BotMode) {
	if err := s.repo.SetTelegramMode(mode); err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude cambiar de modo.")
		return
	}
	s.updatePanel(ctx, bot, chatID)
}

// showTaskPanel is what 📋 Tarea actual does: it reports the chat's task state
// and reason without submitting anything, so checking on the work never sends
// an order by accident.
func (s *Supervisor) showTaskPanel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	s.send(ctx, bot, chatID, s.taskStatusText(chatID))
	s.sendDock(ctx, bot, chatID)
}
