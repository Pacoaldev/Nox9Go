package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"

	"nox9go/proxy/internal/devstatus"
)

// Callback values for the dock's inline subpanels (S4-S6). Every one starts with
// the panel namespace so a value from another feature can never be mistaken for
// one of these, and all of them are checked against Telegram's 64-byte budget by
// TestNewCallbackDataFitsLimit.
const (
	cbModelsPanel   = cbPrefix + "dock:models"
	cbSessionsPanel = cbPrefix + "dock:sessions"
	cbFeaturesPanel = cbPrefix + "dock:features"

	// The pick values carry an argument, so they are prefixes rather than whole
	// ids. A model id and a feature name can be longer than the payload budget,
	// which is why the argument is truncated and mapped back against the live
	// list instead of being trusted.
	cbModelPick   = cbPrefix + "dock:m:"
	cbSessionPick = cbPrefix + "dock:s:"
	cbFeaturePick = cbPrefix + "dock:f:"
)

// How many entries a subpanel draws. An inline keyboard of a hundred rows is not
// a list a phone can use, and the text says what the cap hid.
const (
	maxModelButtons   = 24
	maxSessionButtons = 10
	maxFeatureButtons = 24
)

// truncateBytes cuts a callback argument to a byte budget on a rune boundary. A
// payload split inside a UTF-8 sequence would be rejected or misread, which is
// worse than being short.
func truncateBytes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	cut := max
	for cut > 0 && (value[cut]&0xC0) == 0x80 {
		// 0b10xxxxxx is a continuation byte: step back to the rune's first byte.
		cut--
	}
	return value[:cut]
}

// matchCallbackValue maps a callback argument back to the one value it names.
//
// An exact id wins over a longer id that starts with it; otherwise a unique
// prefix is accepted. An ambiguous prefix is refused rather than guessed: sending
// a request to the wrong model or the wrong session is not a benign mistake.
func matchCallbackValue(values []string, payload string) (string, bool) {
	if payload == "" {
		return "", false
	}
	for _, value := range values {
		if value == payload {
			return value, true
		}
	}
	var match string
	found := 0
	for _, value := range values {
		if strings.HasPrefix(value, payload) {
			match = value
			found++
		}
	}
	if found == 1 {
		return match, true
	}
	return "", false
}

// truncateForPayload cuts a value so the callback it travels in fits Telegram's
// budget, given the prefix that carries it.
func truncateForPayload(prefix, value string) string {
	return truncateBytes(value, maxCallbackData-len(prefix))
}

// sendInline delivers one subpanel: a text that explains it, and the buttons.
func (s *Supervisor) sendInline(ctx context.Context, bot *tgbot.Bot, chatID int64, text string, rows [][]inlineButton) {
	if _, err := bot.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: markup(rows),
	}); err != nil {
		s.setError(fmt.Errorf("telegram: send subpanel: %w", err))
	}
}

// showModelPanel lists the models the gateway itself offers (S4).
//
// The list is read from the same route /model reads, so a deployment's own
// catalog is what the operator picks from and a model the gateway cannot
// dispatch is never offered as a button.
func (s *Supervisor) showModelPanel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	baseURL, cliToken := s.gateway()
	ids, err := fetchModelIDs(ctx, baseURL, cliToken)
	if err != nil || len(ids) == 0 {
		s.send(ctx, bot, chatID, "No pude listar los modelos. Usá /model <id> con un id que conozcas.")
		return
	}

	current, _ := s.repo.TelegramChannelModel()
	var b strings.Builder
	if current != "" {
		fmt.Fprintf(&b, "Modelo actual: %s\n", current)
	}
	b.WriteString("Modelos disponibles — tocá uno para usarlo.")

	shown := ids
	if len(shown) > maxModelButtons {
		shown = shown[:maxModelButtons]
		fmt.Fprintf(&b, "\nMostrando %d de %d — /model lista todos.", maxModelButtons, len(ids))
	}
	rows := make([][]inlineButton, 0, len(shown))
	for _, id := range shown {
		rows = append(rows, []inlineButton{{
			Text: id,
			Data: cbModelPick + truncateForPayload(cbModelPick, id),
		}})
	}
	s.sendInline(ctx, bot, chatID, b.String(), rows)
}

// pickModel stores the model a subpanel button names and redraws the panel.
func (s *Supervisor) pickModel(ctx context.Context, bot *tgbot.Bot, chatID int64, payload string, answer func(string)) {
	baseURL, cliToken := s.gateway()
	ids, err := fetchModelIDs(ctx, baseURL, cliToken)
	if err != nil || len(ids) == 0 {
		// The payload is only a prefix. Without the live list there is nothing
		// honest to map it to, and guessing would send turns to whatever model
		// happened to match.
		s.setError(fmt.Errorf("telegram: resolve model button: %w", err))
		answer("No pude verificar el modelo.")
		s.send(ctx, bot, chatID, "No pude verificar el modelo. Usá /model <id> con el id completo.")
		return
	}
	id, ok := matchCallbackValue(ids, payload)
	if !ok {
		answer("Ese modelo ya no está disponible.")
		s.send(ctx, bot, chatID, "Ese modelo ya no está disponible en este gateway.")
		return
	}
	if err := s.repo.SetTelegramChannelModel(id); err != nil {
		s.setError(err)
		answer("No pude guardar el modelo.")
		s.send(ctx, bot, chatID, "No pude guardar el modelo.")
		return
	}
	answer("Modelo cambiado")
	s.send(ctx, bot, chatID, "Modelo cambiado a: "+id)
	// S4: the mode panel is edited in place, so the new model is what the
	// operator reads without a second panel stacking in the chat.
	s.updatePanel(ctx, bot, chatID)
}

// showSessionsPanel lists the chat's last sessions (S5), from the same read
// /sessions uses.
func (s *Supervisor) showSessionsPanel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	sessions, err := s.repo.ListTelegramSessions(chatID, maxSessionButtons)
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, "No pude listar las sesiones.")
		return
	}
	if len(sessions) == 0 {
		s.send(ctx, bot, chatID, "No hay sesiones todavía. /new para empezar.")
		return
	}

	rows := make([][]inlineButton, 0, len(sessions))
	for _, session := range sessions {
		state := "activa"
		if session.ArchivedAt != "" {
			state = "archivada"
		}
		rows = append(rows, []inlineButton{{
			Text: fmt.Sprintf("#%d — %s — %s", session.ID, state, session.CreatedAt),
			Data: cbSessionPick + strconv.FormatInt(session.ID, 10),
		}})
	}
	s.sendInline(ctx, bot, chatID, "Últimas sesiones — tocá una para retomarla.", rows)
}

// retakeSession makes the tapped session the chat's open one again (S5).
//
// The payload is only a number, so ownership is checked against this chat's own
// list before anything is written: a crafted callback must not resume a session
// that belongs to another chat.
func (s *Supervisor) retakeSession(ctx context.Context, bot *tgbot.Bot, chatID int64, payload string, answer func(string)) {
	sessionID, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		answer("Sesión inválida.")
		return
	}
	sessions, err := s.repo.ListTelegramSessions(chatID, maxSessionButtons)
	if err != nil {
		s.setError(err)
		answer("No pude listar las sesiones.")
		return
	}
	owned := false
	for _, session := range sessions {
		if session.ID == sessionID {
			owned = true
			break
		}
	}
	if !owned {
		answer("Esa sesión no es de este chat.")
		s.send(ctx, bot, chatID, "Esa sesión no es de este chat.")
		return
	}
	if err := s.repo.ResumeTelegramSession(sessionID); err != nil {
		s.setError(err)
		answer("No pude retomar la sesión.")
		s.send(ctx, bot, chatID, "No pude retomar la sesión. Puede que ya no exista.")
		return
	}
	answer("Sesión retomada")
	s.send(ctx, bot, chatID, fmt.Sprintf(
		"Sesión %d retomada. Escribí lo que quieras. /end para cerrarla.", sessionID))
	s.updatePanel(ctx, bot, chatID)
}

// showFeaturePanel opens the ODD feature picker (S6), or answers directly when
// one feature is already configured for this deployment (L0b).
func (s *Supervisor) showFeaturePanel(ctx context.Context, bot *tgbot.Bot, chatID int64) {
	if feature, err := s.repo.DevFeatureName(); err == nil && strings.TrimSpace(feature) != "" {
		// A deployment that follows one feature is answered in one tap; a
		// picker would only ask a question the configuration already answered.
		s.cmdDevStatus(ctx, bot, chatID, strings.TrimSpace(feature))
		return
	}

	root, err := s.repo.DevRepoRoot()
	if err != nil {
		s.setError(err)
		s.send(ctx, bot, chatID, featureGuidance)
		return
	}
	features := devstatus.Repo{Root: root}.ListFeatures()
	if len(features) == 0 {
		s.send(ctx, bot, chatID, featureGuidance)
		return
	}

	shown := features
	if len(shown) > maxFeatureButtons {
		shown = shown[:maxFeatureButtons]
	}
	rows := make([][]inlineButton, 0, len(shown))
	for _, name := range shown {
		rows = append(rows, []inlineButton{{
			Text: name,
			Data: cbFeaturePick + truncateForPayload(cbFeaturePick, name),
		}})
	}
	text := "Features ODD — tocá una para ver su avance."
	if len(shown) < len(features) {
		text += fmt.Sprintf(" Mostrando %d de %d.", len(shown), len(features))
	}
	s.sendInline(ctx, bot, chatID, text, rows)
}

// renderFeature reports the feature a button names, through the same command
// /estado <feature> runs (S10).
func (s *Supervisor) renderFeature(ctx context.Context, bot *tgbot.Bot, chatID int64, payload string, answer func(string)) {
	root, err := s.repo.DevRepoRoot()
	if err != nil || root == "" {
		if err != nil {
			s.setError(err)
		}
		answer("Sin repositorio configurado.")
		s.send(ctx, bot, chatID, "No hay repositorio configurado. Definí DEV_REPO_ROOT en el entorno del gateway.")
		return
	}
	name, ok := matchCallbackValue(devstatus.Repo{Root: root}.ListFeatures(), payload)
	if !ok {
		answer("Esa feature ya no está disponible.")
		s.send(ctx, bot, chatID, featureGuidance)
		return
	}
	answer("Feature " + name)
	s.cmdDevStatus(ctx, bot, chatID, name)
}
