package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// TelegramChannelModel returns the model the channel sends with each turn. An
// empty value means the operator has not chosen one yet, which the handler
// reports instead of guessing a model the deployment may not have.
func (r *Repo) TelegramChannelModel() (string, error) {
	var model string
	err := r.db.QueryRow(`SELECT defaultModel FROM telegramChannel WHERE id = 1`).Scan(&model)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get telegram model: %w", err)
	}
	return model, nil
}

// SetTelegramChannelModel stores the model chosen with /model. The row is created
// on demand so a model can be set before a token exists.
func (r *Repo) SetTelegramChannelModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("set telegram model: empty model")
	}
	_, err := r.db.Exec(
		`INSERT INTO telegramChannel (id, defaultModel, updatedAt)
		 VALUES (1, ?, datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET
		   defaultModel = excluded.defaultModel,
		   updatedAt = excluded.updatedAt`,
		model,
	)
	if err != nil {
		return fmt.Errorf("set telegram model: %w", err)
	}
	return nil
}

// TelegramSession is one conversation with one chat.
type TelegramSession struct {
	ID         int64  `json:"id"`
	ChatID     int64  `json:"chatId"`
	Title      string `json:"title"`
	CreatedAt  string `json:"createdAt"`
	ArchivedAt string `json:"archivedAt,omitempty"`
}

// ActiveTelegramSession returns the open session for a chat, or nil when the
// chat has none. A nil session is what the handler treats as "idle": stray input
// is answered with a hint and never reaches a provider.
func (r *Repo) ActiveTelegramSession(chatID int64) (*TelegramSession, error) {
	var s TelegramSession
	err := r.db.QueryRow(
		`SELECT id, chatId, title, createdAt, archivedAt
		   FROM telegramSession
		  WHERE chatId = ? AND archivedAt = ''
		  ORDER BY id DESC LIMIT 1`,
		chatID,
	).Scan(&s.ID, &s.ChatID, &s.Title, &s.CreatedAt, &s.ArchivedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active telegram session: %w", err)
	}
	return &s, nil
}

// StartTelegramSession opens a session for a chat. Any session already open for
// that chat is archived first, so a chat never has two active sessions and the
// history stays unambiguous.
func (r *Repo) StartTelegramSession(chatID int64, title string) (*TelegramSession, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("start telegram session: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`UPDATE telegramSession SET archivedAt = datetime('now')
		  WHERE chatId = ? AND archivedAt = ''`,
		chatID,
	); err != nil {
		return nil, fmt.Errorf("start telegram session: archive previous: %w", err)
	}
	res, err := tx.Exec(
		`INSERT INTO telegramSession (chatId, title, createdAt, archivedAt)
		 VALUES (?, ?, datetime('now'), '')`,
		chatID, title,
	)
	if err != nil {
		return nil, fmt.Errorf("start telegram session: insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("start telegram session: last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("start telegram session: commit: %w", err)
	}
	return r.TelegramSessionByID(id)
}

// ArchiveTelegramSession closes a session. Closing an already-closed session is
// not an error: /end twice at the end of a chat should just confirm the state.
func (r *Repo) ArchiveTelegramSession(sessionID int64) error {
	_, err := r.db.Exec(
		`UPDATE telegramSession SET archivedAt = datetime('now')
		  WHERE id = ? AND archivedAt = ''`,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("archive telegram session: %w", err)
	}
	return nil
}

// TelegramSessionByID reads one session by id.
func (r *Repo) TelegramSessionByID(sessionID int64) (*TelegramSession, error) {
	var s TelegramSession
	err := r.db.QueryRow(
		`SELECT id, chatId, title, createdAt, archivedAt FROM telegramSession WHERE id = ?`,
		sessionID,
	).Scan(&s.ID, &s.ChatID, &s.Title, &s.CreatedAt, &s.ArchivedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get telegram session %d: %w", sessionID, err)
	}
	return &s, nil
}

// ListTelegramSessions returns a chat's sessions, newest first. limit is clamped
// so a chat with a long history cannot make the dashboard read the whole table.
func (r *Repo) ListTelegramSessions(chatID int64, limit int) ([]TelegramSession, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := r.db.Query(
		`SELECT id, chatId, title, createdAt, archivedAt
		   FROM telegramSession
		  WHERE chatId = ?
		  ORDER BY id DESC LIMIT ?`,
		chatID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list telegram sessions: %w", err)
	}
	defer rows.Close()

	out := make([]TelegramSession, 0, limit)
	for rows.Next() {
		var s TelegramSession
		if err := rows.Scan(&s.ID, &s.ChatID, &s.Title, &s.CreatedAt, &s.ArchivedAt); err != nil {
			return nil, fmt.Errorf("scan telegram session: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telegram sessions: %w", err)
	}
	return out, nil
}

// TelegramTranscriptMessage is one stored turn.
type TelegramTranscriptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AppendTelegramMessage stores one turn of a session.
func (r *Repo) AppendTelegramMessage(sessionID int64, role, content string) error {
	_, err := r.db.Exec(
		`INSERT INTO telegramMessage (sessionId, role, content, createdAt)
		 VALUES (?, ?, ?, datetime('now'))`,
		sessionID, role, content,
	)
	if err != nil {
		return fmt.Errorf("append telegram message: %w", err)
	}
	return nil
}

// TelegramTranscript returns a session's turns in order.
//
// limit applies to the newest turns and the result is returned oldest-first, so
// the caller gets the most recent window in the order a chat expects. A bounded
// read is the point: an unbounded transcript would grow the prompt and the
// memory of a box that has little of either.
func (r *Repo) TelegramTranscript(sessionID int64, limit int) ([]TelegramTranscriptMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	rows, err := r.db.Query(
		`SELECT role, content FROM (
		   SELECT id, role, content FROM telegramMessage
		    WHERE sessionId = ? ORDER BY id DESC LIMIT ?
		 ) ORDER BY id`,
		sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get telegram transcript: %w", err)
	}
	defer rows.Close()

	out := make([]TelegramTranscriptMessage, 0, limit)
	for rows.Next() {
		var m TelegramTranscriptMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, fmt.Errorf("scan telegram transcript: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telegram transcript: %w", err)
	}
	return out, nil
}
