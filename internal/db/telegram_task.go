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
	ChatID      int64  `json:"chatId"`
	Instruction string `json:"instruction"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// GetTelegramAgentTask reads the chat's task. A chat with no row reports an idle
// task rather than an error, so a fresh chat renders a panel immediately.
func (r *Repo) GetTelegramAgentTask(chatID int64) (*TelegramAgentTask, error) {
	task := &TelegramAgentTask{ChatID: chatID}
	err := r.db.QueryRow(
		`SELECT instruction, status, createdAt, updatedAt
		   FROM telegramAgentTask WHERE chatId = ?`, chatID,
	).Scan(&task.Instruction, &task.Status, &task.CreatedAt, &task.UpdatedAt)
	if err == sql.ErrNoRows {
		task.Status = "idle"
		return task, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get telegram agent task: %w", err)
	}
	return task, nil
}

// SetTelegramAgentTask writes the chat's task state.
func (r *Repo) SetTelegramAgentTask(chatID int64, instruction, status string) error {
	instruction = strings.TrimSpace(instruction)
	status = strings.TrimSpace(status)
	if status == "" {
		return fmt.Errorf("set telegram agent task: empty status")
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramAgentTask (chatId, instruction, status, createdAt, updatedAt)
		 VALUES (?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(chatId) DO UPDATE SET
		   instruction = excluded.instruction,
		   status = excluded.status,
		   updatedAt = excluded.updatedAt`,
		chatID, instruction, status,
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
