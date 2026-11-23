package telegram

import (
	"context"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/notify"
)

func TestForwardPendingEvents_CompletesRunningTask(t *testing.T) {
	spool := notify.NewSpool(t.TempDir() + "/events.jsonl")
	if err := spool.Append(notify.Event{
		ID:     "abcd1234-xyz",
		Kind:   notify.KindOrderDone,
		Title:  "Orden completada",
		Detail: "desplegá la rama",
		TaskID: "abcd1234",
	}); err != nil {
		t.Fatalf("Append() = %v", err)
	}

	fake := newFakeTelegram(t)
	repo := &fakeRepo{
		token:   "111:secret",
		allowed: []int64{42},
		mode:    db.ModeAgent,
		task: db.TelegramAgentTask{
			ChatID:        42,
			Instruction:   "desplegá la rama",
			Status:        string(TaskRunning),
			CorrelationID: "abcd1234",
		},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sup.forwardPendingEvents(context.Background(), fake.bot(t), repo, spool)

	if repo.task.Status != string(TaskDone) {
		t.Fatalf("task status = %q, want done", repo.task.Status)
	}
	msgs := fake.messages()
	if len(msgs) < 2 {
		t.Fatalf("sent %d messages, want feed + completion notice: %v", len(msgs), msgs)
	}
	var found bool
	for _, msg := range msgs {
		if strings.Contains(msg, "completada") && strings.Contains(msg, "abcd1234") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("messages = %v, want tagged completion notice", msgs)
	}
}

func TestReconcileTaskFromDevEvent_BlockerSetsAwaitingHerdr(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{
		allowed: []int64{42},
		task: db.TelegramAgentTask{
			ChatID:        42,
			Instruction:   "preguntá algo",
			Status:        string(TaskRunning),
			CorrelationID: "abcd1234",
		},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sup.reconcileTaskFromDevEvent(context.Background(), fake.bot(t), 42, notify.Event{
		Kind:   notify.KindBlocker,
		Title:  "Agente bloqueado",
		Detail: "¿Dónde querés el saludo?",
		TaskID: "abcd1234",
	})

	if repo.task.Status != string(TaskAwaitingHerdr) {
		t.Fatalf("task status = %q, want awaiting_herdr", repo.task.Status)
	}
	msgs := fake.messages()
	var found bool
	for _, msg := range msgs {
		if strings.Contains(msg, "reenvío al panel") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("messages = %v, want Herdr relay hint", msgs)
	}
}

func TestReconcileTaskFromDevEvent_RequiresMatchingDetail(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{
		allowed: []int64{42},
		task: db.TelegramAgentTask{
			ChatID:      42,
			Instruction: "tarea A",
			Status:      string(TaskRunning),
		},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	sup.reconcileTaskFromDevEvent(context.Background(), fake.bot(t), 42, notify.Event{
		Kind:   notify.KindOrderDone,
		Title:  "done",
		Detail: "tarea B",
	})

	if repo.task.Status != string(TaskRunning) {
		t.Errorf("task status = %q, want still running for mismatched detail", repo.task.Status)
	}
}