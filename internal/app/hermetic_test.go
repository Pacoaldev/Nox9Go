package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain redirects the home directory for the whole package: the guard that
// TestPackageHomeIsIsolated pins.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "nox9go-app-test-home")
	if err != nil {
		panic("app_test: isolated home: " + err.Error())
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// TestPackageHomeIsIsolated fails as soon as the home redirect below is
// dropped. Booting this package runs the production startup hook, and that hook
// writes the Pi model catalogue into the user's home directory
// (app/server.go -> ChatHandler.SyncPiCatalog -> clientconfig.SyncPiCatalog
// with an empty home). Without the guard a plain `go test ./internal/app/`
// rewrites the developer's ~/.pi/agent/models.json and
// ~/.gentle-shell/agent/models.json from a temporary database, truncating the
// live catalogue and dropping every combo row.
func TestPackageHomeIsIsolated(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	temp := filepath.Clean(os.TempDir())
	if !strings.HasPrefix(filepath.Clean(home), temp) {
		t.Fatalf("test home %q is not inside the OS temp dir %q: the startup hook would rewrite the real Pi model catalogue", home, temp)
	}
}
