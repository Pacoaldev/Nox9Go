package telegram

import "nox9go/proxy/internal/notify"

// feedScope controls which development events reach the chat. Task outcomes are
// always reconciled regardless of scope.
type feedScope int

const (
	feedScopeAll feedScope = iota
	feedScopeTaskOnly
	feedScopeSilent
)

func feedScopeLabel(s feedScope) string {
	switch s {
	case feedScopeTaskOnly:
		return "Solo tareas"
	case feedScopeSilent:
		return "Silenciado"
	default:
		return "Todo"
	}
}

func (s feedScope) next() feedScope {
	return feedScope((int(s) + 1) % 3)
}

// feedEventForwards reports whether an event should be pushed as chat text.
func feedEventForwards(scope feedScope, event notify.Event) bool {
	switch scope {
	case feedScopeSilent:
		return false
	case feedScopeTaskOnly:
		switch event.Kind {
		case notify.KindOrderDone, notify.KindOrderFailed, notify.KindBlocker:
			return true
		default:
			if event.Kind == notify.KindTask || event.Kind == notify.KindInfo {
				return devEventCompletesOrder(event) || devEventFailsOrder(event)
			}
			return false
		}
	default:
		return true
	}
}