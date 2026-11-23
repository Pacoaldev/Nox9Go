package telegram

import (
	"strings"
	"testing"
	"time"

	"nox9go/proxy/internal/notify"
)

func TestFeedUsesRichLayout_orderOutcomes(t *testing.T) {
	for _, kind := range []notify.Kind{notify.KindOrderDone, notify.KindOrderFailed, notify.KindBlocker} {
		if !feedUsesRichLayout(kind) {
			t.Fatalf("kind %q should use rich layout", kind)
		}
	}
	if feedUsesRichLayout(notify.KindCommit) {
		t.Fatal("commit should stay plain")
	}
}

func TestFeedEventHeaderHTML_escapesMarkup(t *testing.T) {
	ev := notify.Event{
		At:    time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC),
		Kind:  notify.KindOrderDone,
		Title: "Orden <done>",
	}
	got := feedEventHeaderHTML(ev)
	if strings.Contains(got, "<done>") {
		t.Fatalf("title not escaped: %q", got)
	}
	if !strings.Contains(got, "&lt;done&gt;") {
		t.Fatalf("want escaped title, got %q", got)
	}
}

func TestFeedEventBodyHTML_splitsLongDetail(t *testing.T) {
	detail := strings.Repeat("x", maxTelegramMessage)
	parts := feedEventBodyHTML(detail)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want split", len(parts))
	}
	for i, p := range parts {
		if !strings.HasPrefix(p, "<pre>") || !strings.HasSuffix(p, "</pre>") {
			t.Fatalf("part %d missing pre wrapper: %q", i, p[:min(20, len(p))])
		}
	}
}