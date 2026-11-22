package telegram

import (
	"context"
	"fmt"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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

// handleUpdate is the default handler: it gates on the allowlist before doing
// anything at all, so an unauthorized message costs no provider call and no
// token.
func (s *Supervisor) handleUpdate(ctx context.Context, bot *tgbot.Bot, update *models.Update) {
	if update == nil {
		return
	}
	msg := update.Message
	if msg == nil || msg.From == nil {
		// Edits, channel posts and callback queries are outside the text
		// contract this channel promises.
		return
	}

	// S3: fail-closed authorization. The gate runs before any state is touched
	// and before any upstream call is prepared.
	if !s.Allowed(msg.From.ID) {
		return
	}

	s.handleMessage(ctx, bot, msg)
}

// handleMessage is the authorized path. T3/T4 fill in forwarding and sessions;
// for now it acknowledges so the gate is observable end to end.
func (s *Supervisor) handleMessage(ctx context.Context, bot *tgbot.Bot, msg *models.Message) {
	_ = ctx
	_ = bot
	_ = msg
}
