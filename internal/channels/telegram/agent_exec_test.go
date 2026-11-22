package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/notify"
)

// pressAndRead presses one inline control as the authorized operator and
// returns everything the bot said in response, so an assertion covers both the
// effect and what the operator was told about it.
func pressAndRead(t *testing.T, sup *Supervisor, fake *fakeTelegram, data string) []string {
	t.Helper()
	pressCallback(t, sup, fake, 42, data)
	return fake.messages()
}

// readOrders decodes every order the development inbox received, so a test
// asserts what the session will actually drain instead of what the bot said.
func readOrders(t *testing.T, path string) []notify.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read inbox %q: %v", path, err)
	}
	var events []notify.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var event notify.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode inbox line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

// blockedInbox returns an inbox path whose parent is a regular file, so
// os.MkdirAll fails and Append reports the failure the way a broken spool on a
// real install would. The blocker's name is what the failure names.
func blockedInbox(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	return filepath.Join(blocker, "inbox.jsonl")
}

// joinedText is the reply the operator read, as one string.
func joinedText(messages []string) string { return strings.Join(messages, "\n") }

// S1/S3: approving a task hands its instruction to the development session
// through the spool /pi writes to, and only then does the task report running.
// Before this, approval flipped a column and nothing ever executed.
func TestApproveSubmitsOrderToDevInbox(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox.jsonl")
	repo := &fakeRepo{
		token:     "111:secret",
		allowed:   []int64{42},
		mode:      db.ModeAgent,
		inboxPath: inbox,
		task: db.TelegramAgentTask{
			ChatID:      42,
			Instruction: "desplegá la rama feat/telegram",
			Status:      string(TaskPendingApproval),
		},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	fake := newFakeTelegram(t)

	sent := pressAndRead(t, sup, fake, cbApprove)

	orders := readOrders(t, inbox)
	if len(orders) != 1 {
		t.Fatalf("inbox received %d orders, want exactly 1: %+v", len(orders), orders)
	}
	if orders[0].Kind != notify.KindOrder {
		t.Errorf("event kind = %q, want %q", orders[0].Kind, notify.KindOrder)
	}
	if orders[0].Title != "Orden desde Telegram" {
		t.Errorf("event title = %q, want the /pi title", orders[0].Title)
	}
	if orders[0].Detail != "desplegá la rama feat/telegram" {
		t.Errorf("event detail = %q, want the instruction verbatim", orders[0].Detail)
	}
	if repo.task.Status != string(TaskRunning) {
		t.Errorf("task status = %q, want running after an accepted order", repo.task.Status)
	}
	if !strings.Contains(joinedText(sent), "Tarea aprobada") {
		t.Errorf("operator was not told the task was approved: %v", sent)
	}
}

// S3: a submit the spool refuses must never leave the task claiming progress.
// The task is failed and the reason is stored and shown, so the operator knows
// nothing is executing.
func TestApproveFailedSubmitLeavesTaskFailed(t *testing.T) {
	tests := []struct {
		name     string
		repo     func(t *testing.T) *fakeRepo
		wantWord string
	}{
		{
			name: "append fails",
			repo: func(t *testing.T) *fakeRepo {
				return &fakeRepo{
					token:     "111:secret",
					allowed:   []int64{42},
					mode:      db.ModeAgent,
					inboxPath: blockedInbox(t),
					task: db.TelegramAgentTask{
						ChatID: 42, Instruction: "borrar los logs", Status: string(TaskPendingApproval),
					},
				}
			},
			wantWord: "blocker",
		},
		{
			name: "inbox cannot be opened",
			repo: func(t *testing.T) *fakeRepo {
				return &fakeRepo{
					token:    "111:secret",
					allowed:  []int64{42},
					mode:     db.ModeAgent,
					inboxErr: errors.New("bandeja rota"),
					task: db.TelegramAgentTask{
						ChatID: 42, Instruction: "borrar los logs", Status: string(TaskPendingApproval),
					},
				}
			},
			wantWord: "bandeja rota",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := tt.repo(t)
			sup := NewSupervisor(repo, func(int64) bool { return true })
			fake := newFakeTelegram(t)

			sent := pressAndRead(t, sup, fake, cbApprove)

			status, reason := parseTaskStatus(repo.task.Status)
			if status == TaskRunning {
				t.Fatalf("task status = %q after a refused submit, want it never running", repo.task.Status)
			}
			if status != TaskFailed {
				t.Fatalf("task status = %q, want %q", repo.task.Status, TaskFailed)
			}
			if !strings.Contains(reason, tt.wantWord) {
				t.Errorf("stored reason = %q, want it to name %q", reason, tt.wantWord)
			}
			reply := joinedText(sent)
			if !strings.Contains(reply, tt.wantWord) {
				t.Errorf("operator was not told the cause: %v", sent)
			}
			if !strings.Contains(reply, "fallida") {
				t.Errorf("operator was not told the task failed: %v", sent)
			}
		})
	}
}

// S2: auto-approve is the same flow with the waiting step removed — the text is
// recorded as a task and submitted as an order immediately.
func TestAutoApproveSubmitsOrder(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox.jsonl")
	repo := &fakeRepo{
		token:       "111:secret",
		allowed:     []int64{42},
		mode:        db.ModeAgent,
		autoApprove: true,
		inboxPath:   inbox,
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	fake := newFakeTelegram(t)

	sup.handleMessage(context.Background(), fake.bot(t), testMessage(42, "tarea directa"))

	orders := readOrders(t, inbox)
	if len(orders) != 1 {
		t.Fatalf("inbox received %d orders, want exactly 1: %+v", len(orders), orders)
	}
	if orders[0].Kind != notify.KindOrder || orders[0].Detail != "tarea directa" {
		t.Errorf("order = %+v, want an order carrying the instruction", orders[0])
	}
	if repo.task.Status != string(TaskRunning) {
		t.Errorf("task status = %q, want running under auto-approve", repo.task.Status)
	}
}

// S2/S3: an auto-approved task whose order never left the gateway is failed too.
// The two paths share the submission, so they share the honesty.
func TestAutoApproveFailedSubmitLeavesTaskFailed(t *testing.T) {
	tests := []struct {
		name     string
		repo     *fakeRepo
		wantWord string
	}{
		{
			name: "append fails",
			repo: &fakeRepo{
				token: "111:secret", allowed: []int64{42}, mode: db.ModeAgent,
				autoApprove: true, inboxPath: blockedInbox(t),
			},
			wantWord: "blocker",
		},
		{
			name: "inbox cannot be opened",
			repo: &fakeRepo{
				token: "111:secret", allowed: []int64{42}, mode: db.ModeAgent,
				autoApprove: true, inboxErr: errors.New("bandeja rota"),
			},
			wantWord: "bandeja rota",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sup := NewSupervisor(tt.repo, func(int64) bool { return true })
			fake := newFakeTelegram(t)

			sup.handleMessage(context.Background(), fake.bot(t), testMessage(42, "tarea directa"))

			status, reason := parseTaskStatus(tt.repo.task.Status)
			if status != TaskFailed {
				t.Fatalf("task status = %q, want %q", tt.repo.task.Status, TaskFailed)
			}
			if !strings.Contains(reason, tt.wantWord) {
				t.Errorf("stored reason = %q, want it to name %q", reason, tt.wantWord)
			}
			if !strings.Contains(joinedText(fake.messages()), tt.wantWord) {
				t.Errorf("operator was not told the cause: %v", fake.messages())
			}
		})
	}
}

// S4: cancelling a running task says plainly that the order may already be
// queued, because the bot cannot recall what the session has drained.
func TestCancelRunningTaskWarnsOrderMayBeQueued(t *testing.T) {
	repo := &fakeRepo{
		token:   "111:secret",
		allowed: []int64{42},
		mode:    db.ModeAgent,
		task: db.TelegramAgentTask{
			ChatID: 42, Instruction: "desplegar", Status: string(TaskRunning),
		},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	fake := newFakeTelegram(t)

	sent := pressAndRead(t, sup, fake, cbCancelTask)

	if repo.task.Status != string(TaskCancelled) {
		t.Errorf("task status = %q, want cancelled", repo.task.Status)
	}
	if !strings.Contains(joinedText(sent), "puede estar ya en la cola de la sesión de desarrollo") {
		t.Errorf("cancelling a running task hid the queued-order caveat: %v", sent)
	}
}

// S5: the panel and "Estado / Paso" report the stored status and the failure
// reason, and Refrescar re-reads them from storage.
func TestPanelShowsStoredFailureReason(t *testing.T) {
	repo := &fakeRepo{
		token:     "111:secret",
		allowed:   []int64{42},
		mode:      db.ModeAgent,
		inboxPath: blockedInbox(t),
		task: db.TelegramAgentTask{
			ChatID: 42, Instruction: "borrar los logs", Status: string(TaskPendingApproval),
		},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	fake := newFakeTelegram(t)

	// The panel message must exist for a refresh to edit it in place.
	sup.cmdPanel(context.Background(), fake.bot(t), 42)
	pressAndRead(t, sup, fake, cbApprove)
	pressAndRead(t, sup, fake, cbRefresh)

	_, reason := parseTaskStatus(repo.task.Status)
	if reason == "" {
		t.Fatalf("no reason was stored for the failed task: %q", repo.task.Status)
	}

	edits := fake.editedMessages()
	if len(edits) == 0 {
		t.Fatalf("Refrescar edited no panel: sent=%v", fake.messages())
	}
	last := edits[len(edits)-1]
	// The panel line is cut to one phone row, so what it must carry is the same
	// prefix the renderer shows.
	for _, want := range []string{"Fallida", truncate(reason, 160)} {
		if !strings.Contains(last, want) {
			t.Errorf("refreshed panel missing %q:\n%s", want, last)
		}
	}

	status := joinedText(pressAndRead(t, sup, fake, cbTaskStatus))
	for _, want := range []string{"Fallida", reason, "borrar los logs"} {
		if !strings.Contains(status, want) {
			t.Errorf("Estado / Paso missing %q:\n%s", want, status)
		}
	}
}

// S6: approving a task that is already running or finished is refused, and the
// refusal submits nothing, so a stale keyboard cannot queue a second order.
func TestApproveRefusesTerminalTask(t *testing.T) {
	for _, status := range []TaskStatus{TaskRunning, TaskDone, TaskCancelled, TaskRejected, TaskFailed} {
		t.Run(string(status), func(t *testing.T) {
			inbox := filepath.Join(t.TempDir(), "inbox.jsonl")
			repo := &fakeRepo{
				token:     "111:secret",
				allowed:   []int64{42},
				mode:      db.ModeAgent,
				inboxPath: inbox,
				task: db.TelegramAgentTask{
					ChatID: 42, Instruction: "x", Status: string(status),
				},
			}
			sup := NewSupervisor(repo, func(int64) bool { return true })
			fake := newFakeTelegram(t)

			sent := pressAndRead(t, sup, fake, cbApprove)

			if orders := readOrders(t, inbox); len(orders) != 0 {
				t.Fatalf("a refused approval submitted %d orders", len(orders))
			}
			if repo.task.Status != string(status) {
				t.Errorf("task status = %q, want it unchanged at %q", repo.task.Status, status)
			}
			if !strings.Contains(joinedText(sent), "No puedo hacer eso") {
				t.Errorf("the refusal was not explained: %v", sent)
			}
		})
	}
}

// S7: chat mode still forwards the text as a conversation turn and never
// submits an order, so the executor is reachable only from agent mode.
func TestChatModeNeverSubmitsOrder(t *testing.T) {
	srv, got := newTestGateway(t, "respuesta del gateway")
	inbox := filepath.Join(t.TempDir(), "inbox.jsonl")
	repo := &fakeRepo{
		token:     "111:secret",
		allowed:   []int64{42},
		model:     "test/model",
		inboxPath: inbox,
	}
	repo.session = &db.TelegramSession{ID: 1, ChatID: 42}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	sup.SetGateway(srv.URL, "sk-test")
	fake := newFakeTelegram(t)

	sent := runCommand(t, sup, fake, "hola, ¿cómo va el deploy?")

	if len(*got) != 1 {
		t.Fatalf("gateway received %d chat requests, want the turn forwarded", len(*got))
	}
	if orders := readOrders(t, inbox); len(orders) != 0 {
		t.Errorf("chat mode submitted %d orders, want none", len(orders))
	}
	if repo.task.Instruction != "" {
		t.Errorf("chat mode recorded a task: %+v", repo.task)
	}
	if !strings.Contains(joinedText(sent), "respuesta del gateway") {
		t.Errorf("chat reply = %v, want the gateway answer", sent)
	}
}

// A transient fault must not condemn the channel forever. setError used to
// overwrite the whole status with StateError, so one expired long-poll (or the
// cosmetic SetMyCommands failure) left the dashboard red until a restart, and
// reconcile() only ever calls startLoop when no loop exists — so nothing could
// clear it. The loop keeps running; only the last error is reported.
func TestSetError_DoesNotStopTheRunningLoop(t *testing.T) {
	s := NewSupervisor(&fakeRepo{}, func(int64) bool { return true })
	s.mu.Lock()
	s.status = Status{State: StateRunning, Running: true}
	s.mu.Unlock()

	s.setError(errors.New("telegram: unknown error"))

	st := s.Status()
	if st.State != StateRunning {
		t.Fatalf("State = %q after a transient error, want %q", st.State, StateRunning)
	}
	if !st.Running {
		t.Error("Running = false after a transient error, want true")
	}
	if st.LastError != "telegram: unknown error" {
		t.Errorf("LastError = %q, want the reported cause", st.LastError)
	}
}

func TestStartLoop_ClearsLastError(t *testing.T) {
	s := NewSupervisor(&fakeRepo{}, func(int64) bool { return true })
	s.setError(errors.New("telegram: token rejected: 401"))

	if st := s.Status(); st.State != StateError {
		t.Fatalf("state before start = %q, want %q", st.State, StateError)
	}
	s.startLoop("tok")
	defer s.stopLoop()
	if st := s.Status(); st.LastError != "" {
		t.Errorf("LastError = %q after a successful start, want empty", st.LastError)
	}
}

// A task left running by a previous process has no order behind it any more:
// the spool is drained by the development session, and the gateway restarting
// does not resurrect the instruction. Reporting it as running forever is the
// one thing the panel must never do, so the boot pass closes it with the
// reason the operator can act on.
func TestReconcile_ClosesOrphanedRunningTask(t *testing.T) {
	repo := &fakeRepo{
		task: db.TelegramAgentTask{
			ChatID:      42,
			Instruction: "Actualiza el CHANGELOG.md",
			Status:      string(TaskRunning),
		},
		allowed: []int64{42},
	}
	sup := NewSupervisor(repo, func(id int64) bool { return id == 42 })

	sup.closeOrphanedTask()

	got := repo.task.Status
	if !strings.HasPrefix(got, string(TaskFailed)+": ") {
		t.Fatalf("status = %q, want it to start with %q", got, string(TaskFailed)+": ")
	}
	if !strings.Contains(got, "orden perdida") {
		t.Errorf("status = %q, want the lost-order reason", got)
	}
}

func TestCloseOrphanedTask_LeavesOtherStatesAlone(t *testing.T) {
	for _, status := range []TaskStatus{TaskIdle, TaskPendingApproval, TaskCancelled, TaskDone} {
		t.Run(string(status), func(t *testing.T) {
			repo := &fakeRepo{
				task:    db.TelegramAgentTask{ChatID: 42, Instruction: "x", Status: string(status)},
				allowed: []int64{42},
			}
			NewSupervisor(repo, func(int64) bool { return true }).closeOrphanedTask()
			if repo.task.Status != string(status) {
				t.Errorf("status = %q, want it untouched", repo.task.Status)
			}
		})
	}
}
