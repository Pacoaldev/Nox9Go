package telegram

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"nox9go/proxy/internal/notify"
)

// writeOrderScript writes a runner script the host platform can actually
// execute, and returns its path. runOrderScript spawns PowerShell, which only
// exists on Windows, so a POSIX host gets a shell script instead; this keeps the
// suite green on the Linux CI runners without weakening what it asserts.
func writeOrderScript(t *testing.T, name, powershell, shell string) string {
	t.Helper()

	root := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(root, name+".ps1")
		if err := os.WriteFile(path, []byte(powershell), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	path := filepath.Join(root, name+".sh")
	if err := os.WriteFile(path, []byte(shell), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPollInboxOnce_runsScriptAndReportsOutcome(t *testing.T) {
	root := t.TempDir()
	inboxPath := filepath.Join(root, "inbox.jsonl")
	spoolPath := filepath.Join(root, "events.jsonl")
	script := writeOrderScript(t, "ok", `
$ErrorActionPreference = 'Stop'
if ($env:NOX9GO_ORDER_DETAIL -ne 'hola') { throw 'wrong detail' }
Write-Output 'respuesta del agente'
exit 0
`, `#!/bin/sh
set -eu
if [ "$NOX9GO_ORDER_DETAIL" != 'hola' ]; then echo 'wrong detail' >&2; exit 1; fi
echo 'respuesta del agente'
exit 0
`)
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
	if events[0].Detail != "respuesta del agente" {
		t.Errorf("detail = %q, want agent stdout not instruction", events[0].Detail)
	}
}

func TestOrderDoneDetail_prefersRunnerOutput(t *testing.T) {
	if got := orderDoneDetail("  salida  "); got != "salida" {
		t.Fatalf("got %q", got)
	}
	if got := orderDoneDetail(""); got != "" {
		t.Fatalf("empty stdout => %q, want empty", got)
	}
}

func TestPollInboxOnce_blockedDoesNotCompleteOrder(t *testing.T) {
	root := t.TempDir()
	inboxPath := filepath.Join(root, "inbox.jsonl")
	spoolPath := filepath.Join(root, "events.jsonl")
	script := writeOrderScript(t, "blocked", "exit 2", "#!/bin/sh\nexit 2\n")
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
	script := writeOrderScript(t, "blocked", "exit 2", "#!/bin/sh\nexit 2\n")
	outcome, _ := runOrderScript(context.Background(), script, notify.Event{Detail: "x"})
	if outcome != orderRunBlocked {
		t.Fatalf("outcome = %v, want blocked", outcome)
	}
}