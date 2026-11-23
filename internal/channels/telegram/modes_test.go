package telegram

import (
	"context"
	"strings"
	"testing"

	tgbotmodels "github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/db"
)

// testMessage builds an incoming text message from one user.
func testMessage(userID int64, text string) *tgbotmodels.Message {
	return &tgbotmodels.Message{
		Text: text,
		From: &tgbotmodels.User{ID: userID},
		Chat: tgbotmodels.Chat{ID: userID},
	}
}

// render gathers the text and the button data of a panel in one call, so a test
// asserts what the operator actually receives.
func render(t *testing.T, s PanelState) (string, []string) {
	t.Helper()
	text, kb := RenderModePanel(s)
	markup, ok := kb.(tgbotmodels.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("markup type = %T, want InlineKeyboardMarkup", kb)
	}
	var data []string
	for _, row := range markup.InlineKeyboard {
		for _, b := range row {
			data = append(data, b.CallbackData)
		}
	}
	return text, data
}

// S2: the chat panel names the mode, shows the model and offers only the
// conversation controls.
func TestRenderChatPanel(t *testing.T) {
	text, data := render(t, PanelState{Mode: db.ModeChat, ModelOrCombo: "deepseek-chat"})

	for _, want := range []string{"Modo Chat", "deepseek-chat"} {
		if !strings.Contains(text, want) {
			t.Errorf("chat panel missing %q:\n%s", want, text)
		}
	}
	want := []string{"m:chat:model", "m:chat:reset", "m:dock:sessions", "m:chat:status", "m:chat:end", "m:mode:agent", "m:dock:hide"}
	if strings.Join(data, ",") != strings.Join(want, ",") {
		t.Errorf("chat panel buttons = %v, want %v", data, want)
	}
	// A chat panel must not offer task controls: there is no task.
	for _, forbidden := range []string{"m:ag:approve", "m:ag:cancel"} {
		if strings.Contains(strings.Join(data, ","), forbidden) {
			t.Errorf("chat panel offered the agent control %q", forbidden)
		}
	}
}

// S2: an unset model is named rather than left blank, so the panel never shows
// an empty field the operator cannot interpret.
func TestRenderChatPanel_NoModel(t *testing.T) {
	text, _ := render(t, PanelState{Mode: db.ModeChat})
	if !strings.Contains(text, "sin elegir") {
		t.Errorf("chat panel did not name the empty model:\n%s", text)
	}
}

// S3: the agent panel's controls follow the task state. Offering an action the
// state would refuse is what makes an operator press a button that does nothing.
func TestRenderAgentPanel_ControlsFollowState(t *testing.T) {
	tests := []struct {
		name       string
		state      PanelState
		wantData   []string
		notWant    []string
		wantStatus string
	}{
		{
			name:       "pending approval offers approve and reject",
			state:      PanelState{Mode: db.ModeAgent, TaskStatus: TaskPendingApproval, PendingApproval: "desplegar"},
			wantData:   []string{"m:ag:approve", "m:ag:reject"},
			notWant:    []string{"m:ag:cancel"},
			wantStatus: "Esperando aprobación",
		},
		{
			name:       "running offers cancel",
			state:      PanelState{Mode: db.ModeAgent, TaskStatus: TaskRunning},
			wantData:   []string{"m:ag:cancel"},
			notWant:    []string{"m:ag:approve"},
			wantStatus: "Ejecutando",
		},
		{
			name:       "idle offers the autonomy toggle",
			state:      PanelState{Mode: db.ModeAgent, TaskStatus: TaskIdle, AutoApprove: true},
			wantData:   []string{"m:ag:toggle_auto"},
			notWant:    []string{"m:ag:approve", "m:ag:cancel"},
			wantStatus: "Inactivo",
		},
		{
			name:       "dock visible hides duplicate status buttons",
			state:      PanelState{Mode: db.ModeAgent, TaskStatus: TaskRunning, DockHidden: false},
			notWant:    []string{"m:ag:status", "m:ag:autonomy"},
			wantStatus: "Ejecutando",
		},
		{
			name:       "dock hidden restores task status on the inline panel",
			state:      PanelState{Mode: db.ModeAgent, TaskStatus: TaskRunning, DockHidden: true},
			wantData:   []string{"m:ag:status"},
			notWant:    []string{"m:ag:autonomy"},
			wantStatus: "Ejecutando",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, data := render(t, tt.state)
			joined := strings.Join(data, ",")
			if tt.wantStatus != "" && !strings.Contains(text, tt.wantStatus) {
				t.Errorf("panel missing status %q:\n%s", tt.wantStatus, text)
			}
			for _, want := range tt.wantData {
				if !strings.Contains(joined, want) {
					t.Errorf("missing button %q in %v", want, data)
				}
			}
			for _, forbidden := range tt.notWant {
				if strings.Contains(joined, forbidden) {
					t.Errorf("offered %q when the state does not allow it: %v", forbidden, data)
				}
			}
			// Every agent panel ends with the way back.
			if !strings.Contains(joined, "m:mode:chat") {
				t.Errorf("agent panel lost the switch-to-chat button: %v", data)
			}
		})
	}
}

// S8: Telegram rejects a callback_data over 64 bytes, which would make the
// button dead. Every value this package can emit is checked, not one sample.
func TestCallbackDataFitsTelegramLimit(t *testing.T) {
	all := []string{
		cbChatModel, cbChatReset, cbModeAgent, cbModeChat,
		cbApprove, cbReject, cbCancelTask, cbRefresh,
		cbToggleAuto, cbTaskStatus, cbAutonomy, cbDockHide, cbDockShow,
		cbFeedCycle, cbAgentCompact, cbChatStatus, cbChatEnd, cbDevPanel,
	}
	for _, v := range all {
		if len(v) > maxCallbackData {
			t.Errorf("callback_data %q is %d bytes, limit %d", v, len(v), maxCallbackData)
		}
		if !strings.HasPrefix(v, cbPrefix) {
			t.Errorf("callback_data %q is outside the panel namespace", v)
		}
	}
}

// S3/S6: the pending instruction is shown so the operator can see what they are
// approving instead of approving blind.
func TestRenderAgentPanel_ShowsPendingInstruction(t *testing.T) {
	text, _ := render(t, PanelState{
		Mode:            db.ModeAgent,
		TaskStatus:      TaskPendingApproval,
		PendingApproval: "borrar los logs",
	})
	if !strings.Contains(text, "borrar los logs") {
		t.Errorf("panel hid the pending instruction:\n%s", text)
	}
}

// S3: a value containing markup characters is escaped, or it either breaks the
// message or injects formatting.
func TestRenderAgentPanel_EscapesValues(t *testing.T) {
	text, _ := render(t, PanelState{
		Mode:            db.ModeAgent,
		TaskStatus:      TaskPendingApproval,
		PendingApproval: "a <b>bold</b> trick",
	})
	if strings.Contains(text, "<b>bold</b>") {
		t.Errorf("an unescaped value reached the panel:\n%s", text)
	}
	if !strings.Contains(text, "&lt;b&gt;") {
		t.Errorf("expected the value escaped:\n%s", text)
	}
}

// S6/S7: the transition table is the whole safety of the buttons. A refused
// transition must be refused, not silently applied.
func TestTaskTransitionAllowed(t *testing.T) {
	tests := []struct {
		from, to TaskStatus
		want     bool
	}{
		{TaskPendingApproval, TaskRunning, true},
		{TaskPendingApproval, TaskRejected, true},
		{TaskPendingApproval, TaskCancelled, true},
		{TaskRunning, TaskCancelled, true},
		{TaskRunning, TaskRunning, false},
		{TaskRunning, TaskRejected, false},
		{TaskIdle, TaskRunning, false},
		{TaskIdle, TaskCancelled, false},
		{TaskDone, TaskRunning, false},
		// S3: a task that failed never claims progress, and failure is only
		// reachable from the two states a task can be submitted from.
		{TaskPendingApproval, TaskFailed, true},
		{TaskRunning, TaskFailed, true},
		{TaskRunning, TaskDone, true},
		{TaskRunning, TaskAwaitingHerdr, true},
		{TaskAwaitingHerdr, TaskRunning, true},
		{TaskAwaitingHerdr, TaskCancelled, true},
		{TaskAwaitingHerdr, TaskDone, true},
		{TaskAwaitingHerdr, TaskFailed, true},
		{TaskIdle, TaskDone, false},
		{TaskIdle, TaskFailed, false},
		{TaskCancelled, TaskFailed, false},
		{TaskDone, TaskFailed, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := taskTransitionAllowed(tt.from, tt.to); got != tt.want {
				t.Errorf("taskTransitionAllowed(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

// S5: in agent mode free text becomes a task, and it does not reach the chat
// route. The distinction is the whole point of the mode.
func TestHandleMessage_AgentModeRecordsTask(t *testing.T) {
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: db.ModeAgent}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	fake := newFakeTelegram(t)
	sup.handleMessage(context.Background(), fake.bot(t), testMessage(42, "desplegá la rama"))

	if repo.task.Instruction != "desplegá la rama" {
		t.Errorf("task instruction = %q, want the message text", repo.task.Instruction)
	}
	if repo.task.Status != string(TaskPendingApproval) {
		t.Errorf("task status = %q, want pending approval", repo.task.Status)
	}
}

// S5: auto-approve skips the approval step, which is what the toggle promises.
func TestHandleMessage_AgentModeAutoApprove(t *testing.T) {
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: db.ModeAgent, autoApprove: true}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	fake := newFakeTelegram(t)
	sup.handleMessage(context.Background(), fake.bot(t), testMessage(42, "tarea directa"))

	if repo.task.Status != string(TaskRunning) {
		t.Errorf("task status = %q, want running under auto-approve", repo.task.Status)
	}
}

// S1: an unknown stored value reads as chat. A corrupt row must not put the
// channel into agent mode and start recording tasks.
func TestParseBotMode(t *testing.T) {
	tests := []struct {
		in   string
		want db.BotMode
	}{
		{"agent", db.ModeAgent},
		{"AGENT", db.ModeAgent},
		{" agent ", db.ModeAgent},
		{"chat", db.ModeChat},
		{"", db.ModeChat},
		{"garbage", db.ModeChat},
	}
	for _, tt := range tests {
		if got := db.ParseBotMode(tt.in); got != tt.want {
			t.Errorf("ParseBotMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// S9: a callback from a user outside the allowlist changes nothing. A button
// must not be a way around the gate.
func TestHandleCallbackQuery_RefusesNonAllowed(t *testing.T) {
	repo := &fakeRepo{token: "111:secret", allowed: []int64{1}, mode: db.ModeChat}
	sup := NewSupervisor(repo, func(id int64) bool { return id == 1 })

	q := &tgbotmodels.CallbackQuery{
		ID:   "cb1",
		From: tgbotmodels.User{ID: 999},
		Message: tgbotmodels.MaybeInaccessibleMessage{
			Message: &tgbotmodels.Message{Chat: tgbotmodels.Chat{ID: 999}},
		},
		Data: cbModeAgent,
	}
	fake := newFakeTelegram(t)
	sup.handleCallbackQuery(context.Background(), fake.bot(t), q)

	if repo.mode != db.ModeChat {
		t.Errorf("mode changed to %q by an unauthorized callback", repo.mode)
	}
}

// S6/S7: a transition the state refuses is refused, and the task keeps its
// state. Silently applying it would make the panel lie.
func TestResolveTask_RefusesInvalidTransition(t *testing.T) {
	repo := &fakeRepo{
		token:   "111:secret",
		allowed: []int64{42},
		mode:    db.ModeAgent,
		task:    db.TelegramAgentTask{ChatID: 42, Instruction: "x", Status: string(TaskIdle)},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	fake := newFakeTelegram(t)
	sup.resolveTask(context.Background(), fake.bot(t), 42, TaskRunning, "should not apply")

	if repo.task.Status != string(TaskIdle) {
		t.Errorf("task status = %q, want it unchanged at idle", repo.task.Status)
	}
}

// A running task keeps its instruction on the panel instead of inviting the
// operator to type another one, which is what a screenshot showed as confusing.
func TestRenderAgentPanel_RunningShowsInstruction(t *testing.T) {
	text, _ := render(t, PanelState{
		Mode:               db.ModeAgent,
		TaskStatus:         TaskRunning,
		CurrentInstruction: "desplegar la rama feat/telegram",
	})
	if !strings.Contains(text, "desplegar la rama") {
		t.Errorf("panel lost the running instruction:\n%s", text)
	}
	if strings.Contains(text, "Enviá una instrucción") {
		t.Errorf("panel asked for another task while one runs:\n%s", text)
	}
}
