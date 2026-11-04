// Package modelhealth is an opt-in loop that probes the custom models an
// operator has configured, counts consecutive failures, and switches a model
// off in the disabledModels list once it has failed often enough to be
// considered dead. With autoEnable on it switches back on only the models it
// switched off itself.
//
// Ported in spirit from the reference gateway luqman-v1/9router-go
// (internal/modelhealth), trimmed to the loop itself: no scope setting, no
// dashboard hooks, no audit row. Decisions are logged.
//
// Guard rails: the loop is off by default, never probes a model an operator
// disabled by hand, never re-enables one it did not disable, and never counts
// a parked account (quota or rate limit) as evidence the model is dead.
package modelhealth

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"nox9go/proxy/internal/log"
)

// Settings key and KV scopes the loop owns.
const (
	SettingsKey   = "modelHealth"
	ScopeFailures = "modelHealth.failures"
	ScopeDisabled = "disabledModels"
)

// Defaults and bounds of the configuration.
const (
	DefaultIntervalMin = 60
	MinIntervalMin     = 5
	MaxIntervalMin     = 1440
	DefaultThreshold   = 3
	MinThreshold       = 2
	MaxThreshold       = 100
)

// Config is the parsed modelHealth settings value.
type Config struct {
	Enabled     bool
	IntervalMin int
	Threshold   int
	AutoEnable  bool
}

// ParseConfig reads the settings value leniently. Anything unreadable yields
// the default, which is off: a damaged row must not start probing models.
func ParseConfig(value any) Config {
	cfg := Config{IntervalMin: DefaultIntervalMin, Threshold: DefaultThreshold}
	obj, ok := value.(map[string]any)
	if !ok {
		return cfg
	}
	cfg.Enabled, _ = obj["enabled"].(bool)
	cfg.AutoEnable, _ = obj["autoEnable"].(bool)
	if n, ok := obj["intervalMin"].(float64); ok {
		cfg.IntervalMin = min(max(int(n), MinIntervalMin), MaxIntervalMin)
	}
	if n, ok := obj["threshold"].(float64); ok {
		cfg.Threshold = min(max(int(n), MinThreshold), MaxThreshold)
	}
	return cfg
}

// Interval is the pause between passes.
func (c Config) Interval() time.Duration { return time.Duration(c.IntervalMin) * time.Minute }

// Verdict is what one probe answered.
type Verdict struct {
	OK bool
	// Blocked means the account is parked and nothing was asked of the model.
	Blocked bool
	Error   string
}

// Candidate is one provider/model pair a pass may probe. Provider is the
// storage alias the disabledModels list is keyed by.
type Candidate struct {
	Provider string
	Model    string
}

// Store is the persistence the loop needs.
type Store interface {
	// GetKVScope returns every key of one scope.
	GetKVScope(scope string) (map[string]string, error)
	SetKV(scope, key, value string) error
}

type failures struct {
	Count               int  `json:"count"`
	DisabledByScheduler bool `json:"disabledByScheduler,omitempty"`
}

// Loop runs the model-health passes.
type Loop struct {
	store      Store
	probe      func(ctx context.Context, c Candidate) Verdict
	candidates func() []Candidate
	config     func() Config
}

// NewLoop builds a loop. A nil dependency yields a loop whose Run does nothing.
func NewLoop(store Store, probe func(ctx context.Context, c Candidate) Verdict, candidates func() []Candidate, config func() Config) *Loop {
	return &Loop{store: store, probe: probe, candidates: candidates, config: config}
}

// Run passes until ctx ends. The configuration is re-read every cycle, so
// switching the feature on in settings takes effect without a restart.
func (l *Loop) Run(ctx context.Context) {
	if l.store == nil || l.probe == nil || l.candidates == nil || l.config == nil {
		return
	}
	for {
		cfg := l.config()
		wait := 30 * time.Second
		if cfg.Enabled {
			l.Pass(ctx, cfg)
			wait = cfg.Interval()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Pass runs one sweep over the candidates.
func (l *Loop) Pass(ctx context.Context, cfg Config) {
	for _, c := range l.candidates() {
		if ctx.Err() != nil {
			return
		}
		if c.Provider == "" || c.Model == "" {
			continue
		}
		l.evaluate(ctx, cfg, c)
	}
}

func (l *Loop) evaluate(ctx context.Context, cfg Config, c Candidate) {
	disabled, err := l.disabledList(c.Provider)
	if err != nil {
		log.Warn("modelhealth", "read disabled list", "provider", c.Provider, "error", err)
		return
	}
	state := l.loadFailures(c)
	isOff := slices.Contains(disabled, c.Model)
	// A model switched off by an operator stays theirs: no probe, no quota spent.
	if isOff && !state.DisabledByScheduler {
		return
	}
	// With auto-enable off nothing can change for a model we already disabled.
	if isOff && !cfg.AutoEnable {
		return
	}

	v := l.probe(ctx, c)
	switch {
	case v.Blocked:
		// A parked account says nothing about the model.
		return
	case v.OK:
		l.onSuccess(c, state, disabled, isOff)
	default:
		l.onFailure(c, state, disabled, isOff, cfg.Threshold, v.Error)
	}
}

func (l *Loop) onSuccess(c Candidate, state failures, disabled []string, isOff bool) {
	if isOff {
		next := slices.DeleteFunc(slices.Clone(disabled), func(id string) bool { return id == c.Model })
		if err := l.saveDisabled(c.Provider, next); err != nil {
			log.Warn("modelhealth", "save disabled list", "provider", c.Provider, "error", err)
			return
		}
		log.Info("modelhealth", "model re-enabled", "provider", c.Provider, "model", c.Model)
	}
	l.saveFailures(c, failures{})
}

func (l *Loop) onFailure(c Candidate, state failures, disabled []string, isOff bool, threshold int, reason string) {
	if isOff {
		return
	}
	state.Count++
	if state.Count >= threshold {
		next := append(slices.Clone(disabled), c.Model)
		if err := l.saveDisabled(c.Provider, next); err != nil {
			log.Warn("modelhealth", "save disabled list", "provider", c.Provider, "error", err)
			return
		}
		state.DisabledByScheduler = true
		log.Info("modelhealth", "model disabled", "provider", c.Provider, "model", c.Model,
			"failures", state.Count, "reason", truncate(reason, 120))
	}
	l.saveFailures(c, state)
}

func (l *Loop) disabledList(provider string) ([]string, error) {
	all, err := l.store.GetKVScope(ScopeDisabled)
	if err != nil {
		return nil, err
	}
	var ids []string
	if raw := strings.TrimSpace(all[provider]); raw != "" {
		// A malformed row reads as empty: the loop is about to rewrite it.
		_ = json.Unmarshal([]byte(raw), &ids)
	}
	return ids, nil
}

func (l *Loop) saveDisabled(provider string, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return l.store.SetKV(ScopeDisabled, provider, string(raw))
}

func failureKey(c Candidate) string { return c.Provider + "/" + c.Model }

func (l *Loop) loadFailures(c Candidate) failures {
	var f failures
	all, err := l.store.GetKVScope(ScopeFailures)
	if err != nil {
		return f
	}
	if raw := all[failureKey(c)]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &f)
	}
	f.Count = max(f.Count, 0)
	return f
}

func (l *Loop) saveFailures(c Candidate, f failures) {
	raw, err := json.Marshal(f)
	if err != nil {
		return
	}
	if err := l.store.SetKV(ScopeFailures, failureKey(c), string(raw)); err != nil {
		log.Warn("modelhealth", "write counter", "provider", c.Provider, "model", c.Model, "error", err)
	}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}