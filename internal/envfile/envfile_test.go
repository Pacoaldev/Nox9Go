package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	return path
}

func TestLoadFromExportsKeys(t *testing.T) {
	t.Setenv("NOX_TEST_PLAIN", "")
	t.Setenv("NOX_TEST_QUOTED", "")
	t.Setenv("NOX_TEST_EXPORTED", "")
	t.Setenv("NOX_TEST_CRLF", "")

	path := writeEnv(t, "# comment\n\nNOX_TEST_PLAIN=value\nNOX_TEST_QUOTED=\"quoted value\"\nexport NOX_TEST_EXPORTED=single\nNOX_TEST_CRLF=crlf\r\n")
	if err := LoadFrom(path); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	for key, want := range map[string]string{
		"NOX_TEST_PLAIN":    "value",
		"NOX_TEST_QUOTED":   "quoted value",
		"NOX_TEST_EXPORTED": "single",
		"NOX_TEST_CRLF":     "crlf",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// An explicit export must win over the file, otherwise a deployment that sets
// the value in its service definition could never override a stale .env.
func TestLoadFromKeepsExistingEnvironment(t *testing.T) {
	t.Setenv("NOX_TEST_PRECEDENCE", "from-process")

	if err := LoadFrom(writeEnv(t, "NOX_TEST_PRECEDENCE=from-file\n")); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := os.Getenv("NOX_TEST_PRECEDENCE"); got != "from-process" {
		t.Errorf("got %q, want the process value", got)
	}
}

func TestLoadFromSkipsMalformedLines(t *testing.T) {
	t.Setenv("NOX_TEST_AFTER_GARBAGE", "")

	path := writeEnv(t, "not a pair\n=orphan value\nNOX_TEST_AFTER_GARBAGE=ok\n")
	if err := LoadFrom(path); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := os.Getenv("NOX_TEST_AFTER_GARBAGE"); got != "ok" {
		t.Errorf("malformed lines stopped the parse: got %q", got)
	}
}

func TestLoadIgnoresMissingFile(t *testing.T) {
	if err := LoadFrom(filepath.Join(t.TempDir(), "absent")); !os.IsNotExist(err) {
		t.Fatalf("want a not-exist error, got %v", err)
	}
}
