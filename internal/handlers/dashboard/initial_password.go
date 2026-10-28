package dashboard

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/log"
)

// passwordGeneratedKey marks a stored password as the one generated at first
// start, so the dashboard can force the operator to pick their own before it
// hands out a session.
const passwordGeneratedKey = "passwordGenerated"

// EnsureInitialPassword sets a dashboard password on a fresh install and
// returns the plaintext so the caller can print it once.
//
// Before this, a fresh install accepted the dashboard password "123456" — the
// value every 9router install shares, written in this project\u2019s own README.
// Hiding it from the docs would not have helped: the behaviour was the problem,
// and anyone who had run the upstream project once already knew it. So the first
// start generates one instead, prints it, and the next login is forced to
// replace it (see mustChangePassword).
//
// A stored hash wins and nothing is printed. A hash of the old default counts as
// no password, so an install that upgraded is migrated here on its next start.
// initialPassword is what the operator asked for, or empty. It is a parameter
// rather than a lookup so the behaviour is testable and the caller stays the
// one place that reads configuration.
//
// It used to be ignored entirely: config parsed INITIAL_PASSWORD into
// Config.InitialPassword and nothing read the field, so an operator who set it
// still got a random password and the notice's own advice ("set
// INITIAL_PASSWORD to choose your own") was false. The end-to-end suite found
// it, because it boots the gateway with that variable and logs in.
func EnsureInitialPassword(repo *db.Repo, initialPassword string) (string, error) {
	raw, err := repo.GetSettingsRaw()
	if err != nil {
		return "", fmt.Errorf("read settings: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	if hash, _ := raw["password"].(string); hash != "" && !isKnownDefaultHash(hash) {
		return "", nil
	}

	password := strings.TrimSpace(initialPassword)
	generated := password == ""
	if generated {
		var err error
		if password, err = generateInitialPassword(); err != nil {
			return "", err
		}
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash initial password: %w", err)
	}
	// A password the operator chose is not the generated one, so it is not
	// forced to be replaced at the first login.
	if err := repo.UpdateSettingsRaw(map[string]any{
		"password":           string(hashed),
		passwordGeneratedKey: generated,
	}); err != nil {
		return "", fmt.Errorf("store initial password: %w", err)
	}
	if hash, _ := raw["password"].(string); hash != "" {
		log.Warn("dashboard", "the dashboard still used the well-known default password; a new one has been generated")
	}
	if !generated {
		// Nothing to print: the operator already knows their own password.
		return "", nil
	}
	return password, nil
}

// isGeneratedPassword reports whether the stored password is still the one
// EnsureInitialPassword generated and nobody has replaced yet.
//
// The marker is explicit rather than inferred from the hash: a generated
// password is a bcrypt hash like any other, so nothing about the stored value
// says "this one was handed out in a terminal and must be replaced".
func isGeneratedPassword(password string, raw map[string]any) bool {
	flag, _ := raw[passwordGeneratedKey].(bool)
	return flag && password != ""
}

// generateInitialPassword returns 24 base64url characters (144 bits) of
// crypto/rand output. URL-safe and punctuation-free so it survives a terminal,
// a copy-paste into the login field and a password manager without escaping.
func generateInitialPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// InitialPasswordNotice is the block printed on first start. It is deliberately
// loud and self-contained: it is the only place the password exists in
// plaintext, and an operator who misses it has to reset the install.
func InitialPasswordNotice(password, port string) string {
	if port == "" {
		port = "20130"
	}
	rule := strings.Repeat("=", 68)
	return strings.Join([]string{
		"",
		rule,
		"  NOX9GO generated the dashboard password for this install:",
		"",
		"      " + password,
		"",
		"  Open http://localhost:" + port + "/dashboard and sign in with it.",
		"  The dashboard will ask for a new password before it lets you in.",
		"  This is shown once. Set INITIAL_PASSWORD to choose your own instead.",
		rule,
		"",
	}, "\n")
}
