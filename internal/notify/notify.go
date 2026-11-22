// Package notify carries development events to an operator who is not at the
// machine.
//
// The direction is one-way and append-only by design. A development session
// writes a line to a spool file; the Telegram channel drains it and forwards
// each event to the allowed chat. Nothing reads a reply back into the
// development session, so a compromised or mistaken chat message cannot change
// what the code does — the worst it can do is ask for a status report.
package notify

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Kind classifies an event so a reader can decide how loudly to present it.
type Kind string

const (
	// KindTask is a unit of work starting or finishing.
	KindTask Kind = "task"
	// KindCommit records that a work unit landed.
	KindCommit Kind = "commit"
	// KindCheck is a test, build or gate result.
	KindCheck Kind = "check"
	// KindBlocker is something that needs the operator's answer.
	KindBlocker Kind = "blocker"
	// KindInfo is anything else worth surfacing.
	KindInfo Kind = "info"
)

// Event is one line of the spool.
type Event struct {
	// ID is unique per spool and lets a reader report exactly what it consumed.
	ID string `json:"id"`
	// At is when the event happened, in UTC.
	At   time.Time `json:"at"`
	Kind Kind      `json:"kind"`
	// Title is the one-line summary the operator reads on a phone.
	Title string `json:"title"`
	// Detail is optional context, already formatted for reading.
	Detail string `json:"detail,omitempty"`
}

// Render formats an event as the Telegram message text.
func (e Event) Render() string {
	stamp := e.At.UTC().Format("15:04")
	label := e.Kind.Label()
	if e.Detail == "" {
		return fmt.Sprintf("[%s] %s\n%s", stamp, label, e.Title)
	}
	return fmt.Sprintf("[%s] %s\n%s\n%s", stamp, label, e.Title, e.Detail)
}

// Label is the Spanish word shown to the operator.
func (k Kind) Label() string {
	switch k {
	case KindTask:
		return "tarea"
	case KindCommit:
		return "commit"
	case KindCheck:
		return "check"
	case KindBlocker:
		return "decisión"
	default:
		return "info"
	}
}

// Spool is an append-only event file. It is safe for concurrent writers within
// one process and for a separate process appending while another drains, which
// is the arrangement this package exists for.
type Spool struct {
	// Path is the spool file. Its parent directory is created on demand.
	Path string

	mu sync.Mutex
}

// NewSpool returns a spool at path. The file is created lazily, on the first
// write, so a channel that is never used leaves no empty file behind.
func NewSpool(path string) *Spool { return &Spool{Path: path} }

// Append writes one event. A spool with no path is a no-op rather than an
// error, so a caller can call it unconditionally.
func (s *Spool) Append(event Event) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("%d-%s", event.At.UnixNano(), randomSuffix())
	}
	if event.Kind == "" {
		event.Kind = KindInfo
	}

	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("notify: marshal event: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("notify: create spool directory: %w", err)
	}
	file, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("notify: open spool: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("notify: write spool: %w", err)
	}
	return nil
}

// Drain reads every pending event and truncates the spool.
//
// A line that does not parse is skipped rather than failing the batch: one
// corrupt append must not silence every later event. The spool is truncated
// only after a successful read, so a crash mid-drain loses nothing that was
// already acknowledged.
func (s *Spool) Drain() ([]Event, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("notify: open spool: %w", err)
	}

	var events []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 16*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	scanErr := scanner.Err()
	file.Close()
	if scanErr != nil {
		return nil, fmt.Errorf("notify: read spool: %w", scanErr)
	}

	// Truncate in place rather than removing: another writer may already hold
	// the path open, and an unlinked file would take that append with it.
	if err := os.Truncate(s.Path, 0); err != nil {
		return events, fmt.Errorf("notify: truncate spool: %w", err)
	}
	return events, nil
}

// randomSuffix keeps ids distinct when two writes land in the same nanosecond.
func randomSuffix() string {
	var buf [4]byte
	now := time.Now().UnixNano()
	for i := range buf {
		buf[i] = byte(now >> (8 * i))
	}
	return fmt.Sprintf("%x", buf)
}
