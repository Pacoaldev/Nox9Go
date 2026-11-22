package db

import "testing"

// S1: an installation that never chose a mode reads chat. Existing rows keep
// behaving as they did, without a migration.
func TestTelegramMode_DefaultsToChat(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))
	mode, err := repo.TelegramMode()
	if err != nil {
		t.Fatalf("TelegramMode() error = %v", err)
	}
	if mode != ModeChat {
		t.Errorf("TelegramMode() = %q, want chat", mode)
	}
}

// S1: the mode survives a write and a re-read, and an empty column reads chat.
func TestTelegramMode_RoundTrip(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	if err := repo.SetTelegramMode(ModeAgent); err != nil {
		t.Fatalf("SetTelegramMode(agent) error = %v", err)
	}
	got, err := repo.TelegramMode()
	if err != nil {
		t.Fatalf("TelegramMode() error = %v", err)
	}
	if got != ModeAgent {
		t.Errorf("after set agent, got %q", got)
	}

	if err := repo.SetTelegramMode(ModeChat); err != nil {
		t.Fatalf("SetTelegramMode(chat) error = %v", err)
	}
	if got, err = repo.TelegramMode(); err != nil || got != ModeChat {
		t.Errorf("after set chat, got %q, err %v", got, err)
	}
}

// S6/S7 storage: the task state is what was written, one row per chat.
func TestTelegramAgentTask_RoundTrip(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	task, err := repo.GetTelegramAgentTask(42)
	if err != nil {
		t.Fatalf("GetTelegramAgentTask() error = %v", err)
	}
	if task.Status != "idle" {
		t.Errorf("default task status = %q, want idle", task.Status)
	}

	if err := repo.SetTelegramAgentTask(42, "desplegar", "pending_approval"); err != nil {
		t.Fatalf("SetTelegramAgentTask() error = %v", err)
	}
	got, err := repo.GetTelegramAgentTask(42)
	if err != nil {
		t.Fatalf("GetTelegramAgentTask() error = %v", err)
	}
	if got.Instruction != "desplegar" || got.Status != "pending_approval" {
		t.Errorf("task = %+v, want instruction+pending", got)
	}

	// A second chat is independent: one row per chat, not one global task.
	if err := repo.SetTelegramAgentTask(7, "otra", "running"); err != nil {
		t.Fatalf("SetTelegramAgentTask() error = %v", err)
	}
	first, _ := repo.GetTelegramAgentTask(42)
	if first.Status != "pending_approval" {
		t.Errorf("chat 42 changed when chat 7 was written: %+v", first)
	}
}
