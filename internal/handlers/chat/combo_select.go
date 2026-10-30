package chat

import "context"

type comboSelectContextKey struct{}

var comboSelectContextKeyVal = comboSelectContextKey{}

// withComboSelection marks a context as belonging to a combo member pick. The
// connection selector must not honour forceFallback here: that toggle exists
// for a lone provider with nowhere else to go, and a combo's next member is
// the failover. Forcing a cooling account through an active rateLimitedUntil
// is how an operator earns a provider ban.
func withComboSelection(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, comboSelectContextKeyVal, true)
}

// isComboSelection reports whether the context belongs to a combo member pick.
func isComboSelection(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(comboSelectContextKeyVal).(bool)
	return v
}
