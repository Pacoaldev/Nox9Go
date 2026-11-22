package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"nox9go/proxy/internal/db"
)

// send delivers one Telegram message, splitting anything over the platform cap.
func (s *Supervisor) send(ctx context.Context, bot *tgbot.Bot, chatID int64, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, part := range splitMessage(text, maxTelegramMessage) {
		_, err := bot.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   part,
		})
		if err != nil {
			s.setError(fmt.Errorf("telegram: send message: %w", err))
			return
		}
	}
}

// replyToChat resolves the chat a message came from.
func replyToChat(msg *models.Message) int64 { return msg.Chat.ID }

// handleMessage is the authorized path. Commands are answered locally and never
// reach a provider; free text is forwarded through the active session.
func (s *Supervisor) handleMessage(ctx context.Context, bot *tgbot.Bot, msg *models.Message) {
	chatID := replyToChat(msg)

	if cmd, args, isCommand := parseCommand(msg.Text); isCommand {
		s.handleCommand(ctx, bot, chatID, cmd, args)
		return
	}

	s.handleChat(ctx, bot, msg, chatID)
}

// handleChat forwards free text through the chat's active session.
//
// S6: a chat with no active session is idle. The text is answered with a hint
// instead of being forwarded, so stray input never spends a token.
func (s *Supervisor) handleChat(ctx context.Context, bot *tgbot.Bot, msg *models.Message, chatID int64) {
	session, err := s.repo.ActiveTelegramSession(chatID)
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude leer la sesión. Revisá el dashboard.")
		return
	}
	if session == nil {
		s.send(ctx, bot, chatID, "No hay sesión activa. Mandá /new para empezar a chatear.")
		return
	}

	model, err := s.repo.TelegramChannelModel()
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude leer el modelo configurado.")
		return
	}
	if model == "" {
		s.send(ctx, bot, chatID, "No hay modelo configurado. Elegilo con /model <id>.")
		return
	}

	if err := s.repo.AppendTelegramMessage(session.ID, "user", msg.Text); err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude guardar el mensaje.")
		return
	}

	transcript, err := s.repo.TelegramTranscript(session.ID, 40)
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude leer el historial.")
		return
	}
	messages := make([]chatMessage, 0, len(transcript))
	for _, m := range transcript {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}

	baseURL, _ := s.gateway()
	apiKey, err := s.repo.EnsureTelegramAPIKey()
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude emitir la clave del canal.")
		return
	}
	reply, err := forwardText(ctx, baseURL, apiKey, model, messages)
	if err != nil {
		// The failure is reported to the user because a silent turn looks like
		// the bot is broken, and the cause is usually a model or provider choice
		// the operator can act on.
		s.send(ctx, bot, chatID, "Error del gateway: "+err.Error())
		return
	}

	if err := s.repo.AppendTelegramMessage(session.ID, "assistant", reply); err != nil {
		s.setError(err)
	}

	s.send(ctx, bot, chatID, reply)
}

// handleCommand routes a bot command. None of these call a provider.
func (s *Supervisor) handleCommand(ctx context.Context, bot *tgbot.Bot, chatID int64, cmd, args string) {
	switch cmd {
	case "start", "help":
		s.send(ctx, bot, chatID, helpText())
	case "new":
		s.cmdNew(ctx, bot, chatID)
	case "end", "stop":
		s.cmdEnd(ctx, bot, chatID)
	case "sessions":
		s.cmdSessions(ctx, bot, chatID)
	case "status":
		s.cmdStatus(ctx, bot, chatID)
	case "model":
		s.cmdModel(ctx, bot, chatID, args)
	default:
		s.send(ctx, bot, chatID, "Comando desconocido. /help para ver la lista.")
	}
}

func (s *Supervisor) cmdNew(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	session, err := s.repo.StartTelegramSession(chatID, "chat")
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude abrir la sesión.")
		return
	}
	s.send(ctx, bot, chatID,
		fmt.Sprintf("Sesión %d abierta. Escribí lo que quieras. /end para cerrarla.", session.ID))
}

func (s *Supervisor) cmdEnd(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	session, err := s.repo.ActiveTelegramSession(chatID)
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude leer la sesión.")
		return
	}
	if session == nil {
		s.send(ctx, bot, chatID, "No había sesión activa.")
		return
	}
	if err := s.repo.ArchiveTelegramSession(session.ID); err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude cerrar la sesión.")
		return
	}
	s.send(ctx, bot, chatID, fmt.Sprintf("Sesión %d cerrada y archivada.", session.ID))
}

func (s *Supervisor) cmdSessions(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	sessions, err := s.repo.ListTelegramSessions(chatID, 10)
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude listar las sesiones.")
		return
	}
	if len(sessions) == 0 {
		s.send(ctx, bot, chatID, "No hay sesiones todavía. /new para empezar.")
		return
	}

	var b strings.Builder
	b.WriteString("Últimas sesiones:\n")
	for _, session := range sessions {
		state := "archivada"
		if session.ArchivedAt == "" {
			state = "activa"
		}
		fmt.Fprintf(&b, "· %d — %s — %s\n", session.ID, state, session.CreatedAt)
	}
	s.send(ctx, bot, chatID, b.String())
}

func (s *Supervisor) cmdStatus(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	st := s.Status()
	model, _ := s.repo.TelegramChannelModel()
	session, _ := s.repo.ActiveTelegramSession(chatID)

	var b strings.Builder
	fmt.Fprintf(&b, "Estado: %s\n", st.State)
	if st.BotUsername != "" {
		fmt.Fprintf(&b, "Bot: @%s\n", st.BotUsername)
	}
	if model == "" {
		b.WriteString("Modelo: sin configurar (/model <id>)\n")
	} else {
		fmt.Fprintf(&b, "Modelo: %s\n", model)
	}
	if session == nil {
		b.WriteString("Sesión: ninguna (/new)\n")
	} else {
		fmt.Fprintf(&b, "Sesión: %d\n", session.ID)
	}
	if st.LastError != "" {
		fmt.Fprintf(&b, "Último error: %s\n", st.LastError)
	}
	s.send(ctx, bot, chatID, b.String())
}

// cmdModel shows or sets the model used for every turn.
//
// With no argument it lists the models the gateway currently offers, read from
// its own /v1/models route, so the operator picks from what this deployment can
// actually dispatch instead of from a hardcoded list that goes stale.
func (s *Supervisor) cmdModel(ctx context.Context, bot *tgbot.Bot, chatID int64, args string) {
	if args == "" {
		current, _ := s.repo.TelegramChannelModel()
		if current != "" {
			s.send(ctx, bot, chatID, "Modelo actual: "+current)
		}
		baseURL, _ := s.gateway()
		apiKey, err := s.repo.EnsureTelegramAPIKey()
		if err != nil {
			s.setError(err)
			s.send(ctx, bot, chatID, "No pude emitir la clave del canal.")
			return
		}
		ids, err := fetchModelIDs(ctx, baseURL, apiKey)
		if err != nil || len(ids) == 0 {
			s.send(ctx, bot, chatID, "No pude listar los modelos. Usá /model <id> con un id que conozcas.")
			return
		}
		// Bounded list: a deployment with hundreds of models must not produce a
		// wall of text the operator has to scroll through on a phone.
		const shown = 30
		var b strings.Builder
		b.WriteString("Modelos disponibles (usá /model <id>):\n")
		for i, id := range ids {
			if i >= shown {
				fmt.Fprintf(&b, "… y %d más\n", len(ids)-shown)
				break
			}
			fmt.Fprintf(&b, "· %s\n", id)
		}
		s.send(ctx, bot, chatID, b.String())
		return
	}

	if err := s.repo.SetTelegramChannelModel(args); err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude guardar el modelo.")
		return
	}
	s.send(ctx, bot, chatID, "Modelo cambiado a: "+args)
}

func helpText() string {
	return strings.Join([]string{
		"Comandos:",
		"/new — abrir una sesión",
		"/end — cerrar y archivar la sesión",
		"/sessions — ver las últimas sesiones",
		"/model — ver el modelo actual y los disponibles",
		"/model <id> — cambiar el modelo",
		"/status — estado del canal",
		"/help — esta ayuda",
	}, "\n")
}

// ensure db stays referenced by the interface docs above.
var _ = db.TelegramSession{}
