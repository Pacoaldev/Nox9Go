package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const desktopActionSchema = "gentle-shell.desktop-action/v1"

// GentleDesktopAnswerTarget is the open ask_user_choice from a desktop feed snapshot.
type GentleDesktopAnswerTarget struct {
	AgentDir   string
	SessionID  string
	ToolCallID string
}

func (s *Supervisor) rememberGentleDesktopAnswer(chatID int64, target GentleDesktopAnswerTarget) {
	if target.AgentDir == "" || target.SessionID == "" || target.ToolCallID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gentleDesktopAnswers == nil {
		s.gentleDesktopAnswers = make(map[int64]GentleDesktopAnswerTarget)
	}
	s.gentleDesktopAnswers[chatID] = target
}

func (s *Supervisor) gentleDesktopAnswerTarget(chatID int64) (GentleDesktopAnswerTarget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.gentleDesktopAnswers[chatID]
	return t, ok
}

func (s *Supervisor) clearGentleDesktopAnswer(chatID int64) {
	s.mu.Lock()
	delete(s.gentleDesktopAnswers, chatID)
	s.mu.Unlock()
}

// submitGentleDesktopAnswerOption drops an answer_question action into Gentle Shell's
// desktop inbox (via: session). Herdr send-keys often cannot reach the Pi TUI.
func submitGentleDesktopAnswerOption(target GentleDesktopAnswerTarget, optionIndex int) error {
	agentDir := strings.TrimSpace(target.AgentDir)
	sessionID := strings.TrimSpace(target.SessionID)
	toolCallID := strings.TrimSpace(target.ToolCallID)
	if agentDir == "" || sessionID == "" || toolCallID == "" {
		return fmt.Errorf("gentle desktop answer: incomplete target")
	}
	if optionIndex < 0 || optionIndex > 63 {
		return fmt.Errorf("gentle desktop answer: option %d out of range", optionIndex)
	}
	inbox := filepath.Join(agentDir, "desktop", "inbox", sessionID)
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		return fmt.Errorf("gentle desktop inbox: %w", err)
	}
	actionID := "tg-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	body := map[string]any{
		"schema":    desktopActionSchema,
		"id":        actionID,
		"sessionId": sessionID,
		"kind":      "answer_question",
		"args": map[string]any{
			"toolCallId": toolCallID,
			"option":     optionIndex,
			"via":        "session",
		},
		"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	path := filepath.Join(inbox, actionID+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("gentle desktop action write: %w", err)
	}
	return nil
}