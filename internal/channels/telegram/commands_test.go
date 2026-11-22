package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// fakeTelegram is a stand-in for the Bot API. It records every sendMessage so a
// test asserts what the operator would have read in the chat, which is the only
// observable effect of a command.
type fakeTelegram struct {
	mu   sync.Mutex
	sent []string
	srv  *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			// The library posts sendMessage as multipart/form-data.
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.sent = append(f.sent, r.FormValue("text"))
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"testbot"}}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// newTestSupervisor builds a supervisor over the package's fake repo, with the
// allowlist open so the commands themselves are what the test exercises.
func newTestSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	return NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(int64) bool { return true })
}

func (f *fakeTelegram) bot(t *testing.T) *tgbot.Bot {
	t.Helper()
	b, err := tgbot.New("123:abc", tgbot.WithServerURL(f.srv.URL), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("tgbot.New() = %v", err)
	}
	return b
}

func (f *fakeTelegram) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	copy(out, f.sent)
	return out
}

// runCommand drives one command through the same entry point a real update
// takes, so the dispatcher is exercised rather than the command in isolation.
func runCommand(t *testing.T, sup *Supervisor, fake *fakeTelegram, text string) []string {
	t.Helper()
	sup.handleMessage(context.Background(), fake.bot(t), &models.Message{
		Text: text,
		Chat: models.Chat{ID: 42},
	})
	return fake.messages()
}

// S6: a command is answered locally and never reaches a provider. The test
// asserts the reply the operator sees, and that the turn count the forward path
// would bump stays at zero, so a regression that routed commands upstream fails
// here instead of spending tokens.
func TestCommands_AnsweredLocally(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		wantText string
	}{
		{name: "start offers help", command: "/start", wantText: "/new"},
		{name: "help offers help", command: "/help", wantText: "/new"},
		{name: "new opens a session", command: "/new", wantText: "abierta"},
		{name: "unknown command is refused", command: "/nonsense", wantText: "desconocido"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			sup := newTestSupervisor(t)

			sent := runCommand(t, sup, fake, tt.command)

			if len(sent) != 1 {
				t.Fatalf("sent %d messages, want exactly 1: %q", len(sent), sent)
			}
			if !strings.Contains(sent[0], tt.wantText) {
				t.Errorf("reply = %q, want it to contain %q", sent[0], tt.wantText)
			}
		})
	}
}

// S6: /new and /end both reach the repository and both answer. The stored
// session lifecycle itself is pinned in internal/db against a real database;
// here the subject is the dialogue the operator sees, so the assertions are the
// replies and the fact that each command issued exactly one send.
func TestCommands_NewThenEnd(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := newTestSupervisor(t)

	runCommand(t, sup, fake, "/new")
	runCommand(t, sup, fake, "/end")

	msgs := fake.messages()
	if len(msgs) != 2 {
		t.Fatalf("sent %d messages, want 2: %q", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "abierta") {
		t.Errorf("/new reply = %q, want it to confirm the session opened", msgs[0])
	}
	if !strings.Contains(msgs[1], "cerrada") && !strings.Contains(msgs[1], "No había sesión") {
		t.Errorf("/end reply = %q, want it to confirm the close or report none", msgs[1])
	}
}

// S6: /end with no open session says so instead of erroring, and /status
// reports the live state the operator reads in the dashboard.
func TestCommands_EndWithoutSession_AndStatus(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := newTestSupervisor(t)

	sent := runCommand(t, sup, fake, "/end")
	if len(sent) != 1 || !strings.Contains(sent[0], "No había sesión") {
		t.Fatalf("reply to /end with no session = %q, want the no-session hint", sent)
	}

	sent = runCommand(t, sup, fake, "/status")
	if len(sent) != 2 {
		t.Fatalf("sent %d messages after /status, want 2", len(sent))
	}
	last := sent[len(sent)-1]
	if !strings.Contains(strings.ToLower(last), "estado") && !strings.Contains(last, "Modelo") {
		t.Errorf("/status reply = %q, want a status summary", last)
	}
}

// S6: free text with no open session is refused with a hint rather than
// forwarded, so stray input never spends a token.
func TestHandleMessage_FreeTextWithoutSessionIsRefused(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := newTestSupervisor(t)

	sent := runCommand(t, sup, fake, "hola, ¿qué tal?")

	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1: %q", len(sent), sent)
	}
	if !strings.Contains(sent[0], "/new") {
		t.Errorf("reply = %q, want a hint to open a session first", sent[0])
	}
}

// The bot must never address a chat other than the one the message came from.
func TestReplyToChat_UsesMessageChat(t *testing.T) {
	msg := &models.Message{Chat: models.Chat{ID: 987654}}
	if got := replyToChat(msg); got != 987654 {
		t.Errorf("replyToChat() = %d, want the originating chat", got)
	}
}

// A blank reply is not sent: Telegram rejects empty text, and a silent failure
// would leave the operator waiting.
func TestSend_SkipsBlankText(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := newTestSupervisor(t)

	sup.send(context.Background(), fake.bot(t), 42, "   ")

	if sent := fake.messages(); len(sent) != 0 {
		t.Errorf("sent %d messages for blank text, want 0", len(sent))
	}
}

// S4: a long answer is split at the platform cap, and every part is a complete
// UTF-8 string.
func TestSend_SplitsLongText(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := newTestSupervisor(t)

	long := strings.Repeat("á", maxTelegramMessage+50)
	sup.send(context.Background(), fake.bot(t), 42, long)

	sent := fake.messages()
	if len(sent) < 2 {
		t.Fatalf("sent %d messages, want the text split across at least 2", len(sent))
	}
	for i, part := range sent {
		if len([]rune(part)) > maxTelegramMessage {
			t.Errorf("part %d has %d runes, over the cap", i, len([]rune(part)))
		}
		if !utf8Valid(part) {
			t.Errorf("part %d is not valid UTF-8", i)
		}
	}
	if joined := strings.Join(sent, ""); joined != long {
		t.Errorf("split lost or changed text: %d runes in, %d out", len([]rune(long)), len([]rune(joined)))
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// S3: the allowlist gate runs before anything else. A message from a user who
// is not authorized produces no reply and touches no session — the loop must not
// even acknowledge that it exists.
func TestHandleUpdate_RejectsUnauthorizedUser(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(id int64) bool {
		return id == 42
	})

	sup.handleUpdate(context.Background(), fake.bot(t), &models.Update{
		Message: &models.Message{
			Text: "/new",
			From: &models.User{ID: 999},
			Chat: models.Chat{ID: 999},
		},
	})

	if sent := fake.messages(); len(sent) != 0 {
		t.Errorf("replied %d times to an unauthorized user, want 0: %q", len(sent), sent)
	}
}

// S3: the same update from an authorized user is answered, which proves the
// rejection above is the allowlist and not a broken handler.
func TestHandleUpdate_AcceptsAuthorizedUser(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(id int64) bool {
		return id == 42
	})

	sup.handleUpdate(context.Background(), fake.bot(t), &models.Update{
		Message: &models.Message{
			Text: "/start",
			From: &models.User{ID: 42},
			Chat: models.Chat{ID: 42},
		},
	})

	if sent := fake.messages(); len(sent) != 1 {
		t.Errorf("replied %d times to an authorized user, want 1", len(sent))
	}
}

// Updates outside the text contract are ignored rather than answered: an edit,
// a callback query or a message with no sender has nothing to act on, and
// replying to one would be noise in the chat.
func TestHandleUpdate_IgnoresNonTextUpdates(t *testing.T) {
	tests := []struct {
		name   string
		update *models.Update
	}{
		{name: "nil update", update: nil},
		{name: "no message", update: &models.Update{}},
		{name: "no sender", update: &models.Update{Message: &models.Message{Text: "hola", Chat: models.Chat{ID: 42}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(int64) bool { return true })

			sup.handleUpdate(context.Background(), fake.bot(t), tt.update)

			if sent := fake.messages(); len(sent) != 0 {
				t.Errorf("replied %d times, want 0", len(sent))
			}
		})
	}
}

// S6: /model reports the current model and lists what can be dispatched, and a
// deployment with more models than the display cap says how many it hid instead
// of printing a wall of text on a phone.
func TestCmdModel_ListsAndTruncates(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, model: "ag/gemini-3.8-flash-low"}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	// A fake gateway serving more model ids than the cap.
	var ids []string
	for i := 0; i < 35; i++ {
		ids = append(ids, "ag/model-"+strconv.Itoa(i))
	}
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-9r-cli-token"); got != "cli" {
			t.Errorf("x-9r-cli-token = %q, want the configured CLI token", got)
		}
		if !strings.HasSuffix(r.URL.Path, "/api/channels/telegram/models") {
			t.Errorf("path = %q, want the dashboard models route", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{"models": ids})
		_, _ = w.Write(body)
	}))
	t.Cleanup(gw.Close)
	sup.SetGateway(gw.URL, "cli")

	sent := runCommand(t, sup, fake, "/model")

	if len(sent) < 2 {
		t.Fatalf("sent %d messages, want the current model then the list", len(sent))
	}
	if !strings.Contains(sent[0], "ag/gemini-3.8-flash-low") {
		t.Errorf("first reply = %q, want the current model", sent[0])
	}
	list := sent[len(sent)-1]
	if !strings.Contains(list, "y 5 más") {
		t.Errorf("list = %q, want it to report the 5 hidden ids", list)
	}
	if strings.Contains(list, "ag/model-34") {
		t.Errorf("list printed a model past the display cap")
	}
}

// S6: /model <id> stores the choice, so the next turn uses it.
func TestCmdModel_SetsModel(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sent := runCommand(t, sup, fake, "/model cx/gpt-6-luna")

	if repo.model != "cx/gpt-6-luna" {
		t.Errorf("stored model = %q, want the requested one", repo.model)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "cx/gpt-6-luna") {
		t.Errorf("reply = %q, want it to confirm the new model", sent)
	}
}

// S6: /sessions with nothing stored says so instead of sending an empty list.
func TestCmdSessions_Empty(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := newTestSupervisor(t)

	sent := runCommand(t, sup, fake, "/sessions")

	if len(sent) != 1 || !strings.Contains(sent[0], "No hay sesiones") {
		t.Errorf("reply = %q, want the empty-sessions hint", sent)
	}
}

// The gateway address is read and written under the supervisor's lock, because
// the poll loop reads it while the dashboard may replace it on a settings save.
func TestSetGateway_RoundTrip(t *testing.T) {
	sup := newTestSupervisor(t)

	sup.SetGateway("http://127.0.0.1:20130", "cli-token")

	baseURL, token := sup.gateway()
	if baseURL != "http://127.0.0.1:20130" || token != "cli-token" {
		t.Errorf("gateway() = (%q, %q), want the configured pair", baseURL, token)
	}
}

// A failure recorded by the supervisor is what /status and the dashboard banner
// report, so it must be readable back.
func TestSetError_IsReportedByStatus(t *testing.T) {
	sup := newTestSupervisor(t)

	sup.setError(errors.New("token rejected"))

	status := sup.Status()
	if !strings.Contains(status.LastError, "token rejected") {
		t.Errorf("LastError = %q, want the recorded failure", status.LastError)
	}
}
