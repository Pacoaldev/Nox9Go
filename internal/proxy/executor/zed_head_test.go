package executor

// Tests for the diagnostic helpers around the Zed event-stream fold.
//
// zedAggregateClaudeSSE used to report every unreadable stream as "carried no
// completion chunk", which is true of an empty stream, a non-JSON stream and a
// JSON stream of event types the fold does not model — three different faults
// with one message. These tests pin the renderer that now shows what actually
// arrived, so the log line stays readable whatever the upstream sends.

import (
	"strings"
	"testing"
)

func TestZedHead(t *testing.T) {
	t.Run("empty body renders nothing", func(t *testing.T) {
		if got := zedHead(nil, 100); got != "" {
			t.Errorf("zedHead(nil) = %q, want empty", got)
		}
	})

	t.Run("plain JSON is returned verbatim", func(t *testing.T) {
		if got := zedHead([]byte(`{"type":"ping"}`), 100); got != `{"type":"ping"}` {
			t.Errorf("unexpected rendering: %q", got)
		}
	})

	t.Run("newlines are escaped so one event stays one log line", func(t *testing.T) {
		got := zedHead([]byte("data: a\n\ndata: b"), 100)
		if strings.Contains(got, "\n") {
			t.Fatalf("raw newline leaked into the log line: %q", got)
		}
		if got != `data: a\n\ndata: b` {
			t.Errorf("unexpected rendering: %q", got)
		}
	})

	t.Run("control bytes are escaped", func(t *testing.T) {
		got := zedHead([]byte{0x00, 0x1f, 0x7f}, 100)
		if got != `\x00\x1f\x7f` {
			t.Errorf("unexpected rendering: %q", got)
		}
	})

	t.Run("truncation is marked", func(t *testing.T) {
		got := zedHead([]byte("abcdef"), 3)
		if got != "abc..." {
			t.Errorf("want a marked truncation, got %q", got)
		}
	})

	t.Run("a body of exactly the limit is not marked", func(t *testing.T) {
		got := zedHead([]byte("abc"), 3)
		if got != "abc" {
			t.Errorf("want no truncation marker, got %q", got)
		}
	})
}

// A stream whose events never parse must still fail with the fold's error,
// but the failure has to be reachable — the guard is what the log sits on.
func TestZedAggregate_EmptyStreamStillFails(t *testing.T) {
	if _, err := zedAggregateClaudeSSE(nil); err == nil {
		t.Fatal("an empty stream must not fold into a completion")
	}
}
