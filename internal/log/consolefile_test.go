package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The file sink is opt-in and global; each test disables it on the way out so a
// later test does not inherit an open handle.
func TestConsoleFile_WritesCapturedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.log")
	if err := EnableConsoleFile(path, 1<<20, 1); err != nil {
		t.Fatalf("EnableConsoleFile: %v", err)
	}
	defer DisableConsoleFile()

	Info("file", "persisted line")
	DisableConsoleFile() // flush

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "persisted line") {
		t.Errorf("file missing captured line: %q", body)
	}
	if !strings.Contains(body, "info") {
		t.Errorf("file missing level token: %q", body)
	}
}

// Rotation must bound disk use: writing far past the threshold keeps the
// active file under the budget and produces numbered backups.
func TestConsoleFile_RotatesWithinBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	if err := EnableConsoleFile(path, 512, 2); err != nil {
		t.Fatalf("EnableConsoleFile: %v", err)
	}
	defer DisableConsoleFile()

	for range 200 {
		Info("file", "line padded to force rotation quickly ..................")
	}
	DisableConsoleFile()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least the active log file")
	}
	if len(entries) > 3 { // active + keep(2)
		t.Errorf("rotation kept too many files: %d", len(entries))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat active file: %v", err)
	}
	if info.Size() > 512 {
		t.Errorf("active file exceeded budget: %d > 512", info.Size())
	}
}

// Only the first EnableConsoleFile call takes effect, so an env-configured sink
// and a programmatic call cannot open two handles on one path.
func TestEnableConsoleFile_SecondCallIsNoOp(t *testing.T) {
	first := filepath.Join(t.TempDir(), "a.log")
	second := filepath.Join(t.TempDir(), "b.log")
	if err := EnableConsoleFile(first, 1<<20, 1); err != nil {
		t.Fatalf("EnableConsoleFile first: %v", err)
	}
	defer DisableConsoleFile()

	Info("file", "only first")
	if err := EnableConsoleFile(second, 1<<20, 1); err != nil {
		t.Fatalf("EnableConsoleFile second: %v", err)
	}
	DisableConsoleFile()

	if _, err := os.Stat(second); err == nil {
		t.Error("second EnableConsoleFile opened a second sink; should be a no-op")
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("ReadFile first: %v", err)
	}
	if !strings.Contains(string(data), "only first") {
		t.Errorf("first sink missing line: %q", string(data))
	}
}
