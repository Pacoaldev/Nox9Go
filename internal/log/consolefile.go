package log

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Console log persistence.
//
// The in-memory ring buffer (consoleMaxLines) is what the dashboard streams
// live; it is lost the moment the process stops. An operator investigating a
// restart therefore has nothing left to read. This file adds an OPTIONAL,
// strictly bounded file sink that mirrors the same captured lines to disk with
// size-based rotation, so the dashboard can offer a download that survives a
// restart.
//
// It is deliberately opt-in (CONSOLE_LOG_FILE) and bounded: the default budget
// is consoleFileMaxBytes per file across consoleFileKeep files, so the worst
// case on a 1 vCPU router is a few MiB, never an unbounded log. Writes are
// buffered and flushed on a ticker so a busy gateway does not pay one syscall
// per log line. Nothing here runs unless the operator asked for it.

const (
	// consoleFileMaxBytes is the rotation threshold per file.
	consoleFileMaxBytes = 2 << 20 // 2 MiB
	// consoleFileKeep is how many rotated files are retained (excluding the
	// active one). 12 MiB worst case at the default threshold.
	consoleFileKeep = 3
	// consoleFileFlushEvery bounds how long a line can sit in the buffer.
	consoleFileFlushEvery = time.Second
)

type consoleFileSink struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
	file    *os.File
	buf     *bufio.Writer
	size    int64
	stop    chan struct{}
	done    chan struct{}
}

var (
	consoleFileMu sync.RWMutex
	consoleFile   *consoleFileSink
)

// captureConsoleFile mirrors one captured entry to the file sink when it is
// enabled. The line carries the same level prefix the terminal prints, plus an
// RFC3339 stamp so a downloaded log is readable without the dashboard.
func captureConsoleFile(entry ConsoleEntry) {
	consoleFileMu.RLock()
	sink := consoleFile
	consoleFileMu.RUnlock()
	if sink == nil {
		return
	}
	line := entry.Time.UTC().Format(time.RFC3339Nano) + " " + entry.Level.String() + " " + entry.Line + "\n"
	sink.write(line)
}

func (s *consoleFileSink) write(line string) {
	s.mu.Lock()
	if s.file == nil { // closed
		s.mu.Unlock()
		return
	}
	if s.size+int64(len(line)) > s.maxSize {
		s.rotateLocked()
	}
	if s.file == nil {
		s.mu.Unlock()
		return
	}
	n, err := s.buf.WriteString(line)
	if err != nil {
		s.mu.Unlock()
		return
	}
	s.size += int64(n)
	s.mu.Unlock()
}

// rotateLocked closes the active file, shifts the numbered backups and opens a
// fresh one. The caller holds s.mu.
func (s *consoleFileSink) rotateLocked() {
	s.flushLocked()
	s.closeLocked()

	// Shift path.N -> path.(N+1), oldest dropped.
	for i := s.keep; i >= 1; i-- {
		from := s.path + "." + strconv.Itoa(i)
		to := s.path + "." + strconv.Itoa(i+1)
		if i == s.keep {
			_ = os.Remove(from)
			continue
		}
		_ = os.Rename(from, to)
	}
	_ = os.Rename(s.path, s.path+".1")

	s.openLocked()
}

func (s *consoleFileSink) openLocked() {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		s.file = nil
		return
	}
	s.file = f
	s.buf = bufio.NewWriterSize(f, 32<<10)
	s.size = 0
}

func (s *consoleFileSink) flushLocked() {
	if s.buf != nil {
		_ = s.buf.Flush()
	}
}

func (s *consoleFileSink) closeLocked() {
	s.flushLocked()
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
		s.buf = nil
	}
}

// loop flushes on a ticker until the sink is closed, so a stalled process
// loses at most one interval of lines instead of everything buffered.
func (s *consoleFileSink) loop() {
	ticker := time.NewTicker(consoleFileFlushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			s.flushLocked()
			s.mu.Unlock()
		case <-s.stop:
			s.mu.Lock()
			s.closeLocked()
			s.mu.Unlock()
			close(s.done)
			return
		}
	}
}

// EnableConsoleFile turns on the rotating file sink. Calling it while a sink is
// already active is a no-op, so an env var and an explicit call cannot open two
// handles on the same path. After DisableConsoleFile it can be re-enabled.
func EnableConsoleFile(path string, maxBytes int64, keep int) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("log.EnableConsoleFile: empty path")
	}
	consoleFileMu.RLock()
	active := consoleFile != nil
	consoleFileMu.RUnlock()
	if active {
		return nil
	}
	return openConsoleFile(path, maxBytes, keep)
}

func openConsoleFile(path string, maxBytes int64, keep int) error {
	if maxBytes <= 0 {
		maxBytes = consoleFileMaxBytes
	}
	if keep < 0 {
		keep = 0
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("log.EnableConsoleFile: mkdir %s: %w", dir, err)
		}
	}
	sink := &consoleFileSink{
		path:    path,
		maxSize: maxBytes,
		keep:    keep,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	sink.mu.Lock()
	sink.openLocked()
	ok := sink.file != nil
	sink.mu.Unlock()
	if !ok {
		return fmt.Errorf("log.EnableConsoleFile: cannot open %s", path)
	}

	consoleFileMu.Lock()
	consoleFile = sink
	consoleFileMu.Unlock()

	go sink.loop()
	return nil
}

// DisableConsoleFile stops the sink and flushes what is buffered. Tests use it
// to leave no open handle behind.
func DisableConsoleFile() {
	consoleFileMu.Lock()
	sink := consoleFile
	consoleFile = nil
	consoleFileMu.Unlock()
	if sink == nil {
		return
	}
	select {
	case <-sink.stop:
	default:
		close(sink.stop)
	}
	<-sink.done
}

// FlushConsoleFile pushes any buffered lines to disk. The download handler
// calls it first so a log line emitted milliseconds before the request is not
// still sitting in the writer.
func FlushConsoleFile() {
	consoleFileMu.RLock()
	sink := consoleFile
	consoleFileMu.RUnlock()
	if sink == nil {
		return
	}
	sink.mu.Lock()
	sink.flushLocked()
	sink.mu.Unlock()
}

// ConsoleFileEnabled reports whether the file sink is active.
func ConsoleFileEnabled() bool {
	consoleFileMu.RLock()
	defer consoleFileMu.RUnlock()
	return consoleFile != nil
}

// ConsoleFilePaths returns the persisted console log files oldest-first, so a
// download replays the history in order. An empty slice means persistence is
// disabled or nothing has been written yet.
func ConsoleFilePaths() []string {
	consoleFileMu.RLock()
	sink := consoleFile
	consoleFileMu.RUnlock()
	if sink == nil {
		return nil
	}

	type numbered struct {
		path string
		n    int
	}
	entries, err := os.ReadDir(filepath.Dir(sink.path))
	if err != nil {
		return nil
	}
	base := filepath.Base(sink.path)
	var rotated []numbered
	active := ""
	for _, e := range entries {
		name := e.Name()
		if name == base {
			active = filepath.Join(filepath.Dir(sink.path), name)
			continue
		}
		if !strings.HasPrefix(name, base+".") {
			continue
		}
		if n, convErr := strconv.Atoi(strings.TrimPrefix(name, base+".")); convErr == nil {
			rotated = append(rotated, numbered{filepath.Join(filepath.Dir(sink.path), name), n})
		}
	}
	// Highest suffix is oldest: emit descending, then the active file last.
	sort.Slice(rotated, func(i, j int) bool { return rotated[i].n > rotated[j].n })
	paths := make([]string, 0, len(rotated)+1)
	for _, r := range rotated {
		if r.n >= 1 && r.n <= sink.keep {
			paths = append(paths, r.path)
		}
	}
	if active != "" {
		paths = append(paths, active)
	}
	return paths
}

// init wires the sink from CONSOLE_LOG_FILE when the operator set it. An empty
// value leaves persistence off; "off"/"false"/"0" disables it explicitly. The
// value is treated as a file path, and its directory is created on demand.
//
// Failures are never fatal: a log sink that cannot open must not stop the
// gateway, so the error goes to stderr and the in-memory buffer keeps working.
func init() {
	raw := strings.TrimSpace(os.Getenv("CONSOLE_LOG_FILE"))
	if raw == "" {
		return
	}
	switch strings.ToLower(raw) {
	case "off", "false", "0", "no":
		return
	}
	if err := EnableConsoleFile(raw, consoleFileMaxBytes, consoleFileKeep); err != nil {
		fmt.Fprintf(os.Stderr, "[log] warning: console file persistence disabled: %v\n", err)
		return
	}
	// Flush the tail on a graceful shutdown signal as well, in case the process
	// is stopped without calling DisableConsoleFile.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		DisableConsoleFile()
	}()
}
