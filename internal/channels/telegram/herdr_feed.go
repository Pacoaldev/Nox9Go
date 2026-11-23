package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/config"
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

func (s *Supervisor) sendHerdrBlockerFeed(ctx context.Context, bot *tgbot.Bot, chatID int64, event notify.Event, plain string) {
	runner := strings.ToLower(strings.TrimSpace(config.DevOrderRunner()))
	if strings.TrimSpace(config.HerdrAgentTarget()) == "" && !strings.Contains(runner, "herdr") {
		s.send(ctx, bot, chatID, plain)
		return
	}
	screen, err := readHerdrAgentScreen(ctx)
	if err != nil {
		screen = event.Detail
	}
	dialogTitle, choices := ParseHerdrDialog(screen)
	if len(choices) == 0 {
		s.send(ctx, bot, chatID, plain)
		return
	}
	s.rememberHerdrChoices(chatID, choices)
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
	if err := sendHerdrAgentKeys(ctx, choice.Keys...); err != nil {
		s.setError(err)
		answer("No pude enviar la tecla a Herdr.")
		return
	}
	answer("Enviado a Herdr.")
	s.wakeOrderDrain()
}