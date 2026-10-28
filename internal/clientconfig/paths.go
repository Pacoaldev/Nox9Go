package clientconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

// Path resolution for the clients an adapter knows.
//
// Every function here takes the home directory as an argument rather than
// reading it from the environment. That is what lets the adapter tests build a
// fixture in t.TempDir() and never touch the real ~/.claude: a resolver that
// calls os.UserHomeDir internally cannot be held to that.

// Paths is one client's resolved target files.
//
// Files is ordered: a client whose reset has to undo several writes depends on
// the order being the same one Apply used. Dir is the nearest directory the
// adapter is allowed to touch, which is what resolveGuard takes as its base —
// never $HOME itself, or containment would permit every path on the machine.
type Paths struct {
	Files []TargetFile
	Dir   string
}

// TargetFile is one file a client owns, with the base directory its guard is
// resolved against.
type TargetFile struct {
	// Path is absolute and fully resolved: no "~", no "$HOME", no unexpanded
	// variable.
	Path string
	// Dir is the nearest ancestor the adapter may touch, e.g. ~/.claude.
	Dir string
	// Format is how the file is parsed and spliced.
	Format Format
	// Purpose names what the file holds, for error messages that have to say
	// which of a two-file client failed.
	Purpose string
}

// homeDir resolves the operator's home directory.
//
// os.UserHomeDir reads $HOME on POSIX and %USERPROFILE% on Windows. It is the
// one place this package reads the environment for a path.
func homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if h == "" {
		return "", fmt.Errorf("resolve home directory: empty result")
	}
	return h, nil
}

// xdgConfigHome returns $XDG_CONFIG_HOME when set, else ~/.config.
//
// The layout stays the same on Windows (~/.config/<tool>) rather than switching
// to %APPDATA%, because the clients themselves do that. Following the client is
// the only way the file the user edits is the file this patches.
func xdgConfigHome(home string) string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	return filepath.Join(home, ".config")
}

// opencodePaths is ~/.config/opencode/opencode.jsonc when the user already has
// one there, else ~/.config/opencode/opencode.json.
//
// Both spellings load in OpenCode, and .json wins when both are present. The
// file that exists is the file the user actually edits, so that is the one
// patched: writing the sibling would create a second config that silently
// overrides it.
func opencodePaths(home string) Paths {
	dir := filepath.Join(xdgConfigHome(home), "opencode")
	path := filepath.Join(dir, "opencode.json")
	if jsonc := filepath.Join(dir, "opencode.jsonc"); fileExists(jsonc) {
		path = jsonc
	}
	return Paths{
		Dir:   dir,
		Files: []TargetFile{{Path: path, Dir: dir, Format: FormatJSONC, Purpose: "config"}},
	}
}

// fileExists reports whether p is an existing regular file. Any error is a
// "no": an unreadable path is not a file this should prefer.
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
