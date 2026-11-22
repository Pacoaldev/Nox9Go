package telegram

import (
	"context"

	"testing"
	"time"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/notify"
)

// fakeRepo is the storage surface the channel needs. It exists so the lifecycle
// tests exercise the gate and the loop without a database.
type fakeRepo struct {
	token   string
	allowed []int64
	model   string

	// repoTarget is the checkout the development report reads, and feature is
	// the ODD document it follows by default.
	repoRoot  string
	feature   string
	spoolPath string

	// session, when set, is the active session every chat reads back, so a
	// test can drive the forward path without a database.
	session *db.TelegramSession

	// appended, when set, records every transcript write, so a test can assert
	// what a turn actually stored.
	appended func(role, content string)

	// inboxPath, when set, points DevInbox at a real throwaway file.
	inboxPath string
}

func (f *fakeRepo) TelegramToken() (string, error) { return f.token, nil }

func (f *fakeRepo) TelegramChannelModel() (string, error) { return f.model, nil }

func (f *fakeRepo) SetTelegramChannelModel(m string) error { f.model = m; return nil }

func (f *fakeRepo) ActiveTelegramSession(int64) (*db.TelegramSession, error) {
	return f.session, nil
}

func (f *fakeRepo) StartTelegramSession(chatID int64, title string) (*db.TelegramSession, error) {
	return &db.TelegramSession{ID: 1, ChatID: chatID, Title: title}, nil
}

func (f *fakeRepo) ArchiveTelegramSession(int64) error { return nil }

func (f *fakeRepo) ListTelegramSessions(int64, int) ([]db.TelegramSession, error) {
	return nil, nil
}

func (f *fakeRepo) AppendTelegramMessage(sessionID int64, role, content string) error {
	if f.appended != nil {
		f.appended(role, content)
	}
	return nil
}

func (f *fakeRepo) TelegramTranscript(int64, int) ([]db.TelegramTranscriptMessage, error) {
	return nil, nil
}

func (f *fakeRepo) EnsureTelegramAPIKey() (string, error) { return "sk-fake", nil }
func (f *fakeRepo) DevRepoRoot() (string, error)          { return f.repoRoot, nil }
func (f *fakeRepo) DevFeatureName() (string, error)       { return f.feature, nil }
func (f *fakeRepo) DevInbox() (*notify.Spool, error)      { return notify.NewSpool(f.inboxPath), nil }
func (f *fakeRepo) DevSpool() (*notify.Spool, error)      { return notify.NewSpool(f.spoolPath), nil }

func (f *fakeRepo) GetTelegramChannelConfig() (*Config, error) {
	return &Config{
		Enabled:        f.token != "" && len(f.allowed) > 0,
		TokenPresent:   f.token != "",
		AllowedUserIDs: f.allowed,
	}, nil
}

// S1: with no token stored the channel does not start. The supervisor reports
// idle and never constructs a poll loop.
func TestSupervisor_DoesNotStartWithoutToken(t *testing.T) {
	repo := &fakeRepo{}
	s := NewSupervisor(repo, nil)

	s.Reconcile(context.Background())

	if got := s.Status(); got.Running {
		t.Errorf("Running = true with no token, want false")
	}
	if got := s.Status().State; got != StateDisabled {
		t.Errorf("State = %q, want %q", got, StateDisabled)
	}
}

// S3: a token with an empty allowlist is the fail-closed case. The loop must not
// start, because starting it would poll Telegram and consume updates it has no
// authorization to answer.
func TestSupervisor_DoesNotStartWithEmptyAllowlist(t *testing.T) {
	repo := &fakeRepo{token: "111:secret"}
	s := NewSupervisor(repo, nil)

	s.Reconcile(context.Background())

	if got := s.Status(); got.Running {
		t.Errorf("Running = true with an empty allowlist, want false")
	}
	if got := s.Status().State; got != StateNoAllowlist {
		t.Errorf("State = %q, want %q", got, StateNoAllowlist)
	}
}

// S9: Reconcile is idempotent. Two calls with the same config must not leave two
// loops running, which would deliver every update twice.
func TestSupervisor_ReconcileIsIdempotent(t *testing.T) {
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}}
	starts := make(chan struct{}, 4)
	s := NewSupervisor(repo, func(int64) bool { return true })
	s.runLoop = func(ctx context.Context, _ Repo) { starts <- struct{}{}; <-ctx.Done() }

	s.Reconcile(context.Background())
	// The loop runs in its own goroutine, so wait for it to report entry before
	// reconciling again: that is the state the second call must not duplicate.
	select {
	case <-starts:
	case <-time.After(2 * time.Second):
		t.Fatalf("loop did not start")
	}
	s.Reconcile(context.Background())

	select {
	case <-starts:
		t.Errorf("a second loop started: Reconcile is not idempotent")
	case <-time.After(50 * time.Millisecond):
	}
	if !s.Status().Running {
		t.Errorf("Running = false, want true")
	}
}

// S1: clearing the token stops a running loop. The channel returns to disabled
// and the context handed to the loop is cancelled.
func TestSupervisor_StoppingClearsRunningState(t *testing.T) {
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}}
	cancelled := make(chan struct{})
	s := NewSupervisor(repo, nil)
	s.runLoop = func(ctx context.Context, _ Repo) {
		<-ctx.Done()
		close(cancelled)
	}

	s.Reconcile(context.Background())
	if !s.Status().Running {
		t.Fatalf("Running = false after a start, want true")
	}

	s.Stop(context.Background())

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatalf("loop context was not cancelled by Stop()")
	}
	if s.Status().Running {
		t.Errorf("Running = true after Stop(), want false")
	}
	if got := s.Status().State; got != StateDisabled {
		t.Errorf("State = %q, want %q", got, StateDisabled)
	}
}

// S3: the authorizer is the per-message gate. An empty allowlist admits nobody
// and a populated one admits exactly its members.
func TestSupervisor_Allowed(t *testing.T) {
	repo := &fakeRepo{token: "111:secret", allowed: []int64{7, 9001}}
	s := NewSupervisor(repo, nil)

	if s.Allowed(7) != true {
		t.Errorf("Allowed(7) = false, want true: 7 is on the list")
	}
	if s.Allowed(8) != false {
		t.Errorf("Allowed(8) = true, want false: 8 is not on the list")
	}

	empty := NewSupervisor(&fakeRepo{}, nil)
	if empty.Allowed(7) != false {
		t.Errorf("Allowed(7) = true with an empty allowlist, want false")
	}
}

// S9: no goroutine outlives the supervisor. After Stop, the loop it started has
// fully returned, so a gateway shutdown does not leave a poller behind.
func TestSupervisor_StopWaitsForLoopExit(t *testing.T) {
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}}
	exited := make(chan struct{})
	s := NewSupervisor(repo, nil)
	s.runLoop = func(ctx context.Context, _ Repo) {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // simulate in-flight bookkeeping
		close(exited)
	}

	s.Reconcile(context.Background())
	s.Stop(context.Background())

	select {
	case <-exited:
	default:
		t.Errorf("Stop() returned before the loop finished exiting")
	}
}
