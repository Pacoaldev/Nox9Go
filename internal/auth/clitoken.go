package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/constants"
)

// cliAuthSalt mirrors upstream's CLI_TOKEN_SALT ("9r-cli-auth") in
// src/shared/utils/machineId.js: the dashboard CLI token is
// sha256(rawMachineID + salt + cliSecret).hex[:16].
const cliAuthSalt = "9r-cli-auth"

// CLIToken returns the local CLI token, deriving it exactly like upstream
// Next (machine-id + cli-secret files under DATA_DIR) so the Go server
// accepts the same x-9r-cli-token the CLI and the Next dashboard use.
func CLIToken() string {
	dir := config.ResolveDataDir()
	raw := readOrCreateHexFile(filepath.Join(dir, "machine-id"), 16)
	secret := readOrCreateHexFile(filepath.Join(dir, "auth", "cli-secret"), 32)
	sum := sha256.Sum256([]byte(raw + cliAuthSalt + secret))
	return hex.EncodeToString(sum[:])[:16]
}

// ValidCLIToken reports whether the presented CLI token matches this
// machine's derived token. Upstream compares against the derived value;
// presence alone must never authenticate, otherwise any remote caller could
// set an arbitrary header value and bypass the login gate.
func ValidCLIToken(provided string) bool {
	if provided == "" {
		return false
	}
	expected := CLIToken()
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

// readOrCreateHexFile loads a hex secret file, generating and persisting
// (0600) n random bytes on first use, mirroring upstream's loadRawMachineId
// / loadCliSecret persistence.
func readOrCreateHexFile(path string, n int) string {
	if data, err := os.ReadFile(path); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			return v
		}
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	v := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), constants.FilePermDir); err == nil {
		_ = os.WriteFile(path, []byte(v), constants.FilePermKey)
	}
	return v
}
