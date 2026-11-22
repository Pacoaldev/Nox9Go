package db

import "testing"

// S5: a chat starts with no session. That is the "idle" state the handler uses
// to avoid spending tokens on stray input.
func TestTelegramSession_NoSessionAtStart(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	s, err := repo.ActiveTelegramSession(42)
	if err != nil {
		t.Fatalf("ActiveTelegramSession() error = %v", err)
	}
	if s != nil {
		t.Errorf("ActiveTelegramSession() = %+v, want nil", s)
	}
}

// S5: starting a session makes it the active one, and starting a second archives
// the first. A chat must never have two open sessions, or the transcript would
// belong to whichever row a query happened to return.
func TestTelegramSession_StartArchivesPrevious(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	first, err := repo.StartTelegramSession(42, "first")
	if err != nil {
		t.Fatalf("StartTelegramSession(first) error = %v", err)
	}
	second, err := repo.StartTelegramSession(42, "second")
	if err != nil {
		t.Fatalf("StartTelegramSession(second) error = %v", err)
	}

	active, err := repo.ActiveTelegramSession(42)
	if err != nil {
		t.Fatalf("ActiveTelegramSession() error = %v", err)
	}
	if active == nil || active.ID != second.ID {
		t.Fatalf("ActiveTelegramSession() = %+v, want session %d", active, second.ID)
	}

	archived, err := repo.TelegramSessionByID(first.ID)
	if err != nil {
		t.Fatalf("TelegramSessionByID() error = %v", err)
	}
	if archived.ArchivedAt == "" {
		t.Errorf("first session ArchivedAt = empty, want a timestamp after a second start")
	}
}

// S5: /end archives the active session, returning the chat to idle.
func TestTelegramSession_Archive(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	s, err := repo.StartTelegramSession(42, "chat")
	if err != nil {
		t.Fatalf("StartTelegramSession() error = %v", err)
	}
	if err := repo.ArchiveTelegramSession(s.ID); err != nil {
		t.Fatalf("ArchiveTelegramSession() error = %v", err)
	}

	active, err := repo.ActiveTelegramSession(42)
	if err != nil {
		t.Fatalf("ActiveTelegramSession() error = %v", err)
	}
	if active != nil {
		t.Errorf("ActiveTelegramSession() = %+v after /end, want nil", active)
	}

	// Archiving twice must not fail: /end at the end of a chat just confirms.
	if err := repo.ArchiveTelegramSession(s.ID); err != nil {
		t.Errorf("ArchiveTelegramSession() twice = %v, want nil", err)
	}
}

// S5: the transcript round-trips in order and carries both roles.
func TestTelegramSession_TranscriptOrder(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	s, err := repo.StartTelegramSession(42, "chat")
	if err != nil {
		t.Fatalf("StartTelegramSession() error = %v", err)
	}
	for _, m := range []struct{ role, content string }{
		{"user", "primero"},
		{"assistant", "segundo"},
		{"user", "tercero"},
	} {
		if err := repo.AppendTelegramMessage(s.ID, m.role, m.content); err != nil {
			t.Fatalf("AppendTelegramMessage(%q) error = %v", m.content, err)
		}
	}

	got, err := repo.TelegramTranscript(s.ID, 10)
	if err != nil {
		t.Fatalf("TelegramTranscript() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("transcript length = %d, want 3", len(got))
	}
	wantRoles := []string{"user", "assistant", "user"}
	wantContent := []string{"primero", "segundo", "tercero"}
	for i := range got {
		if got[i].Role != wantRoles[i] || got[i].Content != wantContent[i] {
			t.Errorf("turn %d = %+v, want {%s %s}", i, got[i], wantRoles[i], wantContent[i])
		}
	}
}

// S9: the transcript read is bounded. A long chat must not make the prompt grow
// without limit, which is a memory and a cost problem on small hardware.
func TestTelegramSession_TranscriptIsBounded(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	s, err := repo.StartTelegramSession(42, "chat")
	if err != nil {
		t.Fatalf("StartTelegramSession() error = %v", err)
	}
	for i := 0; i < 60; i++ {
		if err := repo.AppendTelegramMessage(s.ID, "user", "turno"); err != nil {
			t.Fatalf("AppendTelegramMessage() error = %v", err)
		}
	}

	got, err := repo.TelegramTranscript(s.ID, 10)
	if err != nil {
		t.Fatalf("TelegramTranscript() error = %v", err)
	}
	if len(got) != 10 {
		t.Errorf("transcript length = %d, want the 10 newest turns", len(got))
	}
}

// S5/S6: the model chosen with /model persists, so a restart keeps it.
func TestTelegramChannelModel_RoundTrip(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	got, err := repo.TelegramChannelModel()
	if err != nil {
		t.Fatalf("TelegramChannelModel() error = %v", err)
	}
	if got != "" {
		t.Errorf("TelegramChannelModel() = %q, want empty before any choice", got)
	}

	if err := repo.SetTelegramChannelModel("anthropic/claude-sonnet"); err != nil {
		t.Fatalf("SetTelegramChannelModel() error = %v", err)
	}
	got, err = repo.TelegramChannelModel()
	if err != nil {
		t.Fatalf("TelegramChannelModel() error = %v", err)
	}
	if got != "anthropic/claude-sonnet" {
		t.Errorf("TelegramChannelModel() = %q, want the stored model", got)
	}
}

// S1: a model can be chosen before a token exists, and doing so must not enable
// the channel. Enabled still requires a token AND an allowlist.
func TestTelegramChannelModel_DoesNotEnableChannel(t *testing.T) {
	repo := NewRepo(openTelegramTestDB(t))

	if err := repo.SetTelegramChannelModel("some/model"); err != nil {
		t.Fatalf("SetTelegramChannelModel() error = %v", err)
	}
	cfg, err := repo.GetTelegramChannelConfig()
	if err != nil {
		t.Fatalf("GetTelegramChannelConfig() error = %v", err)
	}
	if cfg.Enabled {
		t.Errorf("Enabled = true with a model but no token, want false")
	}
}
