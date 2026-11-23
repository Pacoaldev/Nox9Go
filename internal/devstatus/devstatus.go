// Package devstatus reports where an ODD feature stands: the commits it landed,
// the work still uncommitted, and the tasks the feature document marks done.
//
// It exists so an operator away from the development machine can ask the
// Telegram bot "how is it going" and get the real answer. It reads the
// repository and never writes to it: every command it runs is a read-only git
// query, and the only files it opens are the ODD task documents.
package devstatus

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Repo is a checked-out git working tree. Root is the absolute path the queries
// run in; an empty Root makes every method report that no repository is
// configured instead of silently reading the process's own directory.
type Repo struct {
	Root string
}

// Snapshot is everything one report needs, gathered in a single pass so the
// numbers cannot disagree with each other.
type Snapshot struct {
	// RepoKnown is false when no repository is configured or git cannot read it.
	// The report then says so instead of showing empty values that look healthy.
	RepoKnown   bool
	Branch      string
	Head        string
	HeadSubject string
	HeadWhen    time.Time

	// Commits lists the feature's recent commits, newest first.
	Commits []Commit

	// Changes is the uncommitted work, which is what a paused session leaves
	// behind and what an operator most needs to see.
	Changes []Change

	// Feature is the ODD document being followed, when one is named.
	Feature *Feature

	// TakenAt is when the snapshot was read.
	TakenAt time.Time
}

// Commit is one line of history.
type Commit struct {
	Hash    string
	Subject string
	When    time.Time
}

// Change is one entry from `git status --porcelain`.
type Change struct {
	// Status is the two-letter porcelain code, e.g. "M", "A", "??".
	Status string
	Path   string
}

// Feature is a parsed ODD feature document.
type Feature struct {
	// Name is the document's file name without extension, e.g. "telegram-channel".
	Name string
	// Title is the document's H1.
	Title string
	// Tasks are the rows of the "## Tasks" table.
	Tasks []Task
	// Done and Total count tasks. A task is done when its Commit cell is not "—".
	Done  int
	Total int
}

// Task is one row of the feature's task table.
type Task struct {
	ID     string
	Specs  string
	Route  string
	Commit string
}

// Done reports whether the task has a commit recorded against it.
func (t Task) Done() bool {
	c := strings.TrimSpace(t.Commit)
	return c != "" && c != "—" && c != "-"
}

// ---------------------------------------------------------------------------
// Snapshot
// ---------------------------------------------------------------------------

// MaxCommits bounds the history a report prints. An operator reading this on a
// phone needs the last few, not the whole log.
const MaxCommits = 5

// Take reads the repository and, when feature is non-empty, the ODD document of
// that name under odd/tasks.
func (r Repo) Take(ctx context.Context, feature string) Snapshot {
	snap := Snapshot{TakenAt: time.Now()}

	if r.Root == "" {
		return snap
	}
	if _, err := r.git(ctx, "rev-parse", "--git-dir"); err != nil {
		return snap
	}
	snap.RepoKnown = true

	snap.Branch, _ = r.gitLine(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	snap.Head, _ = r.gitLine(ctx, "rev-parse", "--short", "HEAD")
	snap.HeadSubject, _ = r.gitLine(ctx, "log", "-1", "--pretty=%s")
	if raw, err := r.gitLine(ctx, "log", "-1", "--pretty=%ct"); err == nil {
		if secs, convErr := strconv.ParseInt(raw, 10, 64); convErr == nil {
			snap.HeadWhen = time.Unix(secs, 0)
		}
	}

	snap.Commits = r.commits(ctx, MaxCommits)
	snap.Changes = r.changes(ctx)

	if feature != "" {
		snap.Feature = r.loadFeature(feature)
	}
	return snap
}

func (r Repo) commits(ctx context.Context, limit int) []Commit {
	out, err := r.git(ctx, "log", "--pretty=%h\x1f%s\x1f%ct", "-n", strconv.Itoa(limit))
	if err != nil {
		return nil
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) != 3 {
			continue
		}
		c := Commit{Hash: parts[0], Subject: parts[1]}
		if secs, convErr := strconv.ParseInt(parts[2], 10, 64); convErr == nil {
			c.When = time.Unix(secs, 0)
		}
		commits = append(commits, c)
	}
	return commits
}

func (r Repo) changes(ctx context.Context) []Change {
	out, err := r.git(ctx, "status", "--porcelain")
	if err != nil {
		return nil
	}
	var changes []Change
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		changes = append(changes, Change{
			Status: strings.TrimSpace(line[:2]),
			Path:   strings.TrimSpace(line[3:]),
		})
	}
	return changes
}

// loadFeature parses odd/tasks/<name>.md. A missing document is not an error:
// the report simply carries no feature section.
func (r Repo) loadFeature(name string) *Feature {
	path := filepath.Join(r.Root, "odd", "tasks", name+".md")
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	feature := &Feature{Name: name}
	inTasks := false

	scanner := bufio.NewScanner(file)
	// A task table row carries a full route description, so the default 64 KB
	// token limit is generous but not unbounded.
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "# ") && feature.Title == "" {
			feature.Title = strings.TrimSpace(line[2:])
			continue
		}
		if strings.HasPrefix(line, "## ") {
			inTasks = strings.TrimSpace(line[3:]) == "Tasks"
			continue
		}
		if !inTasks {
			continue
		}
		task, ok := parseTaskRow(line)
		if !ok {
			task, ok = parseProseTaskLine(line)
		}
		if !ok {
			continue
		}
		feature.Tasks = append(feature.Tasks, task)
	}
	if err := scanner.Err(); err != nil {
		return feature
	}

	feature.Total = len(feature.Tasks)
	for _, task := range feature.Tasks {
		if task.Done() {
			feature.Done++
		}
	}
	return feature
}

// separatorRow matches the | :- | :---- | line that follows a markdown table
// header.
var separatorRow = regexp.MustCompile(`^\|[\s:|-]+\|$`)

// parseTaskRow reads one "| ID | Specs | Route | Commit |" line. The header row
// and the separator row are rejected, so a table with no tasks parses to none.
func parseTaskRow(line string) (Task, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return Task{}, false
	}
	if separatorRow.MatchString(line) {
		return Task{}, false
	}

	cells := strings.Split(strings.Trim(line, "|"), "|")
	if len(cells) < 4 {
		return Task{}, false
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}

	// The header row's first cell is the literal "ID"; a separator was already
	// rejected above, so this is the only remaining non-task row.
	if strings.EqualFold(cells[0], "ID") {
		return Task{}, false
	}
	// A data row always starts with a task identifier such as "T1".
	if !strings.HasPrefix(cells[0], "T") {
		return Task{}, false
	}

	commit := cells[3]
	if len(cells) >= 5 && !commitMarksDone(commit) && commitMarksDone(cells[4]) {
		commit = commitFromExtraCell(cells[4])
	}

	return Task{
		ID:     cells[0],
		Specs:  cells[1],
		Route:  cells[2],
		Commit: commit,
	}, true
}

var (
	proseTaskID    = regexp.MustCompile(`^(T\d+)\b`)
	proseCommitHex = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// parseProseTaskLine reads ODD task bullets and single-line specs (no pipe table).
func parseProseTaskLine(line string) (Task, bool) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "- ") {
		line = strings.TrimSpace(line[2:])
	}
	if line == "" || !strings.HasPrefix(line, "T") {
		return Task{}, false
	}
	m := proseTaskID.FindStringSubmatch(line)
	if m == nil {
		return Task{}, false
	}
	commit := proseTaskCommit(line)
	return Task{ID: m[1], Commit: commit}, true
}

func proseTaskCommit(line string) string {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "— done") || strings.Contains(lower, "- done") {
		return "done"
	}
	if idx := strings.Index(line, "Commit:"); idx >= 0 {
		rest := strings.TrimSpace(line[idx+len("Commit:"):])
		rest = strings.TrimRight(rest, ".")
		if rest != "" {
			return rest
		}
	}
	if h := proseCommitHex.FindString(line); h != "" {
		return h
	}
	return ""
}

func commitMarksDone(commit string) bool {
	commit = strings.TrimSpace(commit)
	return commit != "" && commit != "—" && commit != "-"
}

// commitFromExtraCell honours a trailing Status column that says "done".
func commitFromExtraCell(cell string) string {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(cell), "done") {
		if h := proseCommitHex.FindString(cell); h != "" {
			return h
		}
		return "done"
	}
	return cell
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// Render produces the text the operator reads in Telegram. It is deliberately
// plain: the channel splits at 4096 characters, so a long report degrades by
// being split rather than by being unreadable.
func (s Snapshot) Render() string {
	if !s.RepoKnown {
		return "No hay repositorio configurado, así que no puedo leer el avance."
	}

	var b strings.Builder

	if s.Feature != nil {
		fmt.Fprintf(&b, "Avance — %s\n", s.Feature.Name)
		if s.Feature.Total > 0 {
			fmt.Fprintf(&b, "Tareas: %d/%d completas\n", s.Feature.Done, s.Feature.Total)
			if pending := s.Feature.pendingTasks(); len(pending) > 0 {
				fmt.Fprintf(&b, "Siguiente: %s\n", pending[0].ID)
			}
		} else {
			b.WriteString("El documento no tiene tareas.\n")
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Rama: %s\n", fallback(s.Branch, "?"))
	if s.Head != "" {
		fmt.Fprintf(&b, "Último commit: %s %s\n", s.Head, s.HeadSubject)
		if !s.HeadWhen.IsZero() {
			fmt.Fprintf(&b, "Hace %s\n", humanSince(time.Since(s.HeadWhen)))
		}
	}

	if len(s.Commits) > 0 {
		b.WriteString("\nCommits recientes:\n")
		for _, c := range s.Commits {
			fmt.Fprintf(&b, "· %s %s\n", c.Hash, truncate(c.Subject, 60))
		}
	}

	b.WriteString("\n")
	switch len(s.Changes) {
	case 0:
		b.WriteString("Árbol limpio: todo lo hecho está commiteado.")
	default:
		fmt.Fprintf(&b, "Sin commitear (%d):\n", len(s.Changes))
		for i, ch := range s.Changes {
			// Twenty paths is already more than a phone screen; the count above
			// still tells the operator the real total.
			if i >= 20 {
				fmt.Fprintf(&b, "… y %d más\n", len(s.Changes)-20)
				break
			}
			fmt.Fprintf(&b, "· %s %s\n", ch.Status, truncate(ch.Path, 60))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func (f *Feature) pendingTasks() []Task {
	var pending []Task
	for _, task := range f.Tasks {
		if !task.Done() {
			pending = append(pending, task)
		}
	}
	return pending
}

// ListFeatures returns the ODD documents available under odd/tasks, sorted, so a
// command can report which features exist when none is named.
func (r Repo) ListFeatures() []string {
	if r.Root == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(r.Root, "odd", "tasks"))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".md"))
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------------------
// git plumbing
// ---------------------------------------------------------------------------

// git runs one read-only query. It is bounded by the caller's context and by a
// fixed timeout, because a hung git process must not stall the poll loop.
func (r Repo) git(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Root
	// A stale index lock would otherwise turn every report into an error.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("devstatus: git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func (r Repo) gitLine(ctx context.Context, args ...string) (string, error) {
	out, err := r.git(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]), nil
}

func fallback(value, whenEmpty string) string {
	if strings.TrimSpace(value) == "" {
		return whenEmpty
	}
	return value
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max < 2 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

// humanSince renders a duration the way an operator reads it, coarse on
// purpose: "hace 3 minutos" is the useful answer, not "hace 3m 12.4s".
func humanSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "menos de un minuto"
	case d < time.Hour:
		return fmt.Sprintf("%d minutos", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d horas", int(d.Hours()))
	default:
		return fmt.Sprintf("%d días", int(d.Hours()/24))
	}
}

// RenderHTML formats the report for a Telegram message.
//
// It is a second rendering rather than a replacement: the dashboard serves
// Render() as plain text, and a client that asked for text must not receive
// markup. Every value that comes from the repository — branch names, commit
// subjects, file paths, task titles — is escaped, because a commit subject
// containing a stray < would otherwise break the message or, worse, inject
// formatting the operator did not write. Telegram parses only the tags this
// function emits.
func (s Snapshot) RenderHTML() string {
	if !s.RepoKnown {
		return "No hay repositorio configurado, así que no puedo leer el avance."
	}

	var b strings.Builder

	if s.Feature != nil {
		fmt.Fprintf(&b, "<b>%s</b>\n", esc(s.Feature.Name))
		if s.Feature.Total > 0 {
			done, total := s.Feature.Done, s.Feature.Total
			fmt.Fprintf(&b, "%s <b>%d/%d</b> tareas\n", progressBar(done, total), done, total)
			if pending := s.Feature.pendingTasks(); len(pending) > 0 {
				fmt.Fprintf(&b, "Siguiente: <code>%s</code>\n", esc(pending[0].ID))
			}
		} else {
			b.WriteString("El documento no tiene tareas.\n")
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Rama: <code>%s</code>\n", esc(fallback(s.Branch, "?")))
	if s.Head != "" {
		fmt.Fprintf(&b, "Último: <code>%s</code> %s\n", esc(s.Head), esc(truncate(s.HeadSubject, 60)))
		if !s.HeadWhen.IsZero() {
			fmt.Fprintf(&b, "Hace %s\n", esc(humanSince(time.Since(s.HeadWhen))))
		}
	}

	if len(s.Commits) > 0 {
		b.WriteString("\n<b>Commits recientes</b>\n")
		for _, c := range s.Commits {
			fmt.Fprintf(&b, "· <code>%s</code> %s\n", esc(c.Hash), esc(truncate(c.Subject, 60)))
		}
	}

	b.WriteString("\n")
	switch len(s.Changes) {
	case 0:
		b.WriteString("✅ Árbol limpio, todo commiteado.")
	default:
		fmt.Fprintf(&b, "✏️ <b>Sin commitear (%d)</b>\n", len(s.Changes))
		for i, ch := range s.Changes {
			if i >= 20 {
				fmt.Fprintf(&b, "… y %d más\n", len(s.Changes)-20)
				break
			}
			fmt.Fprintf(&b, "· %s <code>%s</code>\n", esc(ch.Status), esc(truncate(ch.Path, 60)))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// progressBar renders completion as filled and empty blocks. A bar is one
// glanceable fact; without it the operator has to read two numbers and divide.
func progressBar(done, total int) string {
	if total <= 0 {
		return ""
	}
	const width = 8
	filled := done * width / total
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// esc escapes the three characters Telegram's HTML parser treats as markup.
func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
