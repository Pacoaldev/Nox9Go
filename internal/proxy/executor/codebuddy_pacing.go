package executor

import (
	"context"
	"sync"
	"time"
)

// codebuddyMinGap is the minimum spacing between two request starts on one
// CodeBuddy credential. The gateway restricts accounts it reads as abusive, and
// an agent loop plus failover can fire several requests in the same second on
// one account; spacing them keeps the pattern close to a single interactive
// client.
const codebuddyMinGap = 1500 * time.Millisecond

// codebuddyPacer hands out start slots per credential. The map holds one
// timestamp per credential in use, which is bounded by the account count.
type codebuddyPacer struct {
	mu   sync.Mutex
	next map[string]time.Time
	gap  time.Duration
	now  func() time.Time
}

var defaultCodebuddyPacer = &codebuddyPacer{
	next: make(map[string]time.Time),
	gap:  codebuddyMinGap,
	now:  time.Now,
}

// reserve books the next free slot for key and returns how long the caller
// must wait before sending.
func (p *codebuddyPacer) reserve(key string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	slot := p.next[key]
	if slot.Before(now) {
		slot = now
	}
	p.next[key] = slot.Add(p.gap)
	return slot.Sub(now)
}

// wait blocks until the slot for key is free, or ctx ends.
func (p *codebuddyPacer) wait(ctx context.Context, key string) error {
	d := p.reserve(key)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
