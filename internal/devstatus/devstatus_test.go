package devstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo builds a real throwaway git repository. The package's whole job is to
// read git, so a fake git would test the fake and not the parser.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "test")
	return dir
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	writeFile(t, dir, name, content)
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-q", "-m", message)
}

// S1: an unconfigured repository reports itself as unknown instead of showing
// empty values that look healthy.
func TestTake_NoRootIsUnknown(t *testing.T) {
	snap := Repo{}.Take(context.Background(), "")

	if snap.RepoKnown {
		t.Errorf("RepoKnown = true with no root, want false")
	}
	if got := snap.Render(); !strings.Contains(got, "No hay repositorio") {
		t.Errorf("Render() = %q, want it to say no repository is configured", got)
	}
}

// S1: a directory that is not a git work tree is also unknown, not an empty
// report.
func TestTake_NonGitDirectoryIsUnknown(t *testing.T) {
	snap := Repo{Root: t.TempDir()}.Take(context.Background(), "")

	if snap.RepoKnown {
		t.Errorf("RepoKnown = true outside a work tree, want false")
	}
}

// S1: the snapshot carries the branch, the head commit and the recent history.
func TestTake_ReadsBranchHeadAndCommits(t *testing.T) {
	dir := newRepo(t)
	commitFile(t, dir, "a.txt", "one", "feat: first")
	commitFile(t, dir, "b.txt", "two", "feat: second")

	snap := Repo{Root: dir}.Take(context.Background(), "")

	if !snap.RepoKnown {
		t.Fatalf("RepoKnown = false for a real work tree")
	}
	if snap.Branch == "" {
		t.Errorf("Branch is empty")
	}
	if snap.HeadSubject != "feat: second" {
		t.Errorf("HeadSubject = %q, want the newest commit", snap.HeadSubject)
	}
	if len(snap.Commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(snap.Commits))
	}
	if snap.Commits[0].Subject != "feat: second" {
		t.Errorf("Commits[0] = %q, want newest first", snap.Commits[0].Subject)
	}
}

// S2: uncommitted work is what a paused session leaves behind, so it must be
// reported with its count.
func TestTake_ReportsUncommittedWork(t *testing.T) {
	dir := newRepo(t)
	commitFile(t, dir, "a.txt", "one", "feat: first")
	writeFile(t, dir, "a.txt", "changed")
	writeFile(t, dir, "untracked.txt", "new")

	snap := Repo{Root: dir}.Take(context.Background(), "")

	if len(snap.Changes) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(snap.Changes), snap.Changes)
	}
	rendered := snap.Render()
	if !strings.Contains(rendered, "Sin commitear (2)") {
		t.Errorf("Render() = %q, want the uncommitted count", rendered)
	}
}

// S2: a clean tree says so, because "nothing pending" is the reassuring answer
// and an empty section reads as a bug.
func TestTake_CleanTree(t *testing.T) {
	dir := newRepo(t)
	commitFile(t, dir, "a.txt", "one", "feat: first")

	rendered := Repo{Root: dir}.Take(context.Background(), "").Render()

	if !strings.Contains(rendered, "Árbol limpio") {
		t.Errorf("Render() = %q, want it to report a clean tree", rendered)
	}
}

// S3: the feature document's task table is parsed and its completion counted
// from the commit column, which is the one signal an ODD document carries.
func TestTake_ParsesFeatureTasks(t *testing.T) {
	dir := newRepo(t)
	commitFile(t, dir, "a.txt", "one", "feat: first")
	writeFile(t, dir, "odd/tasks/demo.md", `# Demo feature

Header prose that is not a task.

## Specs

**S1 — something.** Text.

## Tasks

| ID | Specs | Route | Commit |
| :- | :---- | :---- | :----- |
| T1 | S1 | do the first thing | abc1234 |
| T2 | S2 | do the second thing | — |
| T3 | S3 | do the third thing | — |

## Log
`)

	snap := Repo{Root: dir}.Take(context.Background(), "demo")

	if snap.Feature == nil {
		t.Fatalf("Feature is nil for an existing document")
	}
	if snap.Feature.Title != "Demo feature" {
		t.Errorf("Title = %q", snap.Feature.Title)
	}
	if snap.Feature.Total != 3 {
		t.Fatalf("Total = %d, want 3", snap.Feature.Total)
	}
	if snap.Feature.Done != 1 {
		t.Errorf("Done = %d, want 1", snap.Feature.Done)
	}

	rendered := snap.Render()
	if !strings.Contains(rendered, "Tareas: 1/3 completas") {
		t.Errorf("Render() = %q, want the task progress line", rendered)
	}
	if !strings.Contains(rendered, "Siguiente: T2") {
		t.Errorf("Render() = %q, want the next pending task", rendered)
	}
}

// S3: a missing document is not an error, and a document with no task table
// parses to zero rather than to garbage rows.
func TestLoadFeature_EdgeCases(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "odd/tasks/empty.md", "# Empty\n\nNo tasks here.\n")

	tests := []struct {
		name    string
		feature string
		wantNil bool
		wantLen int
	}{
		{name: "missing document", feature: "does-not-exist", wantNil: true},
		{name: "document without a table", feature: "empty", wantNil: false, wantLen: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Repo{Root: dir}.loadFeature(tt.feature)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("loadFeature() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("loadFeature() = nil, want a parsed document")
			}
			if len(got.Tasks) != tt.wantLen {
				t.Errorf("got %d tasks, want %d", len(got.Tasks), tt.wantLen)
			}
		})
	}
}

func TestParseTaskRow_StatusColumnMarksDone(t *testing.T) {
	line := "| T1 | S1 | inline | — | done — `keyboard.go` |"
	task, ok := parseTaskRow(line)
	if !ok {
		t.Fatal("parseTaskRow returned false")
	}
	if !task.Done() {
		t.Fatalf("commit %q should count as done", task.Commit)
	}
}

func TestLoadFeature_OperatorTaskDocs(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "odd", "tasks")); err != nil {
		t.Skip("odd/tasks not present")
	}

	want := map[string]struct{ done, total int }{
		"telegram-dock":              {6, 6},
		"telegram-images":            {5, 5},
		"tokensaver-qoder-pool-sync":   {6, 6},
		"telegram-modes":             {4, 4},
		"telegram-agent-exec":        {4, 4},
		"mcp-image-generation":       {6, 6},
	}
	for name, w := range want {
		f := Repo{Root: root}.loadFeature(name)
		if f == nil {
			t.Fatalf("%s: missing document", name)
		}
		if f.Total != w.total {
			t.Errorf("%s: total=%d want %d", name, f.Total, w.total)
		}
		if f.Done != w.done {
			t.Errorf("%s: done=%d want %d (commit column still —?)", name, f.Done, w.done)
		}
	}
}

func TestParseProseTaskLine_formats(t *testing.T) {
	tests := []struct {
		line string
		want bool
		done bool
	}{
		{line: "- T1 [S1] work inline — done", want: true, done: true},
		{line: "T1 (S4) foo. Commit: feat(telegram): wire.", want: true, done: true},
		{line: "E2 (verify): tests ok. Committed 9c2270bf.", want: false, done: false},
	}
	for _, tt := range tests {
		t.Run(tt.line[:12], func(t *testing.T) {
			task, ok := parseProseTaskLine(tt.line)
			if ok != tt.want {
				t.Fatalf("ok=%v want %v", ok, tt.want)
			}
			if !tt.want {
				return
			}
			if task.Done() != tt.done {
				t.Fatalf("Done()=%v commit=%q", task.Done(), task.Commit)
			}
		})
	}
}

// A commit cell of "—" is not a commit. Treating the placeholder as a value
// would report 100% complete the moment a document was written.
func TestTask_Done(t *testing.T) {
	tests := []struct {
		name   string
		commit string
		want   bool
	}{
		{name: "short hash", commit: "abc1234", want: true},
		{name: "em dash placeholder", commit: "—", want: false},
		{name: "hyphen placeholder", commit: "-", want: false},
		{name: "empty", commit: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Task{Commit: tt.commit}).Done(); got != tt.want {
				t.Errorf("Done() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The task list on the phone must stay short: a report that prints every path
// of a large diff is unreadable where it is actually read.
func TestRender_TruncatesLongChangeLists(t *testing.T) {
	dir := newRepo(t)
	commitFile(t, dir, "a.txt", "one", "feat: first")
	for i := 0; i < 25; i++ {
		writeFile(t, dir, "f"+string(rune('a'+i))+".txt", "x")
	}

	rendered := Repo{Root: dir}.Take(context.Background(), "").Render()

	if !strings.Contains(rendered, "… y 5 más") {
		t.Errorf("Render() = %q, want it to hide the overflow past 20 paths", rendered)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "short stays", in: "abc", max: 10, want: "abc"},
		{name: "long is cut with an ellipsis", in: "abcdef", max: 4, want: "abc…"},
		{name: "counts runes not bytes", in: "áéíóú", max: 3, want: "áé…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.max); got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

func TestListFeatures(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "odd/tasks/b-second.md", "# B\n")
	writeFile(t, dir, "odd/tasks/a-first.md", "# A\n")
	writeFile(t, dir, "odd/tasks/notes.txt", "not a feature")

	got := Repo{Root: dir}.ListFeatures()

	want := []string{"a-first", "b-second"}
	if len(got) != len(want) {
		t.Fatalf("ListFeatures() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListFeatures()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The Telegram report is formatted, and a repository value that contains
// markup characters must travel escaped: an unescaped "<" either breaks the
// message or injects formatting the operator never wrote.
func TestRenderHTML_EscapesRepositoryValues(t *testing.T) {
	snap := Snapshot{
		RepoKnown:   true,
		Branch:      "feat/a<b",
		Head:        "abc1234",
		HeadSubject: "fix: compare a < b & keep it",
		Feature:     &Feature{Name: "telegram-channel", Total: 7, Done: 7},
		Changes:     []Change{{Status: "M", Path: "web/src/a<b.svelte"}},
	}

	got := snap.RenderHTML()

	if strings.Contains(got, "a<b") || strings.Contains(got, "& keep") {
		t.Errorf("an unescaped repository value reached the message:\n%s", got)
	}
	if !strings.Contains(got, "a&lt;b") || !strings.Contains(got, "&amp;") {
		t.Errorf("expected escaped values:\n%s", got)
	}
	// Formatting that this package owns is present.
	for _, want := range []string{"<b>telegram-channel</b>", "<code>abc1234</code>", "████████", "7/7"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
