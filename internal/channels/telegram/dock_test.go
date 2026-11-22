package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tgbotmodels "github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/db"
)

// dockLabelsOf reads the button rows of a reply keyboard, so a test asserts
// what the operator sees instead of the struct that carries it.
func dockLabelsOf(t *testing.T, kb tgbotmodels.ReplyKeyboardMarkup) [][]string {
	t.Helper()
	rows := make([][]string, 0, len(kb.Keyboard))
	for _, row := range kb.Keyboard {
		labels := make([]string, 0, len(row))
		for _, button := range row {
			labels = append(labels, button.Text)
		}
		rows = append(rows, labels)
	}
	return rows
}

// lastReplyMarkup returns the reply keyboard carried by the newest message, and
// fails when nothing the bot sent carried one.
func lastReplyMarkup(t *testing.T, f *fakeTelegram) tgbotmodels.ReplyKeyboardMarkup {
	t.Helper()
	markups := f.replyMarkups()
	for i := len(markups) - 1; i >= 0; i-- {
		if markups[i] == "" {
			continue
		}
		var kb tgbotmodels.ReplyKeyboardMarkup
		if err := json.Unmarshal([]byte(markups[i]), &kb); err != nil {
			t.Fatalf("reply_markup %q is not a reply keyboard: %v", markups[i], err)
		}
		return kb
	}
	t.Fatalf("no message carried a reply keyboard; markups = %v", markups)
	return tgbotmodels.ReplyKeyboardMarkup{}
}

// assertRows compares two keyboard layouts by their labels.
func assertRows(t *testing.T, got, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("keyboard rows = %v, want %v", got, want)
	}
	for i := range want {
		if strings.Join(got[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// S1: the chat dock is the app launcher the request asked for — one action per
// row where it deserves the width, two where they pair, and the way to the
// other panel last.
func TestDockMarkup_ChatRows(t *testing.T) {
	kb := dockMarkup(db.ModeChat)

	assertRows(t, dockLabelsOf(t, kb), [][]string{
		{"💬 Nueva sesión"},
		{"🧠 Modelos IA", "🗂️ Sesiones"},
		{"📡 Estado canal", "🛑 Cancelar turno"},
		{"🔒 Cerrar sesión"},
		{"🤖 Cambiar a Modo Agente"},
	})

	if !kb.ResizeKeyboard {
		t.Error("ResizeKeyboard = false, want the dock resized to the chat")
	}
	if !kb.IsPersistent {
		t.Error("IsPersistent = false, want the dock to stay on screen")
	}
	if kb.InputFieldPlaceholder != "Chateá o elegí una opción del menú..." {
		t.Errorf("chat placeholder = %q, want the spec text", kb.InputFieldPlaceholder)
	}
}

// S1: the agent dock carries the task controls instead of the conversation
// ones, because in agent mode free text is an instruction.
func TestDockMarkup_AgentRows(t *testing.T) {
	kb := dockMarkup(db.ModeAgent)

	assertRows(t, dockLabelsOf(t, kb), [][]string{
		{"📋 Tarea actual"},
		{"📊 Estado dev", "🎯 Feature ODD"},
		{"📡 Estado canal", "🛑 Cancelar turno"},
		{"🗂️ Ver sesiones"},
		{"💬 Cambiar a Modo Chat"},
	})

	if !kb.ResizeKeyboard || !kb.IsPersistent {
		t.Error("agent dock must be resized and persistent")
	}
	if kb.InputFieldPlaceholder != "Escribí una orden para Pi o tocá un botón..." {
		t.Errorf("agent placeholder = %q, want the spec text", kb.InputFieldPlaceholder)
	}
}

// S2: every label maps to the command it names. The first-design spellings and
// the bare "Cancelar" stay accepted, so a keyboard already on an operator's
// screen keeps working after the rename.
func TestResolveDock_RoutingTable(t *testing.T) {
	tests := []struct {
		label string
		want  dockAction
	}{
		// The dock as it ships today.
		{dockChatNew, dockActNew},
		{dockChatModels, dockActModels},
		{dockChatSessions, dockActSessions},
		{dockStatus, dockActStatus},
		{dockCancel, dockActCancel},
		{dockChatEnd, dockActEnd},
		{dockChatToAgent, dockActToAgent},
		{dockAgentTask, dockActTask},
		{dockAgentDev, dockActDev},
		{dockAgentFeature, dockActFeatures},
		{dockAgentSessions, dockActSessions},
		{dockAgentToChat, dockActToChat},

		// First-design spellings, still on screen in older chats.
		{"💬 Nueva sesion de chat", dockActNew},
		{"🗂️ Historial sesiones", dockActSessions},
		{"🔒 Cerrar y archivar sesion", dockActEnd},
		{"🤖 Cambiar a Panel Agente", dockActToAgent},
		{"⚡ Orden a Pi", dockActTask},
		{"📊 Estado desarrollo", dockActDev},
		{"💬 Cambiar a Panel Chat", dockActToChat},
		{"Cancelar", dockActCancel},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got, ok := resolveDock(tt.label)
			if !ok {
				t.Fatalf("resolveDock(%q) found no route", tt.label)
			}
			if got != tt.want {
				t.Errorf("resolveDock(%q) = %v, want %v", tt.label, got, tt.want)
			}
		})
	}
}

// S2: a button the dock draws but the router cannot route is a dead button, so
// every label of both docks is checked, not one sample.
func TestResolveDock_EveryDockButtonIsRouted(t *testing.T) {
	for _, mode := range []db.BotMode{db.ModeChat, db.ModeAgent} {
		for _, row := range dockRows(mode) {
			for _, label := range row {
				if _, ok := resolveDock(label); !ok {
					t.Errorf("mode %q draws %q with no route", mode, label)
				}
			}
		}
	}
}

// S7: an ordinary message is not a button. A near miss must not be captured by
// the router, or free text would stop reaching the mode that owns it.
func TestResolveDock_IgnoresFreeText(t *testing.T) {
	for _, text := range []string{
		"estado canal",
		"hola, ¿cómo va el deploy?",
		"📡 Estado",
		"",
	} {
		if action, ok := resolveDock(text); ok {
			t.Errorf("resolveDock(%q) = %v, want no route", text, action)
		}
	}
}

// pressDock delivers one dock button the way Telegram does: an ordinary text
// message carrying the button's label.
func pressDock(t *testing.T, sup *Supervisor, fake *fakeTelegram, label string) []string {
	t.Helper()
	sup.handleMessage(context.Background(), fake.bot(t), &tgbotmodels.Message{
		Text: label,
		Chat: tgbotmodels.Chat{ID: 42},
	})
	return fake.messages()
}

// S2/S3: a press runs the command the label names and leaves the dock on
// screen. The reply keyboard travels with a message, so "re-send it" is what
// keeps the panel one tap away for the next action.
func TestHandleDock_PressRunsCommandAndResetsDock(t *testing.T) {
	tests := []struct {
		name  string
		mode  db.BotMode
		label string
		want  string
	}{
		{name: "estado canal runs /status", mode: db.ModeChat, label: dockStatus, want: "Estado:"},
		{name: "cancelar turno runs /cancel", mode: db.ModeChat, label: dockCancel, want: "nada en vuelo"},
		{name: "cerrar sesion runs /end", mode: db.ModeChat, label: dockChatEnd, want: "No había sesión"},
		{name: "orden a pi (old spelling) opens the task panel", mode: db.ModeAgent, label: "⚡ Orden a Pi", want: "Estado:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: tt.mode}
			sup := NewSupervisor(repo, func(int64) bool { return true })

			sent := pressDock(t, sup, fake, tt.label)

			if len(sent) < 2 {
				t.Fatalf("sent %d messages, want the answer then the dock: %q", len(sent), sent)
			}
			if !strings.Contains(sent[0], tt.want) {
				t.Errorf("answer = %q, want it to contain %q", sent[0], tt.want)
			}
			assertRows(t, dockLabelsOf(t, lastReplyMarkup(t, fake)), dockRows(tt.mode))
		})
	}
}

// S3: after a mode switch from the dock, the keyboard that stays on screen is
// the new mode's, and the panel is redrawn for it too.
func TestHandleDock_ModeSwitchResetsDock(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: db.ModeChat}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	pressDock(t, sup, fake, dockChatToAgent)

	if repo.mode != db.ModeAgent {
		t.Fatalf("stored mode = %q, want agent", repo.mode)
	}
	assertRows(t, dockLabelsOf(t, lastReplyMarkup(t, fake)), dockRows(db.ModeAgent))

	pressDock(t, sup, fake, dockAgentToChat)

	if repo.mode != db.ModeChat {
		t.Fatalf("stored mode = %q, want chat", repo.mode)
	}
	assertRows(t, dockLabelsOf(t, lastReplyMarkup(t, fake)), dockRows(db.ModeChat))
}

// S7: buttons are a second entry point, never a routing rule. Free text after a
// press still goes where the mode sends it, so a chat with no session is still
// answered with the /new hint instead of being forwarded.
func TestHandleDock_KeepsFreeTextRouting(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: db.ModeChat}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	pressDock(t, sup, fake, dockStatus)
	sent := pressDock(t, sup, fake, "hola, ¿cómo va el deploy?")

	last := sent[len(sent)-1]
	if !strings.Contains(last, "No hay sesión activa") {
		t.Errorf("free text after a press = %q, want the no-session hint", last)
	}
}

// S3: the dock also has to survive a chat turn. The acknowledgment a turn
// starts with carries a reply keyboard, and a reply keyboard replaces the one on
// screen, so that ack is exactly where the dock used to disappear: one lone
// cancel button overwrote every action for the rest of the chat.
func TestSendKeyboard_CarriesTheDock(t *testing.T) {
	for _, mode := range []db.BotMode{db.ModeChat, db.ModeAgent} {
		t.Run(string(mode), func(t *testing.T) {
			fake := newFakeTelegram(t)
			repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: mode}
			sup := NewSupervisor(repo, func(int64) bool { return true })

			sup.sendKeyboard(context.Background(), fake.bot(t), 42, "En vuelo")

			assertRows(t, dockLabelsOf(t, lastReplyMarkup(t, fake)), dockRows(mode))
		})
	}
}

// S8: /start is a fresh chat, and it is where the dock must appear: a launcher
// nobody can see is not reachable.
func TestCommands_StartShipsTheDock(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: db.ModeAgent}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sent := runCommand(t, sup, fake, "/start")

	if len(sent) != 3 {
		t.Fatalf("sent %d messages, want help, panel and the dock: %q", len(sent), sent)
	}
	assertRows(t, dockLabelsOf(t, lastReplyMarkup(t, fake)), dockRows(db.ModeAgent))
}
