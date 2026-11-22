// Package telegram runs the optional Telegram transport for one NOX9GO
// instance.
//
// The design is deliberately a transport adapter, not a gateway. Every message
// is forwarded to the instance's own /v1/chat/completions route, so routing,
// combos, failover, quota accounting and provider credentials all stay in the
// code that already owns them. This package only owns: the bot token, the
// authorization gate, the poll loop, and conversation sessions.
//
// One operator, one bot. There is no per-client key vault: a NOX9GO install
// runs on the operator's own machine, so the token belongs to the install, not
// to a user account.
package telegram

import (
	"context"
	"fmt"
	"sync"

	"nox9go/proxy/internal/notify"
	"time"

	"nox9go/proxy/internal/db"
)

// State is the channel's externally visible condition, reported to the
// dashboard so an operator can tell "off" from "broken".
type State string

const (
	// StateDisabled means no bot token is stored. The channel is off.
	StateDisabled State = "disabled"
	// StateNoAllowlist means a token is stored but nobody is authorized. The
	// loop does not start: polling would consume updates it may not answer.
	StateNoAllowlist State = "no_allowlist"
	// StateRunning means the poll loop is active.
	StateRunning State = "running"
	// StateError means the loop could not start, typically a rejected token.
	StateError State = "error"
)

// Config is the channel configuration the supervisor reconciles against. It
// mirrors the storage read model without leaking the token.
type Config struct {
	Enabled        bool    `json:"enabled"`
	TokenPresent   bool    `json:"tokenPresent"`
	TokenMasked    string  `json:"tokenMasked,omitempty"`
	AllowedUserIDs []int64 `json:"allowedUserIds"`
	// BotUsername is filled once the token has been accepted.
	BotUsername string `json:"botUsername,omitempty"`
	State       State  `json:"state"`
	LastError   string `json:"lastError,omitempty"`
}

// Repo is the storage surface the channel needs. It is an interface so the
// lifecycle and gate logic stay testable without a database.
type Repo interface {
	TelegramToken() (string, error)
	GetTelegramChannelConfig() (*Config, error)

	// Sessions. A chat with no active session is idle: stray input is answered
	// with a hint and never reaches a provider.
	ActiveTelegramSession(chatID int64) (*db.TelegramSession, error)
	StartTelegramSession(chatID int64, title string) (*db.TelegramSession, error)
	ArchiveTelegramSession(sessionID int64) error
	ListTelegramSessions(chatID int64, limit int) ([]db.TelegramSession, error)
	AppendTelegramMessage(sessionID int64, role, content string) error
	TelegramTranscript(sessionID int64, limit int) ([]db.TelegramTranscriptMessage, error)

	// The model chosen with /model.
	TelegramChannelModel() (string, error)
	SetTelegramChannelModel(model string) error

	// The channel's own client API key, issued on first use. A turn carries it
	// as Bearer because the engine's chat route guards itself with RequireApiKey,
	// which the CLI token does not satisfy.
	EnsureTelegramAPIKey() (string, error)

	// The repository the development report reads. Both are configuration, not
	// storage: an operator points the channel at a checkout with the
	// environment, and an empty root means the report is unavailable rather
	// than guessing a directory.
	DevRepoRoot() (string, error)
	DevFeatureName() (string, error)

	// DevSpool is the development event feed the channel forwards. A spool with
	// an empty path means no feed is configured.
	DevSpool() (*notify.Spool, error)

	// DevInbox is the one the operator writes to: /pi submits an instruction
	// for the development session to act on. Same append/drain model as the
	// feed, so one event is delivered exactly once.
	DevInbox() (*notify.Spool, error)
}

// Status is the supervisor's snapshot.
type Status struct {
	State       State     `json:"state"`
	Running     bool      `json:"running"`
	BotUsername string    `json:"botUsername,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
}

// Supervisor owns exactly one poll loop. Reconcile is the only way to start or
// stop it, which keeps "is it running" a single decision instead of a race
// between a start path and a stop path.
type Supervisor struct {
	repo    Repo
	allowed func(int64) bool

	// gatewayURL is this instance's own listener, and cliToken authenticates the
	// bot to it. A Telegram turn therefore travels the same route as an API
	// client: combos, failover and usage accounting all apply.
	gatewayURL string
	cliToken   string

	// runLoop is the loop body. It is a field so tests can observe start and
	// exit without talking to Telegram.
	runLoop func(ctx context.Context, repo Repo)

	mu     sync.Mutex
	cancel context.CancelFunc

	// inFlight holds the cancel function of the turn each chat currently has
	// running (S7). A chat has at most one: a second message while one is in
	// flight is handled after the first returns, not concurrently.
	inFlight map[int64]context.CancelFunc
	done     chan struct{}
	status   Status
	username string
}

// NewSupervisor builds a supervisor for repo. allowed, when non-nil, overrides
// the repo-backed allowlist check; production passes nil and the gate reads the
// stored list.
func NewSupervisor(repo Repo, allowed func(int64) bool) *Supervisor {
	s := &Supervisor{
		repo:     repo,
		allowed:  allowed,
		status:   Status{State: StateDisabled},
		inFlight: make(map[int64]context.CancelFunc),
	}
	s.runLoop = s.poll
	return s
}

// SetGateway tells the supervisor where this instance listens and how to
// authenticate to itself. It is separate from NewSupervisor because the listener
// address is only known after the server is bound.
func (s *Supervisor) SetGateway(baseURL, cliToken string) {
	s.mu.Lock()
	s.gatewayURL = baseURL
	s.cliToken = cliToken
	s.mu.Unlock()
}

// gateway returns the configured self-URL and token.
func (s *Supervisor) gateway() (baseURL, cliToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gatewayURL, s.cliToken
}

// Allowed reports whether a Telegram user may use the bot. It reads the stored
// allowlist on every call rather than caching it, so revoking a user takes
// effect on their next message instead of on the next restart.
//
// An empty allowlist admits nobody. This is the fail-closed contract: a fresh
// install and a deliberately emptied list both answer no one.
func (s *Supervisor) Allowed(userID int64) bool {
	if s.allowed != nil {
		return s.allowed(userID)
	}
	cfg, err := s.repo.GetTelegramChannelConfig()
	if err != nil || cfg == nil {
		return false
	}
	for _, id := range cfg.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// Status returns a snapshot of the current state.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	out.BotUsername = s.username
	return out
}

// Reconcile brings the running loop in line with the stored configuration. It is
// idempotent: calling it with an unchanged configuration leaves exactly one loop
// running, so an operator saving an unrelated setting cannot cause a second poller
// that would deliver every update twice.
func (s *Supervisor) Reconcile(ctx context.Context) {
	cfg, err := s.repo.GetTelegramChannelConfig()
	if err != nil {
		s.setError(fmt.Errorf("read channel config: %w", err))
		return
	}

	token, err := s.repo.TelegramToken()
	if err != nil {
		s.setError(err)
		return
	}

	switch {
	case token == "":
		s.Stop(ctx)
	case len(cfg.AllowedUserIDs) == 0:
		// Stop first: a token that was just stored without an allowlist must
		// not leave a previously running loop polling for nobody.
		s.stopLoop()
		s.mu.Lock()
		s.status = Status{State: StateNoAllowlist}
		s.mu.Unlock()
	default:
		s.startLoop(token)
	}
}

// Stop halts the poll loop and waits for it to exit, so no goroutine outlives a
// gateway shutdown.
func (s *Supervisor) Stop(ctx context.Context) {
	s.stopLoop()
	s.mu.Lock()
	s.status = Status{State: StateDisabled}
	s.username = ""
	s.mu.Unlock()
}

// startLoop starts the poll loop when none is running. An already-running loop
// is left alone, which is what makes Reconcile idempotent.
func (s *Supervisor) startLoop(token string) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	s.status = Status{State: StateRunning, Running: true, StartedAt: time.Now()}
	loop := s.runLoop
	s.mu.Unlock()

	go func() {
		defer close(done)
		loop(ctx, s.repo)
	}()
}

// stopLoop cancels the loop and waits for it to finish before returning.
func (s *Supervisor) stopLoop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

// beginTurn registers a chat's in-flight turn so /cancel can abort it (S7).
func (s *Supervisor) beginTurn(chatID int64, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight[chatID] = cancel
}

// endTurn removes the registration. It runs on every completed turn, so it
// tolerates an entry a cancel already removed.
func (s *Supervisor) endTurn(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, chatID)
}

// turnInFlight reports whether the chat has a turn running.
func (s *Supervisor) turnInFlight(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.inFlight[chatID]
	return ok
}

// cancelTurn aborts the chat's in-flight turn through its context and clears
// the registration. The bool separates an idle cancel from a successful one:
// an operator must be able to tell "cancelled" from "the button did nothing".
func (s *Supervisor) cancelTurn(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cancel, ok := s.inFlight[chatID]
	if ok {
		delete(s.inFlight, chatID)
		cancel()
	}
	return ok
}

// setError records a failure the operator needs to see, in place of the state
// that was running before it.
func (s *Supervisor) setError(err error) {
	s.mu.Lock()
	s.status = Status{State: StateError, LastError: err.Error()}
	s.username = ""
	s.mu.Unlock()
}
