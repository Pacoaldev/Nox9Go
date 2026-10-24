package executor

import "nox9go/proxy/internal/proxy"

// opencodeSessionHint resolves the upstream session a free-lane request
// travels with.
//
// The client's own session wins — BuildOpenCodeHeaders translates it into the
// canonical descending form. Without one, every request would mint a fresh
// upstream session, and the free tier accounts its quota per session: a fresh
// id per request burns through that budget and surfaces as 429
// FreeUsageLimitError with a growing retry-after, while the official CLI
// reuses one long-lived session per conversation (decolua/9router
// stableSessionId; dsh-our-free-model sessionForConversation). A hint-less
// request therefore falls back to one stable session per connection,
// recycled after an idle TTL.
//
// Keyed lanes keep their previous per-request behavior: their quota is
// accounted per key, not per session.
func opencodeSessionHint(req *Request, free bool) string {
	if !free || req.SessionID != "" {
		return req.SessionID
	}
	key := "opencode:default"
	if req.ConnectionID != "" {
		key = "opencode:conn:" + req.ConnectionID
	}
	return proxy.StableOpenCodeSessionID(key)
}
