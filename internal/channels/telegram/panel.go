package telegram

import (
	"fmt"
	"strings"

	models "github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/db"
)

// TaskStatus is what the agent panel reports. It is a plain string set rather
// than free text so the panel cannot print a state nobody can act on.
type TaskStatus string

const (
	TaskIdle            TaskStatus = "idle"
	TaskRunning         TaskStatus = "running"
	TaskPendingApproval TaskStatus = "pending_approval"
	TaskCancelled       TaskStatus = "cancelled"
	TaskRejected        TaskStatus = "rejected"
	TaskDone            TaskStatus = "done"
)

// PanelState is everything the two panels read. It is built from the database
// and the task table on each render, never cached in the Supervisor, so the
// panel can never show a state the channel has already left.
type PanelState struct {
	Mode            db.BotMode
	ModelOrCombo    string
	TaskStatus      TaskStatus
	PendingApproval string
	LastAction      string
	AutoApprove     bool
}

// Callback data prefix. Every value this package emits starts with it, so a
// callback for another feature can never be mistaken for a panel one.
const (
	cbPrefix     = "m:"
	cbChatModel  = "m:chat:model"
	cbChatReset  = "m:chat:reset"
	cbModeAgent  = "m:mode:agent"
	cbModeChat   = "m:mode:chat"
	cbApprove    = "m:ag:approve"
	cbReject     = "m:ag:reject"
	cbCancelTask = "m:ag:cancel"
	cbRefresh    = "m:ag:refresh"
	cbToggleAuto = "m:ag:toggle_auto"
	cbTaskStatus = "m:ag:status"
	cbAutonomy   = "m:ag:autonomy"
)

// maxCallbackData is Telegram's hard limit. A longer value makes the button
// unusable, so there is a test that every constant above fits.
const maxCallbackData = 64

// inlineButton is a button before it becomes a library model. Keeping the
// package's own type makes the callback-data width checkable in one place.
type inlineButton struct {
	Text string
	Data string
}

func (b inlineButton) model() models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: b.Text, CallbackData: b.Data}
}

// markup converts rows into the library's markup.
//
// The struct is passed to the library, never a pre-marshaled JSON string: the
// parameter is an interface, so a string arrives quoted and the API rejects it
// with "can't parse reply keyboard markup JSON object".
func markup(rows [][]inlineButton) models.ReplyMarkup {
	out := make([][]models.InlineKeyboardButton, 0, len(rows))
	for _, row := range rows {
		converted := make([]models.InlineKeyboardButton, 0, len(row))
		for _, b := range row {
			converted = append(converted, b.model())
		}
		out = append(out, converted)
	}
	return models.InlineKeyboardMarkup{InlineKeyboard: out}
}

// RenderModePanel draws the panel for the current mode.
func RenderModePanel(s PanelState) (string, models.ReplyMarkup) {
	if s.Mode == db.ModeAgent {
		return renderAgentPanel(s)
	}
	return renderChatPanel(s)
}

// renderChatPanel is the conversation panel: what to talk with, and how to
// change that. It carries no task controls, because in chat mode there is no
// task to control.
func renderChatPanel(s PanelState) (string, models.ReplyMarkup) {
	model := s.ModelOrCombo
	if model == "" {
		model = "sin elegir"
	}

	var b strings.Builder
	b.WriteString("<b>NOX9GO · Modo Chat</b>\n\n")
	fmt.Fprintf(&b, "Modelo/Combo: <code>%s</code>\n", esc(model))
	b.WriteString("Los mensajes se envían como chat normal.\n")
	if s.LastAction != "" {
		fmt.Fprintf(&b, "\n%s\n", esc(s.LastAction))
	}

	rows := [][]inlineButton{
		{{Text: "Modelo/Combo", Data: cbChatModel}, {Text: "Nuevo hilo", Data: cbChatReset}},
		{{Text: "Pasar a Agente", Data: cbModeAgent}},
	}
	return strings.TrimRight(b.String(), "\n"), markup(rows)
}

// renderAgentPanel is the supervision panel: task state, and the controls that
// act on it. The controls are derived from the state rather than shown always,
// so a button can never offer an action the state would refuse.
func renderAgentPanel(s PanelState) (string, models.ReplyMarkup) {
	status := s.TaskStatus
	if status == "" {
		status = TaskIdle
	}

	var b strings.Builder
	b.WriteString("<b>NOX9GO · Modo Agente</b>\n\n")
	fmt.Fprintf(&b, "Estado: <b>%s</b>\n", esc(taskStatusLabel(status)))

	switch {
	case status == TaskPendingApproval && s.PendingApproval != "":
		fmt.Fprintf(&b, "Pendiente: <code>%s</code>\n", esc(s.PendingApproval))
	case s.LastAction != "":
		fmt.Fprintf(&b, "%s\n", esc(s.LastAction))
	default:
		b.WriteString("Enviá una instrucción para ejecutar una tarea.\n")
	}

	var rows [][]inlineButton
	switch status {
	case TaskPendingApproval:
		rows = append(rows, []inlineButton{
			{Text: "Aprobar", Data: cbApprove},
			{Text: "Rechazar", Data: cbReject},
		})
	case TaskRunning:
		rows = append(rows, []inlineButton{
			{Text: "Cancelar tarea", Data: cbCancelTask},
			{Text: "Refrescar", Data: cbRefresh},
		})
	default:
		auto := "Auto: OFF"
		if s.AutoApprove {
			auto = "Auto: ON"
		}
		rows = append(rows, []inlineButton{
			{Text: auto, Data: cbToggleAuto},
			{Text: "Refrescar", Data: cbRefresh},
		})
	}
	rows = append(rows,
		[]inlineButton{{Text: "Estado / Paso", Data: cbTaskStatus}, {Text: "Autonomía", Data: cbAutonomy}},
		[]inlineButton{{Text: "Cambiar a Chat", Data: cbModeChat}},
	)
	return strings.TrimRight(b.String(), "\n"), markup(rows)
}

// taskStatusLabel is the operator-facing wording for a state.
func taskStatusLabel(s TaskStatus) string {
	switch s {
	case TaskRunning:
		return "Ejecutando"
	case TaskPendingApproval:
		return "Esperando aprobación"
	case TaskCancelled:
		return "Cancelada"
	case TaskRejected:
		return "Rechazada"
	case TaskDone:
		return "Completada"
	default:
		return "Inactivo"
	}
}

// esc escapes the three characters Telegram's HTML parser treats as markup.
// Every value interpolated into a panel goes through it: a model name or task
// summary containing "<" would otherwise break the message or inject markup.
func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
