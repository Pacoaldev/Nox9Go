package telegram

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"nox9go/proxy/internal/notify"
)

func TestPollInboxOnce_runsScriptAndReportsOutcome(t *testing.T) {
	root := t.TempDir()
	inboxPath := filepath.Join(root, "inbox.jsonl")
	spoolPath := filepath.Join(root, "events.jsonl")
	script := filepath.Join(root, "ok.ps1")
	if err := os.WriteFile(script, []byte(`
$ErrorActionPreference = 'Stop'
if ($env:NOX9GO_ORDER_DETAIL -ne 'hola') { throw 'wrong detail' }
exit 0
`), 0o600); err != nil {
		t.Fatal(err)
	}
	inbox := notify.NewSpool(inboxPath)
	if err := inbox.Append(notify.Event{Kind: notify.KindOrder, Title: "Orden desde Telegram", Detail: "hola", TaskID: "abc"}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEV_REPO_ROOT", root)
	sup := NewSupervisor(&fakeRepo{
		token:     "111:secret",
		inboxPath: inboxPath,
		spoolPath: spoolPath,
	}, func(int64) bool { return true })

	sup.pollInboxOnce(context.Background(), sup.repo, script)

	spool := notify.NewSpool(spoolPath)
	events, err := spool.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("outcome events = %d, want 1", len(events))
	}
	if events[0].Kind != notify.KindOrderDone {
		t.Errorf("kind = %q, want order_done", events[0].Kind)
	}
	if events[0].TaskID != "abc" {
		t.Errorf("task_id = %q", events[0].TaskID)
	}
}

func TestPollInboxOnce_blockedDoesNotCompleteOrder(t *testing.T) {
	root := t.TempDir()
	inboxPath := filepath.Join(root, "inbox.jsonl")
	spoolPath := filepath.Join(root, "events.jsonl")
	script := filepath.Join(root, "blocked.ps1")
	if err := os.WriteFile(script, []byte("exit 2"), 0o600); err != nil {
		t.Fatal(err)
	}
	inbox := notify.NewSpool(inboxPath)
	if err := inbox.Append(notify.Event{Kind: notify.KindOrder, Title: "Orden", Detail: "x", TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEV_REPO_ROOT", root)
	sup := NewSupervisor(&fakeRepo{inboxPath: inboxPath, spoolPath: spoolPath}, func(int64) bool { return true })
	sup.pollInboxOnce(context.Background(), sup.repo, script)

	events, err := notify.NewSpool(spoolPath).Drain()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != notify.KindBlocker {
		t.Fatalf("spool = %+v, want one blocker", events)
	}
}

func TestRunOrderScript_herdrBlockedExit(t *testing.T) {
	script := filepath.Join(t.TempDir(), "blocked.ps1")
	if err := os.WriteFile(script, []byte("exit 2"), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, _ := runOrderScript(context.Background(), script, notify.Event{Detail: "x"})
	if outcome != orderRunBlocked {
		t.Fatalf("outcome = %v, want blocked", outcome)
	}
}