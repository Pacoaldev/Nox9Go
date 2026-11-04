package modelhealth

import (
	"context"
	"encoding/json"
	"testing"
)

type memStore map[string]map[string]string

func (m memStore) GetKVScope(scope string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range m[scope] {
		out[k] = v
	}
	return out, nil
}

func (m memStore) SetKV(scope, key, value string) error {
	if m[scope] == nil {
		m[scope] = map[string]string{}
	}
	m[scope][key] = value
	return nil
}

func disabledOf(t *testing.T, s memStore, provider string) []string {
	t.Helper()
	var ids []string
	if raw := s[ScopeDisabled][provider]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			t.Fatalf("bad disabled row %q: %v", raw, err)
		}
	}
	return ids
}

func newLoop(s memStore, v *Verdict, calls *int) *Loop {
	c := Candidate{Provider: "ag", Model: "m1"}
	return NewLoop(s, func(context.Context, Candidate) Verdict { *calls++; return *v },
		func() []Candidate { return []Candidate{c} }, func() Config { return ParseConfig(nil) })
}

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want Config
	}{
		{"missing is off", nil, Config{IntervalMin: 60, Threshold: 3}},
		{"values read", map[string]any{"enabled": true, "intervalMin": 15.0, "threshold": 5.0, "autoEnable": true},
			Config{Enabled: true, IntervalMin: 15, Threshold: 5, AutoEnable: true}},
		{"clamped", map[string]any{"enabled": true, "intervalMin": 1.0, "threshold": 1.0},
			Config{Enabled: true, IntervalMin: MinIntervalMin, Threshold: MinThreshold}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseConfig(tt.in); got != tt.want {
				t.Errorf("ParseConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDisablesOnlyAfterConsecutiveFailures(t *testing.T) {
	s, v, calls := memStore{}, &Verdict{Error: "502"}, 0
	l := newLoop(s, v, &calls)
	cfg := ParseConfig(nil)

	l.Pass(context.Background(), cfg)
	l.Pass(context.Background(), cfg)
	if got := disabledOf(t, s, "ag"); len(got) != 0 {
		t.Fatalf("disabled after 2 failures: %v", got)
	}
	*v = Verdict{OK: true}
	l.Pass(context.Background(), cfg) // success resets the streak
	*v = Verdict{Error: "502"}
	l.Pass(context.Background(), cfg)
	l.Pass(context.Background(), cfg)
	if got := disabledOf(t, s, "ag"); len(got) != 0 {
		t.Fatalf("non-consecutive failures disabled the model: %v", got)
	}
	l.Pass(context.Background(), cfg)
	if got := disabledOf(t, s, "ag"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("disabled = %v, want [m1]", got)
	}
}

func TestBlockedProbeIsNotAFailure(t *testing.T) {
	s, v, calls := memStore{}, &Verdict{Blocked: true, Error: "quota"}, 0
	l := newLoop(s, v, &calls)
	for i := 0; i < 10; i++ {
		l.Pass(context.Background(), ParseConfig(nil))
	}
	if got := disabledOf(t, s, "ag"); len(got) != 0 {
		t.Fatalf("blocked account disabled the model: %v", got)
	}
}

func TestOperatorDisabledModelIsNotProbed(t *testing.T) {
	s := memStore{ScopeDisabled: {"ag": `["m1"]`}}
	v, calls := &Verdict{OK: true}, 0
	l := newLoop(s, v, &calls)
	l.Pass(context.Background(), Config{Enabled: true, IntervalMin: 60, Threshold: 3, AutoEnable: true})
	if calls != 0 {
		t.Fatalf("probed an operator-disabled model %d times", calls)
	}
	if got := disabledOf(t, s, "ag"); len(got) != 1 {
		t.Fatalf("operator's disable was undone: %v", got)
	}
}

func TestAutoEnableOnlyUndoesSchedulerDisable(t *testing.T) {
	s, v, calls := memStore{}, &Verdict{Error: "502"}, 0
	l := newLoop(s, v, &calls)
	cfg := Config{Enabled: true, IntervalMin: 60, Threshold: 2, AutoEnable: true}
	l.Pass(context.Background(), cfg)
	l.Pass(context.Background(), cfg)
	if got := disabledOf(t, s, "ag"); len(got) != 1 {
		t.Fatalf("setup: model not disabled: %v", got)
	}

	cfg.AutoEnable = false
	*v = Verdict{OK: true}
	before := calls
	l.Pass(context.Background(), cfg)
	if calls != before || len(disabledOf(t, s, "ag")) != 1 {
		t.Fatal("auto-enable off must leave a disabled model alone without probing")
	}

	cfg.AutoEnable = true
	l.Pass(context.Background(), cfg)
	if got := disabledOf(t, s, "ag"); len(got) != 0 {
		t.Fatalf("recovered model not re-enabled: %v", got)
	}
}