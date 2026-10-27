package executor

import (
	"context"
	"testing"
	"time"
)

func TestCodebuddyPacerReserve(t *testing.T) {
	now := time.Unix(1000, 0)
	p := &codebuddyPacer{next: map[string]time.Time{}, gap: time.Second, now: func() time.Time { return now }}

	if d := p.reserve("a"); d != 0 {
		t.Fatalf("first reserve = %v, want 0", d)
	}
	if d := p.reserve("a"); d != time.Second {
		t.Errorf("second reserve same key = %v, want 1s", d)
	}
	if d := p.reserve("a"); d != 2*time.Second {
		t.Errorf("third reserve same key = %v, want 2s", d)
	}
	if d := p.reserve("b"); d != 0 {
		t.Errorf("other key = %v, want 0", d)
	}
	now = now.Add(10 * time.Second)
	if d := p.reserve("a"); d != 0 {
		t.Errorf("after idle = %v, want 0", d)
	}
}

func TestCodebuddyPacerWaitHonoursContext(t *testing.T) {
	p := &codebuddyPacer{next: map[string]time.Time{}, gap: time.Hour, now: time.Now}
	if err := p.wait(context.Background(), "k"); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.wait(ctx, "k"); err == nil {
		t.Fatal("expected context error on cancelled wait")
	}
}
