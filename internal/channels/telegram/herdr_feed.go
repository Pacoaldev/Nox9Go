package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/notify"
)

const cbHerdrChoicePrefix = "m:herdr:"

func (s *Supervisor) rememberHerdrChoices(chatID int64, choices []HerdrChoice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.herdrChoices == nil {
		s.herdrChoices = make(map[int64][]HerdrChoice)
	}
	s.herdrChoices[chatID] = append([]HerdrChoice(nil), choices...)
}

func (s *Supervisor) herdrChoiceAt(chatID int64, index int) (HerdrChoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.herdrChoices[chatID]
	if index < 0 || index >= len(list) {
		return HerdrChoice{}, false
	}
	return list[index], true
}

func herdrChoiceMarkup(choices []HerdrChoice) models.ReplyMarkup {
	perRow := 2
	if len(choices) <= 4 {
		perRow = 1
	}
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for i, ch := range choices {
		if i > 0 && i%perRow == 0 {
			rows = append(rows, row)
			row = nil
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         ch.Label,
			CallbackData: fmt.Sprintf("%s%d", cbHerdrChoicePrefix, i),
		})
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// herdrBlockerDialog is optional preset content from the passive watcher (e.g.
// Gentle Shell desktop feed). When choices are set, the pane is not re-parsed.
type herdrBlockerDialog struct {
	Title   string
	Choices []HerdrChoice
	Desktop *GentleDesktopAnswerTarget
}

func (s *Supervisor) sendHerdrBlockerFeed(ctx context.Context, bot *tgbot.Bot, chatID int64, event notify.Event, plain string, preset ...herdrBlockerDialog) {
	if !herdrTelegramRelay() {
		s.send(ctx, bot, chatID, plain)
		return
	}
	var dialogTitle string
	var choices []HerdrChoice
	if len(preset) > 0 && len(preset[0].Choices) > 0 {
		dialogTitle = strings.TrimSpace(preset[0].Title)
		choices = preset[0].Choices
	} else {
		screen, err := readHerdrAgentScreen(ctx)
		if err != nil {
			screen = event.Detail
		}
		dialogTitle, choices = ParseHerdrDialog(screen)
	}
	if len(choices) == 0 {
		s.send(ctx, bot, chatID, plain)
		return
	}
	s.rememberHerdrChoices(chatID, choices)
	if len(preset) > 0 && preset[0].Desktop != nil {
		s.rememberGentleDesktopAnswer(chatID, *preset[0].Desktop)
	}
	prompt := strings.TrimSpace(dialogTitle)
	if prompt == "" {
		prompt = strings.TrimSpace(event.Title)
	}
	stamp := event.At.UTC().Format("15:04")
	text := fmt.Sprintf("[%s] decisión\n%s\n\nElegí una opción:", stamp, prompt)
	if _, err := bot.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: herdrChoiceMarkup(choices),
	}); err != nil {
		s.setError(fmt.Errorf("telegram: herdr blocker buttons: %w", err))
		s.send(ctx, bot, chatID, plain)
	}
}

func (s *Supervisor) handleHerdrChoiceCallback(ctx context.Context, bot *tgbot.Bot, chatID int64, data string, answer func(string)) {
	idxStr := strings.TrimPrefix(data, cbHerdrChoicePrefix)
	var idx int
	if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil {
		answer("Opción inválida.")
		return
	}
	choice, ok := s.herdrChoiceAt(chatID, idx)
	if !ok {
		answer("Esa opción ya no aplica.")
		return
	}
	if target, ok := s.gentleDesktopAnswerTarget(chatID); ok {
		if err := submitGentleDesktopAnswerOption(target, idx); err == nil {
			s.clearGentleDesktopAnswer(chatID)
			s.mu.Lock()
			s.herdrWatchSig = ""
			s.mu.Unlock()
			answer("Respuesta enviada a Gentle Shell.")
			s.wakeOrderDrain()
			return
		}
	}
	if err := sendHerdrAgentKeys(ctx, choice.Keys...); err != nil {
		s.setError(err)
		answer("No pude enviar la tecla a Herdr.")
		return
	}
	answer("Enviado a Herdr.")
	s.wakeOrderDrain()
}