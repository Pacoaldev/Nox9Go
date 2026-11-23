package telegram

import (
	"testing"

	"nox9go/proxy/internal/notify"
)

func TestFeedEventForwards(t *testing.T) {
	commit := notify.Event{Kind: notify.KindCommit, Title: "landed"}
	done := notify.Event{Kind: notify.KindOrderDone, Title: "ok"}

	if !feedEventForwards(feedScopeAll, commit) {
		t.Error("all scope should forward commits")
	}
	if feedEventForwards(feedScopeSilent, commit) {
		t.Error("silent scope should block commits")
	}
	if !feedEventForwards(feedScopeTaskOnly, done) {
		t.Error("task-only should forward order_done")
	}
	if feedEventForwards(feedScopeTaskOnly, commit) {
		t.Error("task-only should block commits")
	}
}