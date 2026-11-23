package telegram

import (
	"fmt"
	"strings"

	"nox9go/proxy/internal/notify"
)

// feedUsesRichLayout reports whether a spool event should use HTML feed layout
// (short header + optional preformatted body) instead of a single plain block.
func feedUsesRichLayout(kind notify.Kind) bool {
	switch kind {
	case notify.KindOrderDone, notify.KindOrderFailed, notify.KindBlocker:
		return true
	default:
		return false
	}
}

// feedEventHeaderHTML is the one-line summary for rich feed events.
func feedEventHeaderHTML(event notify.Event) string {
	stamp := event.At.UTC().Format("15:04")
	label := esc(event.Kind.Label())
	title := esc(strings.TrimSpace(event.Title))
	if title == "" {
		return fmt.Sprintf("[%s] <b>%s</b>", stamp, label)
	}
	return fmt.Sprintf("[%s] <b>%s</b>\n%s", stamp, label, title)
}

// feedEventBodyHTML wraps detail text for Telegram monospace rendering.
func feedEventBodyHTML(detail string) []string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return nil
	}
	const preOverhead = len("<pre></pre>")
	limit := maxTelegramMessage - preOverhead
	if limit < 512 {
		limit = 512
	}
	var parts []string
	for _, chunk := range splitMessage(detail, limit) {
		parts = append(parts, "<pre>"+esc(chunk)+"</pre>")
	}
	return parts
}