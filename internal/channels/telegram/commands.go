package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/devstatus"
	"nox9go/proxy/internal/notify"
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

// sendHTML delivers one Telegram message parsed as HTML.
//
// It exists beside send rather than replacing it: model replies and gateway
// errors are plain text, and a reply containing "a < b" must not be parsed as
// markup. Only text this package formats itself goes through here, and that
// text escapes every repository value it interpolates.
func (s *Supervisor) sendHTML(ctx context.Context, bot *tgbot.Bot, chatID int64, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, part := range splitMessage(text, maxTelegramMessage) {
		_, err := bot.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:    chatID,
			Text:      part,
			ParseMode: models.ParseModeHTML,
		})
		if err != nil {
			// A rejected markup falls back to plain text instead of leaving the
			// operator with nothing: the report is still readable without
			// formatting, and a formatting bug must never silence it.
			s.send(ctx, bot, chatID, stripHTML(part))
			return
		}
	}
}

// stripHTML removes the tags this package emits, for the plain-text fallback.
func stripHTML(text string) string {
	r := strings.NewReplacer("<b>", "", "</b>", "", "<code>", "", "</code>", "",
		"&lt;", "<", "&gt;", ">", "&amp;", "&")
	return r.Replace(text)
}

// sendKeyboard sends one message carrying the cancel reply keyboard (S7).
// A reply keyboard persists in the chat, which is deliberate: the button stays
// reachable while the operator reads a long reply, and a press when nothing is
// in flight gets the same honest nothing-to-cancel answer.
func (s *Supervisor) sendKeyboard(ctx context.Context, bot *tgbot.Bot, chatID int64, text string) {
	// The library's ReplyMarkup field is an interface; passing the struct lets
	// it marshal into the JSON object Telegram demands. A pre-marshaled string
	// was sent as a quoted JSON string and the API rejected it with "can't
	// parse reply keyboard markup JSON object" — which failed every chat turn,
	// because this ack precedes the forward. The marshal also served as the
	// struct-shape check; the compiler now does that job.
	markup := models.ReplyKeyboardMarkup{
		Keyboard:       [][]models.KeyboardButton{{{Text: "Cancelar"}}},
		ResizeKeyboard: true,
		IsPersistent:   true,
	}
	if _, err := bot.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: markup,
	}); err != nil {
		s.setError(fmt.Errorf("telegram: send keyboard: %w", err))
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

	// The cancel keyboard button arrives as plain text. It is intercepted here
	// rather than forwarded to a model that was never asked for it; when no
	// turn is in flight the operator gets the same honest nothing-to-cancel
	// answer the /cancel command gives.
	if strings.TrimSpace(msg.Text) == "Cancelar" {
		s.cmdCancel(ctx, bot, chatID)
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
	// S7: the turn runs on a derived context the /cancel command can abort.
	// The parent ctx is not replaced: a cancelled poll loop must still stop the
	// turn, and the registration only adds a second way to cancel it.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	s.beginTurn(chatID, cancelTurn)

	// S7: the visible cancel control. The reply keyboard travels with an
	// ack so the operator sees, in the moment the turn starts, what a tap
	// does; the command text is accepted anywhere so a stale keyboard after a
	// restart still cancels honestly ("no hay nada en vuelo").
	s.sendKeyboard(ctx, bot, chatID, "En vuelo — cancelá con el botón o /cancel.")

	reply, turnErr := forwardText(turnCtx, baseURL, apiKey, model, messages)

	// Removed before anything else, so a turn that finished normally is never
	// left cancelable and a late /cancel reports nothing in flight.
	s.endTurn(chatID)

	if turnErr != nil {
		if ctx.Err() != nil || turnCtx.Err() != nil {
			// A cancelled turn sends no completion and leaves no assistant row:
			// the request never produced an answer worth keeping.
			s.send(ctx, bot, chatID, "Turno cancelado. El mensaje sigue en el historial: mandá /new o volvé a preguntar.")
			return
		}
		// The failure is reported to the user because a silent turn looks like
		// the bot is broken, and the cause is usually a model or provider choice
		// the operator can act on.
		s.send(ctx, bot, chatID, "Error del gateway: "+turnErr.Error())
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
	case "pi":
		s.cmdPi(ctx, bot, chatID, args)
	case "cancel":
		s.cmdCancel(ctx, bot, chatID)
	case "estado", "avance", "dev":
		s.cmdDevStatus(ctx, bot, chatID, args)
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
		baseURL, cliToken := s.gateway()
		ids, err := fetchModelIDs(ctx, baseURL, cliToken)
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

// cmdPi submits an instruction for the development session.
//
// It is the one writing direction of this bot: /estado and the feed only show
// what the session already produced, while /pi hands it an instruction. The
// delivery contract is the notification spool in reverse — the command appends
// an `order` event, the session drains it, acts, and reports back through the
// usual feed, so one line is one instruction and nothing here executes
// anything itself. The bot runs on the operator's machine behind the
// allowlist, and this is the operator's standing order machine; a remote
// stranger gets nothing, because handleUpdate gates every path before this.
func (s *Supervisor) cmdPi(ctx context.Context, bot *tgbot.Bot, chatID int64, args string) {
	if strings.TrimSpace(args) == "" {
		s.send(ctx, bot, chatID, "Decime la orden después del comando: /pi arreglá el test que flaquea.")
		return
	}

	inbox, err := s.repo.DevInbox()
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude abrir la bandeja de órdenes.")
		return
	}
	if err := inbox.Append(notify.Event{
		Kind:   notify.KindOrder,
		Title:  "Orden desde Telegram",
		Detail: strings.TrimSpace(args),
	}); err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude registrar la orden.")
		return
	}
	s.send(ctx, bot, chatID, "Orden registrada. La ejecuta la sesión de desarrollo a la próxima vuelta.")
}

// cmdCancel aborts the chat's in-flight turn (S7).
//
// "Cancelar" (the label a reply-keyboard button shows) is accepted alongside
// the slash command, because a tap on the keyboard sends that text as an
// ordinary message; treating it as a command keeps the button honest instead
// of leaking the word into a model that was never asked for it.
func (s *Supervisor) cmdCancel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	if s.cancelTurn(chatID) {
		s.send(ctx, bot, chatID, "Cancelando el turno en vuelo.")
		return
	}
	s.send(ctx, bot, chatID, "No hay nada en vuelo que cancelar.")
}

// cmdDevStatus reports where the development stands: the branch, the last
// commit, the work still uncommitted and, when a feature is named, that ODD
// document's task progress.
//
// It reads the repository and never writes to it. The allowed chat is already
// fixed by the allowlist gate in handleUpdate, so this command widens what an
// operator can *see* without widening what anyone can *do*.
func (s *Supervisor) cmdDevStatus(ctx context.Context, bot *tgbot.Bot, chatID int64, args string) {
	repo, err := s.repo.DevRepoRoot()
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude leer la configuración del repositorio.")
		return
	}
	if repo == "" {
		s.send(ctx, bot, chatID, "No hay repositorio configurado. Definí DEV_REPO_ROOT en el entorno del gateway.")
		return
	}

	feature := strings.TrimSpace(args)
	if feature == "" {
		feature, _ = s.repo.DevFeatureName()
	}

	snap := devstatus.Repo{Root: repo}.Take(ctx, feature)
	s.sendHTML(ctx, bot, chatID, snap.RenderHTML())
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
		"/cancel — cancelar el turno en vuelo",
		"/pi <orden> — dejar una orden para la sesión de desarrollo",
		"/estado — avance del desarrollo (commits, pendientes, tareas)",
		"/estado <feature> — avance de una feature ODD concreta",
		"/help — esta ayuda",
	}, "\n")
}

// ensure db stays referenced by the interface docs above.
var _ = db.TelegramSession{}
