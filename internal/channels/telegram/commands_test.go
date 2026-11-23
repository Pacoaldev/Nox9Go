package telegram

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/notify"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// fakeTelegram is a stand-in for the Bot API. It records every sendMessage so a
// test asserts what the operator would have read in the chat, which is the only
// observable effect of a command.
type fakeTelegram struct {
	mu      sync.Mutex
	sent    []string
	markups []string
	modes   []string
	edits   []string
	srv     *httptest.Server
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
			f.markups = append(f.markups, r.FormValue("reply_markup"))
			f.modes = append(f.modes, r.FormValue("parse_mode"))
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`))
		case strings.HasSuffix(r.URL.Path, "/editMessageText"):
			// The panel is edited in place, so the edit is the observable proof
			// that a redraw reached the message the operator is reading.
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.edits = append(f.edits, r.FormValue("text"))
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`))
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"photo-large","file_unique_id":"u","file_path":"photos/file_1.jpg"}}`))
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

func (f *fakeTelegram) parseModes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.modes...)
}

func (f *fakeTelegram) replyMarkups() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.markups...)
}

func (f *fakeTelegram) srvURL() string { return f.srv.URL }

func (f *fakeTelegram) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	copy(out, f.sent)
	return out
}

// editedMessages returns the text of every in-place edit the bot made.
func (f *fakeTelegram) editedMessages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.edits))
	copy(out, f.edits)
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
		name         string
		command      string
		wantText     string
		wantMessages int
	}{
		// /start is a fresh chat's first message: help, panel and the dock, so the
		// actions are one tap away from the first contact (S1/S3).
		{name: "start offers help, the panel and the dock", command: "/start", wantText: "/new", wantMessages: 3},
		{name: "help offers help", command: "/help", wantText: "/new", wantMessages: 1},
		{name: "new opens a session", command: "/new", wantText: "abierta", wantMessages: 1},
		{name: "unknown command is refused", command: "/nonsense", wantText: "desconocido", wantMessages: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			sup := newTestSupervisor(t)

			sent := runCommand(t, sup, fake, tt.command)

			// /start is a fresh chat's first message, so it answers with the help
			// and then opens the mode panel; /help is a lookup and answers once.
			if len(sent) != tt.wantMessages {
				t.Fatalf("sent %d messages, want %d: %q", len(sent), tt.wantMessages, sent)
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

	// /start answers with the help, the mode panel and then the dock, so an
	// authorized first message produces all three; the point here is that it is
	// answered at all.
	if sent := fake.messages(); len(sent) != 3 {
		t.Errorf("replied %d times to an authorized user, want 3", len(sent))
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

// S3: /estado reports the real repository state to an authorized operator.
func TestCmdDevStatus_ReportsRepository(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")
	writeAt(t, dir, "a.txt", "one")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "feat: something landed")

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, repoRoot: dir}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sent := runCommand(t, sup, fake, "/estado")

	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1: %q", len(sent), sent)
	}
	if !strings.Contains(sent[0], "feat: something landed") {
		t.Errorf("reply = %q, want the last commit subject", sent[0])
	}
}

// S3: with no checkout configured the report says so instead of reading the
// gateway's own working directory, which would be an unrelated repository.
func TestCmdDevStatus_Unconfigured(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(int64) bool { return true })

	sent := runCommand(t, sup, fake, "/estado")

	if len(sent) != 1 || !strings.Contains(sent[0], "DEV_REPO_ROOT") {
		t.Errorf("reply = %q, want it to name the variable that enables the report", sent)
	}
}

// S3: /estado with no argument follows the configured feature, and with an
// argument follows the one named in the message.
func TestCmdDevStatus_FeatureSelection(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")
	writeAt(t, dir, "odd/tasks/alpha.md", "# Alpha\n\n## Tasks\n\n| ID | Specs | Route | Commit |\n| :- | :---- | :---- | :----- |\n| T1 | S1 | first | abc123 |\n")
	writeAt(t, dir, "odd/tasks/beta.md", "# Beta\n\n## Tasks\n\n| ID | Specs | Route | Commit |\n| :- | :---- | :---- | :----- |\n| T1 | S1 | first | — |\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "docs: features")

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "argument wins", command: "/estado beta", want: "<b>beta</b>"},
		{name: "configured default", command: "/estado", want: "<b>alpha</b>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, repoRoot: dir, feature: "alpha"}
			sup := NewSupervisor(repo, func(int64) bool { return true })

			sent := runCommand(t, sup, fake, tt.command)

			if len(sent) != 1 {
				t.Fatalf("sent %d messages, want 1", len(sent))
			}
			if !strings.Contains(sent[0], tt.want) {
				t.Errorf("reply = %q, want it to contain %q", sent[0], tt.want)
			}
		})
	}
}

// The development report is behind the same allowlist as every other command:
// an unauthorized user learns nothing about the repository.
func TestCmdDevStatus_RefusedToStrangers(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, repoRoot: t.TempDir()}
	sup := NewSupervisor(repo, func(id int64) bool { return id == 42 })

	sup.handleUpdate(context.Background(), fake.bot(t), &models.Update{
		Message: &models.Message{
			Text: "/estado",
			From: &models.User{ID: 999},
			Chat: models.Chat{ID: 999},
		},
	})

	if sent := fake.messages(); len(sent) != 0 {
		t.Errorf("replied %d times to an unauthorized user, want 0", len(sent))
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeAt(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// S4: a development event reaches the allowed chat once, with the text the
// operator reads.
func TestForwardPendingEvents_DeliversToAllowedChat(t *testing.T) {
	spool := notify.NewSpool(filepath.Join(t.TempDir(), "events.jsonl"))
	if err := spool.Append(notify.Event{Kind: notify.KindCommit, Title: "abc123 feat: landed"}); err != nil {
		t.Fatalf("Append() = %v", err)
	}

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sup.forwardPendingEvents(context.Background(), fake.bot(t), repo, spool)

	sent := fake.messages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1: %q", len(sent), sent)
	}
	if !strings.Contains(sent[0], "abc123 feat: landed") {
		t.Errorf("message = %q, want the event title", sent[0])
	}

	// Draining again must not repeat it.
	sup.forwardPendingEvents(context.Background(), fake.bot(t), repo, spool)
	if again := fake.messages(); len(again) != 1 {
		t.Errorf("event forwarded twice: %d messages", len(again))
	}
}

// S4: an empty allowlist blocks delivery even when an event is waiting. The
// poll loop already refuses to start in that state; this guard means a config
// change mid-flight cannot leak a report.
func TestForwardPendingEvents_EmptyAllowlistDeliversNothing(t *testing.T) {
	spool := notify.NewSpool(filepath.Join(t.TempDir(), "events.jsonl"))
	if err := spool.Append(notify.Event{Title: "should not arrive"}); err != nil {
		t.Fatalf("Append() = %v", err)
	}

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret"}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sup.forwardPendingEvents(context.Background(), fake.bot(t), repo, spool)

	if sent := fake.messages(); len(sent) != 0 {
		t.Errorf("delivered %d messages with no allowlist, want 0", len(sent))
	}
}

// S4: an empty spool sends nothing, so a quiet period is silence rather than a
// heartbeat the operator has to scroll past.
func TestForwardPendingEvents_EmptySpoolIsSilent(t *testing.T) {
	spool := notify.NewSpool(filepath.Join(t.TempDir(), "events.jsonl"))
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sup.forwardPendingEvents(context.Background(), fake.bot(t), repo, spool)

	if sent := fake.messages(); len(sent) != 0 {
		t.Errorf("sent %d messages for an empty spool, want 0", len(sent))
	}
}

// S4: every allowed chat gets the event, because an operator may have more than
// one device paired with the bot.
func TestForwardPendingEvents_AllAllowedChats(t *testing.T) {
	spool := notify.NewSpool(filepath.Join(t.TempDir(), "events.jsonl"))
	_ = spool.Append(notify.Event{Title: "broadcast"})

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42, 99}}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sup.forwardPendingEvents(context.Background(), fake.bot(t), repo, spool)

	if sent := fake.messages(); len(sent) != 2 {
		t.Errorf("sent %d messages, want one per allowed chat (2)", len(sent))
	}
}

// S7: a turn in flight can be cancelled. The cancel must cut the upstream
// request through its context, and the aborted turn must not send a completion
// or reach the transcript.
func TestCmdCancel(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "the /cancel command", text: "/cancel"},
		{name: "the keyboard button label", text: "Cancelar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(int64) bool { return true })

			// No turn in flight: the cancel must say so instead of pretending.
			sent := runCommand(t, sup, fake, "/cancel")
			if len(sent) != 1 || !strings.Contains(sent[0], "nada en vuelo") {
				t.Fatalf("idle cancel reply = %q, want an explicit nothing-in-flight", sent)
			}

			// A turn in flight: register one whose context we can observe.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sup.beginTurn(42, cancel)
			if !sup.turnInFlight(42) {
				t.Fatalf("turnInFlight = false right after beginTurn")
			}

			_ = runCommand(t, sup, fake, tt.text)

			// The observable effect of a cancel is the context being done: a
			// test that only read a boolean would pass even if the abort never
			// reached the request.
			select {
			case <-ctx.Done():
			default:
				t.Errorf("the in-flight context is still alive after %q", tt.text)
			}

			// The slot is released, so a second cancel is an idle cancel.
			sent = runCommand(t, sup, fake, "/cancel")
			if len(sent) == 1 && strings.Contains(sent[0], "Cancelando") {
				t.Errorf("cancel succeeded twice for one in-flight turn")
			}
		})
	}
}

// The registry is keyed by chat, so one chat cannot cancel another's turn.
func TestBeginTurn_IsolatedPerChat(t *testing.T) {
	sup := NewSupervisor(&fakeRepo{token: "111:secret"}, func(int64) bool { return true })

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.beginTurn(42, cancel)

	if sup.turnInFlight(99) {
		t.Errorf("chat 99 sees a turn it never started")
	}
}

// endTurn removes the entry, which is what happens on every normal completion.
func TestEndTurn(t *testing.T) {
	sup := NewSupervisor(&fakeRepo{token: "111:secret"}, func(int64) bool { return true })
	sup.beginTurn(42, func() {})
	sup.endTurn(42)

	if sup.turnInFlight(42) {
		t.Errorf("turn still in flight after endTurn")
	}
}

// S7: while a turn is in flight the bot shows a cancel control. The
// acknowledgment the turn starts with carries the "Cancelar" reply keyboard,
// which is the control an operator sees on the phone.
func TestTurnStarts_WithVisibleCancelControl(t *testing.T) {
	// A slow upstream keeps the turn in flight long enough for the assertions
	// to run against a real in-flight registration.
	block := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer upstream.Close()
	defer close(block)

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, model: "test-model"}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	sup.SetGateway(upstream.URL, "sk-test")

	repo.session = &db.TelegramSession{ID: 1, ChatID: 42}

	done := make(chan struct{})
	go func() {
		defer close(done)
		sup.handleUpdate(context.Background(), fake.bot(t), &models.Update{
			Message: &models.Message{
				Text: "hola",
				From: &models.User{ID: 42},
				Chat: models.Chat{ID: 42},
			},
		})
	}()

	// The turn registers before the ack is sent, so the wait is for the visible
	// control rather than for a race the test would lose half the time.
	deadline := time.After(3 * time.Second)
	var sawControl bool
	for !sawControl {
		select {
		case <-deadline:
			t.Fatalf("the cancel control never appeared: messages=%v markups=%v", fake.messages(), fake.replyMarkups())
		case <-time.After(50 * time.Millisecond):
			for _, m := range fake.replyMarkups() {
				// The bot library base64-encodes a multipart form field that
				// carries non-ASCII text, so the markup is decoded before it is
				// searched; matching the raw string would be testing the
				// encoding instead of the keyboard.
				if decoded, err := base64.StdEncoding.DecodeString(m); err == nil {
					m = string(decoded)
				}
				if strings.Contains(m, "Cancelar") {
					sawControl = true
				}
			}
		}
	}
	if !sup.turnInFlight(42) {
		t.Errorf("no turn registered while the upstream is still running")
	}

	// The finish path releases the slot so the next message is not stranded.
	sup.cancelTurn(42)
	<-done
}

// S7: a cancelled turn sends no completion and records no assistant row. The
// upstream never answers, the context aborts, and the transcript keeps only
// what the user asked.
func TestCancelledTurn_LeavesNoAssistantRow(t *testing.T) {
	// The upstream blocks until the test releases it. It must not block on
	// r.Context().Done(): the assertion below is precisely that the client
	// aborts, and a handler waiting on the very signal under test leaves both
	// the handler and the deferred Close() hanging when the abort never comes.
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, model: "test-model"}
	repo.session = &db.TelegramSession{ID: 7, ChatID: 42}
	var appended []string
	repo.appended = func(role, content string) { appended = append(appended, role) }
	sup := NewSupervisor(repo, func(int64) bool { return true })
	sup.SetGateway(upstream.URL, "sk-test")

	done := make(chan struct{})
	go func() {
		defer close(done)
		sup.handleUpdate(context.Background(), fake.bot(t), &models.Update{
			Message: &models.Message{
				Text: "pregunta larga",
				From: &models.User{ID: 42},
				Chat: models.Chat{ID: 42},
			},
		})
	}()

	// Wait for the turn to register, then cancel it the way /cancel does.
	deadline := time.After(3 * time.Second)
	for sup.turnInFlight(42) == false {
		select {
		case <-deadline:
			t.Fatalf("the turn never registered")
		case <-time.After(20 * time.Millisecond):
		}
	}

	sent := runCommand(t, sup, fake, "/cancel")
	// The turn's own ack is in this capture too, so the cancel confirmation is
	// the message the command added, not necessarily the only one sent.
	if len(sent) == 0 || !strings.Contains(sent[len(sent)-1], "Cancelando") {
		t.Fatalf("cancel reply = %q, want the cancelling confirmation last", sent)
	}
	<-done

	for _, role := range appended {
		if role == "assistant" {
			t.Errorf("a cancelled turn recorded an assistant row: %v", appended)
		}
	}
	// The user's message is still there, so the transcript is honest about
	// what was asked even though no answer came.
	if len(appended) == 0 || appended[0] != "user" {
		t.Errorf("the user message is missing from the transcript: %v", appended)
	}
}

// Regression: ReplyMarkup is an interface field, and sending it a
// pre-marshaled string made the wire carry a quoted JSON string, which the
// Bot API rejects with "can't parse reply keyboard markup JSON object". That
// error surfaced on every chat turn because the abort ack precedes the
// forward, so the operator could not chat at all. The keyboard must travel as
// a JSON object, and the ack must not be reported as an error when it succeeds.
func TestSendKeyboard_SendsAJSONObject(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(int64) bool { return true })

	sup.sendKeyboard(context.Background(), fake.bot(t), 42, "probando")

	markups := fake.replyMarkups()
	if len(markups) != 1 {
		t.Fatalf("sent %d markups, want 1", len(markups))
	}
	if decoded, err := base64.StdEncoding.DecodeString(markups[0]); err == nil {
		markups[0] = string(decoded)
	}
	if !strings.HasPrefix(markups[0], "{") {
		t.Errorf("reply_markup traveled as %q, want a JSON object — a quoted string is what the Bot API rejects", markups[0])
	}
	if !strings.Contains(markups[0], `"keyboard"`) || !strings.Contains(markups[0], "Menú") {
		t.Errorf("reply_markup = %q, want a hub keyboard JSON object", markups[0])
	}
	if len(fake.messages()) != 1 || fake.messages()[0] != "probando" {
		t.Errorf("messages = %v, want the ack", fake.messages())
	}
	if status := sup.Status(); status.LastError != "" {
		t.Errorf("a successful send must not record an error: %v", status.LastError)
	}
}

// /pi submits an instruction for the development session. The bot writes an
// `order` event and executes nothing itself; the session drains the inbox and
// acts. An empty instruction asks for the usage instead of submitting a blank
// line, and the path from a chat the allowlist does not know is closed by
// handleUpdate before any command runs.
func TestCmdPi(t *testing.T) {
	t.Run("appends the order to the inbox", func(t *testing.T) {
		fake := newFakeTelegram(t)
		inbox := filepath.Join(t.TempDir(), "inbox.jsonl")
		sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}, inboxPath: inbox}, func(int64) bool { return true })

		usingKeyboard(t, sup, fake, "/pi arregla el test que flaquea")

		data, err := os.ReadFile(inbox)
		if err != nil {
			t.Fatalf("the instruction never reached the inbox: %v", err)
		}
		var event notify.Event
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if event.Kind != notify.KindOrder {
			t.Errorf("event kind = %q, want order", event.Kind)
		}
		if event.Detail != "arregla el test que flaquea" {
			t.Errorf("event detail = %q, want the instruction verbatim", event.Detail)
		}
	})

	t.Run("an empty instruction asks for usage instead of submitting", func(t *testing.T) {
		fake := newFakeTelegram(t)
		inbox := filepath.Join(t.TempDir(), "inbox.jsonl")
		sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}, inboxPath: inbox}, func(int64) bool { return true })

		usingKeyboard(t, sup, fake, "/pi")

		if _, err := os.Stat(inbox); !os.IsNotExist(err) {
			t.Errorf("an empty instruction must not write to the inbox")
		}
	})
}

// usingKeyboard is a handleUpdate over a command text; runCommand in the other
// tests drives handleMessage through a helper whose shape changed with the
// keyboard path, so /pi is asserted through the same public entry point as a
// real phone message would use.
func usingKeyboard(t *testing.T, sup *Supervisor, fake *fakeTelegram, text string) {
	t.Helper()
	sup.handleUpdate(context.Background(), fake.bot(t), &models.Update{
		Message: &models.Message{
			Text: text,
			From: &models.User{ID: 42},
			Chat: models.Chat{ID: 42},
		},
	})
}

// The report is sent parsed as HTML, and a request that Telegram rejects falls
// back to readable plain text instead of leaving the operator with nothing.
func TestCmdDevStatus_SendsHTML(t *testing.T) {
	fake := newFakeTelegram(t)
	sup := NewSupervisor(&fakeRepo{token: "111:secret", allowed: []int64{42}}, func(int64) bool { return true })

	sup.sendHTML(context.Background(), fake.bot(t), 42, "<b>hola</b>")

	if modes := fake.parseModes(); len(modes) == 0 || modes[0] != "HTML" {
		t.Errorf("parse_mode = %v, want HTML", modes)
	}
}

// The HTML fallback path (send falls back to plain text when Telegram rejects
// the markup) and the bot-command menu registration are pure tables, covered
// here because the codecov/patch gate wants them and they cost nothing to pin.
func TestStripHTML_RemovesEmittedTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "bold", input: "<b>Estado: chat</b>", want: "Estado: chat"},
		{name: "code", input: "Modelo: <code>deepseek-chat</code>", want: "Modelo: deepseek-chat"},
		{name: "entities", input: "a &amp; b &lt; c &gt; d", want: "a & b < c > d"},
		{name: "plain passes through", input: "sin etiquetas", want: "sin etiquetas"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripHTML(tt.input); got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBotCommands_PanelFirstAndMatchesHelp(t *testing.T) {
	cmds := botCommands()
	if len(cmds) == 0 || cmds[0].Command != "panel" {
		t.Fatalf("botCommands() first command = %v, want /panel first", cmds)
	}
	help := helpText()
	for _, c := range cmds {
		if !strings.Contains(help, "/"+c.Command) {
			t.Errorf("menu command /%s is missing from helpText: %q", c.Command, help)
		}
	}
}
