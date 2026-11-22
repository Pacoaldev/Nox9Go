package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpool_AppendThenDrain(t *testing.T) {
	spool := NewSpool(filepath.Join(t.TempDir(), "events.jsonl"))

	if err := spool.Append(Event{Kind: KindTask, Title: "T1 done"}); err != nil {
		t.Fatalf("Append() = %v", err)
	}
	if err := spool.Append(Event{Kind: KindCommit, Title: "abc123 feat: x"}); err != nil {
		t.Fatalf("Append() = %v", err)
	}

	events, err := spool.Drain()
	if err != nil {
		t.Fatalf("Drain() = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Title != "T1 done" {
		t.Errorf("events[0].Title = %q", events[0].Title)
	}
	if events[1].Kind != KindCommit {
		t.Errorf("events[1].Kind = %q, want commit", events[1].Kind)
	}
}

// A drain empties the spool, so the same event is never forwarded twice — the
// failure mode an operator would notice as the bot repeating itself.
func TestSpool_DrainIsDestructive(t *testing.T) {
	spool := NewSpool(filepath.Join(t.TempDir(), "events.jsonl"))
	if err := spool.Append(Event{Title: "only once"}); err != nil {
		t.Fatalf("Append() = %v", err)
	}

	first, err := spool.Drain()
	if err != nil {
		t.Fatalf("first Drain() = %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first drain got %d events, want 1", len(first))
	}

	second, err := spool.Drain()
	if err != nil {
		t.Fatalf("second Drain() = %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second drain got %d events, want 0", len(second))
	}
}

// A spool that was never written drains to nothing rather than to an error, so
// a channel with no development activity is silent instead of broken.
func TestSpool_MissingFileIsEmpty(t *testing.T) {
	events, err := NewSpool(filepath.Join(t.TempDir(), "absent.jsonl")).Drain()
	if err != nil {
		t.Fatalf("Drain() on a missing file = %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

// One corrupt line must not silence every later event: a truncated append
// happens on a crash, and losing the rest of the batch would hide real work.
func TestSpool_SkipsCorruptLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"title\":\"good\"}\nnot json\n{\"title\":\"also good\"}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	events, err := NewSpool(path).Drain()
	if err != nil {
		t.Fatalf("Drain() = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want the 2 readable ones", len(events))
	}
}

// An empty path is a no-op: a caller appends unconditionally and the absence of
// a configured spool simply means nobody is listening.
func TestSpool_EmptyPathIsNoop(t *testing.T) {
	var spool *Spool
	if err := spool.Append(Event{Title: "ignored"}); err != nil {
		t.Errorf("Append() on a nil spool = %v, want nil", err)
	}
	events, err := NewSpool("").Drain()
	if err != nil || len(events) != 0 {
		t.Errorf("Drain() on an empty path = (%v, %v), want nothing", events, err)
	}
}

// The message an operator reads carries the time, the kind and the title, which
// is everything a glance needs.
func TestEvent_Render(t *testing.T) {
	event := Event{
		At:    time.Date(2026, 11, 22, 14, 5, 0, 0, time.UTC),
		Kind:  KindCommit,
		Title: "608b344c fix(telegram): list models",
	}

	got := event.Render()

	for _, want := range []string{"14:05", "commit", "608b344c"} {
		if !strings.Contains(got, want) {
			t.Errorf("Render() = %q, want it to contain %q", got, want)
		}
	}
}

func TestEvent_RenderWithDetail(t *testing.T) {
	event := Event{At: time.Now(), Kind: KindBlocker, Title: "merge?", Detail: "A o B"}

	got := event.Render()

	if !strings.Contains(got, "decisión") {
		t.Errorf("Render() = %q, want the blocker label", got)
	}
	if !strings.Contains(got, "A o B") {
		t.Errorf("Render() = %q, want the detail", got)
	}
}

func TestKind_Label(t *testing.T) {
	tests := []struct {
		kind Kind
		want string
	}{
		{KindTask, "tarea"},
		{KindCommit, "commit"},
		{KindCheck, "check"},
		{KindBlocker, "decisión"},
		{KindInfo, "info"},
		{Kind("unknown"), "info"},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := tt.kind.Label(); got != tt.want {
				t.Errorf("Label() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Events written concurrently from several goroutines all survive, because a
// session may report from more than one worker.
func TestSpool_ConcurrentAppend(t *testing.T) {
	spool := NewSpool(filepath.Join(t.TempDir(), "events.jsonl"))

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 5; j++ {
				_ = spool.Append(Event{Title: "event"})
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}

	events, err := spool.Drain()
	if err != nil {
		t.Fatalf("Drain() = %v", err)
	}
	if len(events) != 40 {
		t.Errorf("got %d events, want 40", len(events))
	}
}
