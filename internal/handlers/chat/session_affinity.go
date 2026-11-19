package chat

import (
	"sync"
	"time"

	"nox9go/proxy/internal/models"
)

// codexSessionPinTTL is how long a conversation stays on the account that
// served its first turn. The Codex backend keeps its prompt cache per account,
// so a rotating account sends every turn to a cold shard: measured on a 30k
// prefix, the same session stayed cached on one account and missed on the next.
// Thirty minutes covers a tool-use turn without freezing an account for the
// whole session.
const codexSessionPinTTL = 30 * time.Minute

// maxSessionPins bounds the map. The key is client-supplied, so the table has
// to be bounded or it is a memory leak with a schedule (§4.G).
const maxSessionPins = 2048

type sessionPin struct {
	ConnID    string
	ExpiresAt time.Time
}

// sessionPinState holds the per-conversation account pins.
type sessionPinState struct {
	mu    sync.Mutex
	pins  map[string]sessionPin
	nowFn func() time.Time
}

func (s *sessionPinState) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func (s *sessionPinState) lookup(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pin, ok := s.pins[key]
	if !ok {
		return "", false
	}
	if !pin.ExpiresAt.After(s.now()) {
		delete(s.pins, key)
		return "", false
	}
	return pin.ConnID, true
}

func (s *sessionPinState) record(key, connID string) {
	if key == "" || connID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pins == nil {
		s.pins = make(map[string]sessionPin)
	}
	now := s.now()
	if len(s.pins) >= maxSessionPins {
		s.evictLocked(now)
	}
	s.pins[key] = sessionPin{ConnID: connID, ExpiresAt: now.Add(codexSessionPinTTL)}
}

// evictLocked drops expired pins first. When the table is still full of live
// entries the oldest deadline goes, so the map cannot grow past its bound.
func (s *sessionPinState) evictLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for k, pin := range s.pins {
		if !pin.ExpiresAt.After(now) {
			delete(s.pins, k)
			continue
		}
		if oldestKey == "" || pin.ExpiresAt.Before(oldest) {
			oldestKey, oldest = k, pin.ExpiresAt
		}
	}
	if len(s.pins) >= maxSessionPins && oldestKey != "" {
		delete(s.pins, oldestKey)
	}
}

// sessionPinKey scopes a pin to one provider and one conversation. Only
// providers whose upstream cache is per account get a key: pinning elsewhere
// would trade account fairness for a cache the provider does not keep.
func sessionPinKey(provider, sessionID string) string {
	if provider != "codex" || sessionID == "" {
		return ""
	}
	return provider + ":" + sessionID
}

// promotePinnedConnection moves the pinned account to the front of the
// candidate list. The row must still be in the list, so an account that is
// excluded, cooling down, or unable to serve the model is skipped and the
// configured strategy answers instead.
func promotePinnedConnection(conns []*models.ProviderConnection, pinnedID string) []*models.ProviderConnection {
	if pinnedID == "" || len(conns) < 2 {
		return conns
	}
	idx := -1
	for i, c := range conns {
		if c.ID == pinnedID {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return conns
	}
	out := make([]*models.ProviderConnection, 0, len(conns))
	out = append(out, conns[idx])
	out = append(out, conns[:idx]...)
	out = append(out, conns[idx+1:]...)
	return out
}
