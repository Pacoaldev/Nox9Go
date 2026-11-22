package telegram

import (
	"context"
	"fmt"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"nox9go/proxy/internal/notify"
)

// poll is the loop body. It runs one long-poll bot until ctx is cancelled, which
// is the only way it exits: a broken token surfaces as an error on the status
// instead of silently retrying forever.
func (s *Supervisor) poll(ctx context.Context, repo Repo) {
	token, err := repo.TelegramToken()
	if err != nil {
		s.setError(err)
		return
	}
	if token == "" {
		return
	}

	bot, err := tgbot.New(token,
		tgbot.WithDefaultHandler(s.handleUpdate),
		// The library logs every update payload by default, which would put
		// conversation content in the gateway log. Route its noise to a
		// discard so failures are reported through the supervisor instead.
		tgbot.WithErrorsHandler(func(err error) { s.setError(err) }),
	)
	if err != nil {
		s.setError(fmt.Errorf("telegram: create bot: %w", err))
		return
	}

	me, err := bot.GetMe(ctx)
	if err != nil {
		s.setError(fmt.Errorf("telegram: token rejected: %w", err))
		return
	}
	s.mu.Lock()
	s.username = me.Username
	s.mu.Unlock()
	// Telegram Web and the mobile clients draw a command menu button from this
	// registration. Without it the chat opens with nothing to tap, which is
	// how the modes stayed undiscoverable until /panel was typed by hand.
	if _, err := bot.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{Commands: botCommands()}); err != nil {
		// A failed registration is cosmetic for users who know the commands,
		// so it is reported but never blocks the channel from running.
		s.setError(fmt.Errorf("telegram: register commands: %w", err))
	}

	// The development feed runs alongside the poll loop: Start blocks, so the
	// drain owns its own goroutine and stops with the same context.
	go s.drainNotifications(ctx, bot, repo)

	// Start blocks until ctx is cancelled. It logs every update payload at debug
	// level through the library's own logger, which is why the errors handler is
	// overridden above.
	bot.Start(ctx)

	if _, err := bot.Close(ctx); err != nil {
		// A close failure during shutdown is not worth reporting as a channel
		// fault: the loop is already gone.
		_ = err
	}
}

// drainNotifications forwards development events to the allowed chats.
//
// It is the read side of internal/notify: a development session appends, this
// forwards. Nothing it sends can come back as an instruction, so the feed
// widens what the operator sees without widening what the bot can do.
func (s *Supervisor) drainNotifications(ctx context.Context, bot *tgbot.Bot, repo Repo) {
	spool, err := repo.DevSpool()
	if err != nil {
		s.setError(err)
		return
	}
	if spool == nil || spool.Path == "" {
		// No feed configured: the channel still answers /estado on demand.
		return
	}

	// One poll of the event queue per interval. The interval is a constant
	// rather than a setting because a development event is not urgent enough to
	// justify tuning, and a shorter one would spend the bot's rate limit.
	const interval = 5 * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.forwardPendingEvents(ctx, bot, repo, spool)
		}
	}
}

// forwardPendingEvents drains the spool once and sends whatever it found.
func (s *Supervisor) forwardPendingEvents(ctx context.Context, bot *tgbot.Bot, repo Repo, spool *notify.Spool) {
	events, err := spool.Drain()
	if err != nil {
		s.setError(err)
		return
	}
	if len(events) == 0 {
		return
	}

	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		s.setError(err)
		return
	}
	// Fail-closed: an event is only ever delivered to an allowlisted chat. A
	// channel with no allowlist already refuses to poll, and this guard means a
	// config change mid-flight cannot leak a report to nobody.
	if len(cfg.AllowedUserIDs) == 0 {
		return
	}

	for _, event := range events {
		text := event.Render()
		for _, chatID := range cfg.AllowedUserIDs {
			s.send(ctx, bot, chatID, text)
		}
	}
}

// handleUpdate is the default handler: it gates on the allowlist before doing
// anything at all, so an unauthorized message costs no provider call and no
// token.
func (s *Supervisor) handleUpdate(ctx context.Context, bot *tgbot.Bot, update *models.Update) {
	if update == nil {
		return
	}
	// An inline button press carries its own authorization: the same gate
	// decides, and handleCallbackQuery applies it before acting.
	if update.CallbackQuery != nil {
		s.handleCallbackQuery(ctx, bot, update.CallbackQuery)
		return
	}

	msg := update.Message
	if msg == nil || msg.From == nil {
		// Edits and channel posts carry nothing this channel acts on.
		return
	}

	// S3: fail-closed authorization. The gate runs before any state is touched
	// and before any upstream call is prepared.
	if !s.Allowed(msg.From.ID) {
		return
	}

	s.handleMessage(ctx, bot, msg)
}
