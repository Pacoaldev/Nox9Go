// Package envfile exports a .env file into the process environment.
//
// Viper reads .env into its own store, so every lookup that goes through
// os.Getenv — the OAuth client credentials, the CLI flag defaults — never saw
// the file. That made "put it in .env" false for exactly the settings that
// document it. This bridges the two: it parses the same .env and exports it,
// leaving viper's own read untouched.
package envfile

import (
	"os"
	"path/filepath"
	"strings"
)

// Load exports the first .env it finds: the working directory, then the
// directory holding the executable. It is a no-op when neither exists, and
// returns only a real read/parse failure.
func Load() error {
	for _, path := range Candidates() {
		err := LoadFrom(path)
		if err == nil {
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Candidates lists the .env locations, most specific first. The executable
// directory comes second so a service or shortcut started from elsewhere still
// finds the file that sits next to the binary.
func Candidates() []string {
	paths := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), ".env"))
	}
	return paths
}

// LoadFrom parses one file and exports its keys. A key already present in the
// environment wins, so an explicit export always overrides the file. Malformed
// lines are skipped rather than failing the whole load: a stray comment should
// never stop the gateway from booting.
func LoadFrom(path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		key, value, ok := parseLine(line)
		if !ok {
			continue
		}
		if os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

// parseLine splits "KEY=value", tolerating a leading "export ", comments,
// blank lines, CRLF endings and one layer of matching quotes.
func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}
