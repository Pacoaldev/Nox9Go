// Package quotalearn tracks sliding-window usage (RPM, RPD, TPM, TPD) per
// (provider, model, connection) and learns ceilings from rate-limit headers
// and 429 status codes, following the FreeLLMAPI router design.
//
// It is thread-safe, process-local by default, and can persist learned ceilings
// to SQLite (see persistence.go).
package quotalearn

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options tunes learner behaviour. Zero values pick safe defaults.
type Options struct {
	// MaxCooldown caps how long an upstream-named Retry-After window may park
	// a connection.
	MaxCooldown time.Duration
}

const (
	maxCooldown = 1 * time.Hour
)

// Key identifies a quota tracking unit: one connection talking to one model
// on one provider.
type Key struct {
	Provider   string
	Model      string
	Connection string
}

func (k Key) String() string {
	return k.Provider + "/" + k.Model + "/" + k.Connection
}

type timestampedUsage struct {
	at     time.Time
	tokens int
}

// state holds in-memory counters and learned limits for a single Key.
type state struct {
	mu sync.Mutex

	// Learned ceilings (0 means unlearned/unlimited).
	learnedRPM int
	learnedRPD int
	learnedTPM int
	learnedTPD int

	// Active cooldown until this instant.
	cooldownUntil  time.Time
	cooldownReason string

	// Sliding usage history. Kept trimmed to 24h.
	history []timestampedUsage
}

// pruneLocked discards records older than 24h. Caller holds mu.
func (s *state) pruneLocked(now time.Time) {
	cutoff := now.Add(-24 * time.Hour)
	idx := 0
	for idx < len(s.history) && s.history[idx].at.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		s.history = s.history[idx:]
	}
}

// countsLocked computes (rpm, rpd, tpm, tpd) over sliding windows. Caller holds mu.
func (s *state) countsLocked(now time.Time) (rpm, rpd, tpm, tpd int) {
	s.pruneLocked(now)
	oneMinAgo := now.Add(-1 * time.Minute)
	for _, u := range s.history {
		// All history entries are <= 24h old thanks to pruneLocked.
		rpd++
		tpd += u.tokens
		if !u.at.Before(oneMinAgo) {
			rpm++
			tpm += u.tokens
		}
	}
	return
}

// Learner is the quota learning registry.
type Learner struct {
	mu     sync.RWMutex
	states map[string]*state
	opts   Options
	now    func() time.Time
	// repo is the optional SQLite persistence backend. When set, ceilings
	// learned and cooldowns set are saved, and first use of a key loads its
	// persisted state.
	repo *Repo
}

// New constructs a Learner with optional configuration.
func New(opts Options) *Learner {
	if opts.MaxCooldown <= 0 {
		opts.MaxCooldown = maxCooldown
	}
	return &Learner{
		states: make(map[string]*state),
		opts:   opts,
		now:    time.Now,
	}
}

// WithPersistence attaches an optional SQLite repo. Calling it after state has
// been touched is a no-op: it only configures future loads and saves.
func (l *Learner) WithPersistence(repo *Repo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.repo = repo
}

func (l *Learner) getState(k Key) *state {
	id := k.String()
	l.mu.RLock()
	st, ok := l.states[id]
	l.mu.RUnlock()
	if ok {
		return st
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if st, ok = l.states[id]; ok {
		return st
	}
	st = &state{}
	// Load persisted ceilings on first touch so a restart keeps what it
	// learned. Load zeroes the struct on a missing row.
	if l.repo != nil {
		if err := l.repo.Load(k, st); err != nil {
			// Fail-open: a broken persistence layer must not block routing.
			st = &state{}
		}
	}
	l.states[id] = st
	return st
}

// IsBlocked reports whether the (provider, model, connection) is currently
// forbidden from taking another request, and the human reason.
func (l *Learner) IsBlocked(provider, model, connID string) (blocked bool, reason string) {
	st := l.getState(Key{Provider: provider, Model: model, Connection: connID})
	st.mu.Lock()
	defer st.mu.Unlock()

	now := l.now()

	// 1. Cooldown check.
	if !st.cooldownUntil.IsZero() && now.Before(st.cooldownUntil) {
		remaining := st.cooldownUntil.Sub(now).Round(time.Second)
		return true, fmt.Sprintf("cooldown active (%s remaining): %s", remaining, st.cooldownReason)
	}

	// 2. Sliding window check against learned ceilings.
	rpm, rpd, tpm, tpd := st.countsLocked(now)

	if st.learnedRPM > 0 && rpm >= st.learnedRPM {
		return true, fmt.Sprintf("learned RPM ceiling reached (%d/%d)", rpm, st.learnedRPM)
	}
	if st.learnedRPD > 0 && rpd >= st.learnedRPD {
		return true, fmt.Sprintf("learned RPD ceiling reached (%d/%d)", rpd, st.learnedRPD)
	}
	if st.learnedTPM > 0 && tpm >= st.learnedTPM {
		return true, fmt.Sprintf("learned TPM ceiling reached (%d/%d)", tpm, st.learnedTPM)
	}
	if st.learnedTPD > 0 && tpd >= st.learnedTPD {
		return true, fmt.Sprintf("learned TPD ceiling reached (%d/%d)", tpd, st.learnedTPD)
	}

	return false, ""
}

// RecordOutcome records an HTTP response from an upstream provider,
// updating sliding windows, learning ceilings from headers, and triggering
// cooldowns on 429s.
// RecordOutcome records an HTTP response from an upstream provider,
// updating sliding windows, learning ceilings from rate-limit headers, and
// applying a cooldown when the upstream names an explicit Retry-After window.
//
// The learner deliberately does NOT invent a cooldown for a 429 that names no
// window: the combo loop already owns that decision (retryableCooldownSec + a
// bounded second pass inside brief429RetryTolerance), and a long default here
// would starve the router's own bounded retry. The learner's job is the
// proactive half — know the ceiling before the throttle — while the DB lock
// system owns the reactive half.
func (l *Learner) RecordOutcome(provider, model, connID string, statusCode int, headers http.Header, tokens int) {
	st := l.getState(Key{Provider: provider, Model: model, Connection: connID})
	st.mu.Lock()
	defer st.mu.Unlock()

	now := l.now()

	// Append usage record.
	st.history = append(st.history, timestampedUsage{at: now, tokens: tokens})
	st.pruneLocked(now)

	// Learn ceilings from standard rate limit headers if present.
	persist := false
	if headers != nil {
		if v := parseHeaderInt(headers, "x-ratelimit-limit-requests", "x-ratelimit-limit-requests-minute", "ratelimit-limit"); v > 0 && v != st.learnedRPM {
			st.learnedRPM = v
			persist = true
		}
		if v := parseHeaderInt(headers, "x-ratelimit-limit-requests-day"); v > 0 && v != st.learnedRPD {
			st.learnedRPD = v
			persist = true
		}
		if v := parseHeaderInt(headers, "x-ratelimit-limit-tokens", "x-ratelimit-limit-tokens-minute"); v > 0 && v != st.learnedTPM {
			st.learnedTPM = v
			persist = true
		}
		if v := parseHeaderInt(headers, "x-ratelimit-limit-tokens-day"); v > 0 && v != st.learnedTPD {
			st.learnedTPD = v
			persist = true
		}
	}

	// 429 handling: honor an explicit Retry-After window exactly. A 429 that
	// announces no window leaves the cooldown alone — the combo's own bounded
	// retry and the DB locks own that decision.
	if statusCode == http.StatusTooManyRequests && headers != nil {
		if retrySec := parseRetryAfter(headers.Get("Retry-After"), now); retrySec > 0 {
			if retrySec > l.opts.MaxCooldown {
				retrySec = l.opts.MaxCooldown
			}
			st.cooldownUntil = now.Add(retrySec)
			st.cooldownReason = fmt.Sprintf("429 too many requests (Retry-After %s)", retrySec)
			persist = true
		}
	}

	// Persist only what a restart must remember: learned ceilings and an
	// active cooldown. The sliding-window request history rebuilds from
	// observation and is never written.
	if l.repo != nil && persist {
		if err := l.repo.Save(Key{Provider: provider, Model: model, Connection: connID}, st); err != nil {
			// Fail-open: persistence errors never change request outcomes.
		}
	}
}

// Snapshot carries an immutable point-in-time view of a key's state.
type Snapshot struct {
	LearnedRPM    int
	LearnedRPD    int
	LearnedTPM    int
	LearnedTPD    int
	CooldownUntil time.Time
	CurrentRPM    int
	CurrentTPM    int
}

// Snapshot returns the current counters and learned ceilings for a key.
func (l *Learner) Snapshot(provider, model, connID string) Snapshot {
	st := l.getState(Key{Provider: provider, Model: model, Connection: connID})
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	rpm, _, tpm, _ := st.countsLocked(now)
	return Snapshot{
		LearnedRPM:    st.learnedRPM,
		LearnedRPD:    st.learnedRPD,
		LearnedTPM:    st.learnedTPM,
		LearnedTPD:    st.learnedTPD,
		CooldownUntil: st.cooldownUntil,
		CurrentRPM:    rpm,
		CurrentTPM:    tpm,
	}
}

// parseHeaderInt looks for the first matching header and parses a positive int.
func parseHeaderInt(h http.Header, names ...string) int {
	for _, n := range names {
		val := h.Get(n)
		if val == "" {
			continue
		}
		// Some headers format as "100, 100;window=60" or plain "100".
		fields := strings.Split(val, ",")
		first := strings.TrimSpace(fields[0])
		first = strings.Split(first, ";")[0]
		if n, err := strconv.Atoi(first); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// parseRetryAfter parses either seconds ("30") or an HTTP-date ("Wed, 21 Oct 2025 07:28:00 GMT").
func parseRetryAfter(val string, now time.Time) time.Duration {
	if val == "" {
		return 0
	}
	val = strings.TrimSpace(val)
	if sec, err := strconv.Atoi(val); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(val); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
