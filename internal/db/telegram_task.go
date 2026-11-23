package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// telegramAgentTaskTables holds the task an agent-mode chat is currently
// supervising. It is Go-only and additive, like the rest of the channel tables.
//
// One row per chat, not a history: the panel can only ever show the task the
// chat is on now, and a history nobody can act on would just grow.
var telegramAgentTaskTables = []tableDef{
	{
		name: "telegramAgentTask",
		columns: [][2]string{
			{"chatId", "INTEGER PRIMARY KEY"},
			{"instruction", "TEXT NOT NULL DEFAULT ''"},
			{"status", "TEXT NOT NULL DEFAULT 'idle'"},
			{"correlationId", "TEXT NOT NULL DEFAULT ''"},
			{"createdAt", "TEXT NOT NULL DEFAULT ''"},
			{"updatedAt", "TEXT NOT NULL DEFAULT ''"},
		},
	},
}

// EnsureTelegramAgentTaskTables creates the agent task table.
func EnsureTelegramAgentTaskTables(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("telegram agent task tables: nil db")
	}
	for _, t := range telegramAgentTaskTables {
		if err := ensureTable(db, t); err != nil {
			return err
		}
	}
	return nil
}

// TelegramAgentTask is the task a chat is supervising.
type TelegramAgentTask struct {
	ChatID        int64  `json:"chatId"`
	Instruction   string `json:"instruction"`
	Status        string `json:"status"`
	CorrelationID string `json:"correlationId"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// GetTelegramAgentTask reads the chat's task. A chat with no row reports an idle
// task rather than an error, so a fresh chat renders a panel immediately.
func (r *Repo) GetTelegramAgentTask(chatID int64) (*TelegramAgentTask, error) {
	task := &TelegramAgentTask{ChatID: chatID}
	err := r.db.QueryRow(
		`SELECT instruction, status, correlationId, createdAt, updatedAt
		   FROM telegramAgentTask WHERE chatId = ?`, chatID,
	).Scan(&task.Instruction, &task.Status, &task.CorrelationID, &task.CreatedAt, &task.UpdatedAt)
	if err == sql.ErrNoRows {
		task.Status = "idle"
		return task, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get telegram agent task: %w", err)
	}
	return task, nil
}

// SetTelegramAgentTask writes the chat's task state. An empty correlationID
// leaves the stored id unchanged on update.
func (r *Repo) SetTelegramAgentTask(chatID int64, instruction, status, correlationID string) error {
	instruction = strings.TrimSpace(instruction)
	status = strings.TrimSpace(status)
	correlationID = strings.TrimSpace(correlationID)
	if status == "" {
		return fmt.Errorf("set telegram agent task: empty status")
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramAgentTask (chatId, instruction, status, correlationId, createdAt, updatedAt)
		 VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(chatId) DO UPDATE SET
		   instruction = excluded.instruction,
		   status = excluded.status,
		   correlationId = COALESCE(NULLIF(excluded.correlationId, ''), telegramAgentTask.correlationId),
		   updatedAt = excluded.updatedAt`,
		chatID, instruction, status, correlationID,
	)
	if err != nil {
		return fmt.Errorf("set telegram agent task: %w", err)
	}
	return nil
}

// TelegramAutoApprove reports whether this chat skips the approval step.
func (r *Repo) TelegramAutoApprove(chatID int64) (bool, error) {
	var v int
	err := r.db.QueryRow(
		`SELECT autoApprove FROM telegramChatSetting WHERE chatId = ?`, chatID,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get telegram auto approve: %w", err)
	}
	return v != 0, nil
}

// TelegramDockHidden reports whether the reply-keyboard dock is hidden.
func (r *Repo) TelegramDockHidden(chatID int64) (bool, error) {
	var v int
	err := r.db.QueryRow(
		`SELECT dockHidden FROM telegramChatSetting WHERE chatId = ?`, chatID,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get telegram dock hidden: %w", err)
	}
	return v != 0, nil
}

// SetTelegramDockHidden stores whether the dock stays off screen for the chat.
func (r *Repo) SetTelegramDockHidden(chatID int64, hidden bool) error {
	v := 0
	if hidden {
		v = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramChatSetting (chatId, dockHidden, updatedAt)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(chatId) DO UPDATE SET
		   dockHidden = excluded.dockHidden,
		   updatedAt = excluded.updatedAt`,
		chatID, v,
	)
	if err != nil {
		return fmt.Errorf("set telegram dock hidden: %w", err)
	}
	return nil
}

// TelegramFeedScope returns 0=all, 1=task-only, 2=silent development feed.
func (r *Repo) TelegramFeedScope(chatID int64) (int, error) {
	var v int
	err := r.db.QueryRow(
		`SELECT feedScope FROM telegramChatSetting WHERE chatId = ?`, chatID,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get telegram feed scope: %w", err)
	}
	return v, nil
}

func (r *Repo) SetTelegramFeedScope(chatID int64, scope int) error {
	if scope < 0 || scope > 2 {
		return fmt.Errorf("set telegram feed scope: invalid %d", scope)
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramChatSetting (chatId, feedScope, updatedAt)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(chatId) DO UPDATE SET
		   feedScope = excluded.feedScope,
		   updatedAt = excluded.updatedAt`,
		chatID, scope,
	)
	if err != nil {
		return fmt.Errorf("set telegram feed scope: %w", err)
	}
	return nil
}

func (r *Repo) TelegramAgentCompactDock(chatID int64) (bool, error) {
	var v int
	err := r.db.QueryRow(
		`SELECT agentCompactDock FROM telegramChatSetting WHERE chatId = ?`, chatID,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get telegram agent compact dock: %w", err)
	}
	return v != 0, nil
}

func (r *Repo) SetTelegramAgentCompactDock(chatID int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramChatSetting (chatId, agentCompactDock, updatedAt)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(chatId) DO UPDATE SET
		   agentCompactDock = excluded.agentCompactDock,
		   updatedAt = excluded.updatedAt`,
		chatID, v,
	)
	if err != nil {
		return fmt.Errorf("set telegram agent compact dock: %w", err)
	}
	return nil
}

func (r *Repo) TelegramPlaceholderHint(chatID int64) (int, error) {
	var v int
	err := r.db.QueryRow(
		`SELECT placeholderHint FROM telegramChatSetting WHERE chatId = ?`, chatID,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get telegram placeholder hint: %w", err)
	}
	return v, nil
}

func (r *Repo) AdvanceTelegramPlaceholderHint(chatID int64, modulo int) error {
	if modulo < 1 {
		modulo = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramChatSetting (chatId, placeholderHint, updatedAt)
		 VALUES (?, 1, datetime('now'))
		 ON CONFLICT(chatId) DO UPDATE SET
		   placeholderHint = (telegramChatSetting.placeholderHint + 1) % ?,
		   updatedAt = excluded.updatedAt`,
		chatID, modulo,
	)
	if err != nil {
		return fmt.Errorf("advance telegram placeholder hint: %w", err)
	}
	return nil
}

// SetTelegramAutoApprove stores the chat's auto-approve preference.
func (r *Repo) SetTelegramAutoApprove(chatID int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramChatSetting (chatId, autoApprove, updatedAt)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(chatId) DO UPDATE SET
		   autoApprove = excluded.autoApprove,
		   updatedAt = excluded.updatedAt`,
		chatID, v,
	)
	if err != nil {
		return fmt.Errorf("set telegram auto approve: %w", err)
	}
	return nil
}
