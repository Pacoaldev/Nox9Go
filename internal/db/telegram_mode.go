package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// BotMode is the behaviour a chat's free text is interpreted under.
//
// The distinction is not cosmetic. In chat mode a message is an ordinary turn:
// the gateway answers it and nothing in the repository changes. In agent mode
// the same words are an instruction, and the channel tracks a task whose state
// the operator supervises from the panel. Mixing the two is what made the
// channel's behaviour unpredictable.
type BotMode string

const (
	// ModeChat treats free text as conversation.
	ModeChat BotMode = "chat"
	// ModeAgent treats free text as a task instruction.
	ModeAgent BotMode = "agent"
)

// ParseBotMode maps a stored or received value to a mode. Anything that is not
// exactly "agent" reads as chat: an unknown value must leave the channel in the
// mode that spends no tokens on task machinery.
func ParseBotMode(v string) BotMode {
	if strings.TrimSpace(strings.ToLower(v)) == string(ModeAgent) {
		return ModeAgent
	}
	return ModeChat
}

// TelegramMode returns the channel's current mode. An installation that has
// never chosen one reports chat, which is how it already behaved.
func (r *Repo) TelegramMode() (BotMode, error) {
	var mode string
	err := r.db.QueryRow(`SELECT mode FROM telegramChannel WHERE id = 1`).Scan(&mode)
	if err == sql.ErrNoRows {
		return ModeChat, nil
	}
	if err != nil {
		return ModeChat, fmt.Errorf("get telegram mode: %w", err)
	}
	return ParseBotMode(mode), nil
}

// SetTelegramMode stores the mode. The row is created on demand so a mode can be
// chosen before a token exists.
func (r *Repo) SetTelegramMode(mode BotMode) error {
	_, err := r.db.Exec(
		`INSERT INTO telegramChannel (id, mode, updatedAt)
		 VALUES (1, ?, datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET
		   mode = excluded.mode,
		   updatedAt = excluded.updatedAt`,
		string(ParseBotMode(string(mode))),
	)
	if err != nil {
		return fmt.Errorf("set telegram mode: %w", err)
	}
	return nil
}
