package telegram

import (
	"context"
	"testing"
)

// The hub's "Menu" button must do something an operator can see. Refreshing the
// panel in place is invisible when nothing changed: Telegram answers "message is
// not modified", so the press reads as a dead button. "Menu" therefore re-opens
// the panel as a new message at the bottom of the thread and removes the old
// one, which is what the label promises.
func TestHandleDock_HubMenuReopensPanel(t *testing.T) {
	sup := newTestSupervisor(t)
	fake := newFakeTelegram(t)
	bot := fake.bot(t)
	ctx := context.Background()

	// A panel already exists and is remembered for this chat.
	sup.cmdPanel(ctx, bot, 42)
	openBefore := sup.panelMessage(42)
	if openBefore == 0 {
		t.Fatalf("cmdPanel did not remember a panel message id")
	}
	editedBefore := len(fake.editedMessages())
	sentBefore := len(fake.messages())

	sup.handleDock(ctx, bot, 42, dockActHubMenu)

	if got := len(fake.editedMessages()); got != editedBefore {
		t.Errorf("hub menu edited the panel in place (%d edits, want %d): the operator sees no change",
			got-editedBefore, 0)
	}
	sent := fake.messages()
	if len(sent) <= sentBefore {
		t.Fatalf("hub menu sent no new panel message; sent = %v", sent)
	}
	if openBefore != 0 && len(fake.deletedMessages()) == 0 {
		t.Errorf("hub menu left the previous panel in the thread; deleted = %v", fake.deletedMessages())
	}
}

// Opening the panel when none is on screen must still work, and must not try to
// delete a message that never existed.
func TestHandleDock_HubMenuOpensWithoutExistingPanel(t *testing.T) {
	sup := newTestSupervisor(t)
	fake := newFakeTelegram(t)
	bot := fake.bot(t)

	sup.handleDock(context.Background(), bot, 42, dockActHubMenu)

	if len(fake.messages()) == 0 {
		t.Fatalf("hub menu sent no panel message")
	}
	if got := fake.deletedMessages(); len(got) != 0 {
		t.Errorf("hub menu deleted %v with no panel on screen", got)
	}
	if sup.panelMessage(42) == 0 {
		t.Errorf("hub menu did not remember the panel it opened")
	}
}
