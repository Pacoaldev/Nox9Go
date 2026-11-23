package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"

	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/notify"
)

const herdrPassiveWatchInterval = 4 * time.Second

func herdrBlockerSignature(agent, title string, choices []HerdrChoice) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(agent))
	b.WriteByte('|')
	b.WriteString(strings.TrimSpace(title))
	for _, c := range choices {
		b.WriteByte('|')
		b.WriteString(strings.TrimSpace(c.Label))
	}
	return b.String()
}

// watchHerdrPassive polls the focused Herdr Pi pane for blocker menus and pushes
// Telegram inline buttons without waiting for a Telegram-issued order (exit 2).
func (s *Supervisor) watchHerdrPassive(ctx context.Context, bot *tgbot.Bot, repo Repo) {
	if !config.HerdrPassiveWatch() || !herdrTelegramRelay() {
		return
	}

	ticker := time.NewTicker(herdrPassiveWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickHerdrPassiveWatch(ctx, bot, repo)
		}
	}
}

func (s *Supervisor) noteHerdrWatchProblem(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	s.mu.Lock()
	if s.herdrWatchErr == msg {
		s.mu.Unlock()
		return
	}
	s.herdrWatchErr = msg
	s.mu.Unlock()
	s.setError(fmt.Errorf("telegram herdr watch: %w", err))
}

func (s *Supervisor) tickHerdrPassiveWatch(ctx context.Context, bot *tgbot.Bot, repo Repo) {
	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		s.setError(err)
		return
	}
	if len(cfg.AllowedUserIDs) == 0 {
		return
	}

	agent, err := ResolveHerdrAgentName(ctx, config.HerdrAgentTarget())
	if err != nil {
		s.noteHerdrWatchProblem(err)
		return
	}

	screen, err := readHerdrAgentScreen(ctx)
	if err != nil {
		s.noteHerdrWatchProblem(err)
		return
	}
	title, choices := ParseHerdrDialog(screen)
	if len(choices) == 0 {
		s.mu.Lock()
		s.herdrWatchSig = ""
		s.mu.Unlock()
		return
	}

	sig := herdrBlockerSignature(agent, title, choices)
	s.mu.Lock()
	if sig == s.herdrWatchSig {
		s.mu.Unlock()
		return
	}
	s.herdrWatchSig = sig
	s.mu.Unlock()

	event := notify.Event{
		At:    time.Now().UTC(),
		Kind:  notify.KindBlocker,
		Title: "Agente bloqueado",
		Detail: firstNonEmpty(strings.TrimSpace(title), strings.TrimSpace(screen)),
	}
	plain := event.Render()
	for _, chatID := range cfg.AllowedUserIDs {
		scopeVal, err := repo.TelegramFeedScope(chatID)
		if err != nil {
			s.setError(err)
			scopeVal = 0
		}
		if !feedEventForwards(feedScope(scopeVal), event) {
			continue
		}
		s.sendHerdrBlockerFeed(ctx, bot, chatID, event, plain)
	}
}