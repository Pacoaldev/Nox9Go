package telegram

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadGentleDesktopBlockedQuestion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent", "desktop", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "schema":"gentle-shell.desktop-feed/v1",
  "updatedAt":"2026-11-23T23:45:47.216Z",
  "identity":{"cwd":"C:\\\\Projects\\\\Ouroboros"},
  "activity":{"state":"blocked","blockedLabel":"Choice awaiting input"},
  "pendingQuestion":{"question":"¿Mañana lloverá? SI o NO","options":[
    {"label":"SI","description":"Confirmo: mañana llueve."},
    {"label":"NO","description":"Confirmo: mañana no llueve."}
  ]}
}`
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GENTLE_SHELL_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)

	blocker, ok := readGentleDesktopBlockedQuestion()
	if !ok {
		t.Fatal("expected blocked question from desktop feed")
	}
	title, choices := blocker.Title, blocker.Choices
	if title != "¿Mañana lloverá? SI o NO" {
		t.Errorf("title = %q", title)
	}
	if len(choices) != 2 || choices[0].Label != "SI" || choices[1].Label != "NO" {
		t.Fatalf("choices = %+v", choices)
	}
}

func TestReadGentleDesktopBlockedQuestion_NotFilteredByHerdrExcludeCwd(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent", "desktop", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "NOX9GO")
	body := `{
  "schema":"gentle-shell.desktop-feed/v1",
  "updatedAt":"2026-11-23T23:45:47.216Z",
  "identity":{"cwd":"` + filepath.ToSlash(cwd) + `","agentDir":"` + filepath.ToSlash(filepath.Join(root, "agent")) + `"},
  "activity":{"state":"blocked"},
  "pendingQuestion":{"question":"pick one","toolCallId":"tc1","options":[
    {"label":"A","description":"first"},
    {"label":"B","description":"second"}
  ]}
}`
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GENTLE_SHELL_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("HERDR_AGENT_EXCLUDE_CWD", cwd)

	_, ok := readGentleDesktopBlockedQuestion()
	if !ok {
		t.Fatal("desktop feed blocker must not be dropped by HERDR_AGENT_EXCLUDE_CWD")
	}
}