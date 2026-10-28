package media

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"nox9go/proxy/internal/clientconfig"
)

// Configuration detection for CLI clients that are not Hermes.
//
// The question the dashboard badge asks is "does this client route through this
// gateway?", and the answer is a property of the *endpoint*, not of the
// provider's name. An earlier version looked for a literal provider key
// ("9router"), which broke the moment the product was renamed and never matched
// a user who had called their provider anything else. Reading the base URL and
// comparing hosts instead makes the check independent of both the name and the
// fork.
//
// Each reader is best-effort: a missing, unreadable or unparsable file means
// "not configured", never an error. Detection runs on every dashboard load and
// must not fail because a user is halfway through editing a file.

// ProviderKey is the provider id the adapters write. Exported here so the
// detection side and the write side cannot drift apart.
const ProviderKey = clientconfig.ProviderKey

// gatewayPort is the port this gateway listens on by default. A client pointing
// at a different port on loopback is talking to something else.
const gatewayPort = "20130"

// opencodeHasNox9Go reports whether any provider in OpenCode's config points at
// this gateway.
//
// Both spellings are read, because OpenCode loads both and the user may have
// either: opencode.json and opencode.jsonc. Only the provider entries matter;
// the active model and the subagent keys describe what is selected, not where
// it goes.
func opencodeHasNox9Go() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	dir := filepath.Join(home, ".config", "opencode")
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if opencodeConfigTargetsGateway(raw) {
			return true
		}
	}
	return false
}

// opencodeConfigTargetsGateway scans the raw config text for a baseURL that
// resolves to this gateway.
//
// The scan is deliberately tolerant: it looks at every quoted value that
// follows "baseURL", wherever it sits, rather than walking
// provider→each→options. A config that nests providers differently, or that
// this build does not model, still counts when it points here — and a false
// positive is a badge, not a write.
func opencodeConfigTargetsGateway(raw []byte) bool {
	s := string(raw)
	for {
		i := strings.Index(s, "baseURL")
		if i < 0 {
			return false
		}
		rest := s[i+len("baseURL")+1:] // step past the closing quote of the key
		if candidate, ok := afterColonString(rest); ok && isLocalGatewayURL(candidate) {
			return true
		}
		s = rest
	}
}

// afterColonString returns the quoted value that follows the next `:`, which
// is the shape `"baseURL": "..."` leaves once the key is consumed. Reporting
// false when no colon or no quoted value follows keeps a nested object or a
// different key from being read as a URL.
func afterColonString(s string) (string, bool) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return "", false
	}
	rest := s[colon+1:]
	start := strings.IndexByte(rest, '"')
	if start < 0 {
		return "", false
	}
	// A value that is not a bare string (an object, a number) has its first
	// quote after another token; the brace/number check below rejects it.
	if strings.TrimSpace(rest[:start]) != "" {
		return "", false
	}
	end := strings.IndexByte(rest[start+1:], '"')
	if end < 0 {
		return "", false
	}
	return rest[start+1 : start+1+end], true
}

// isLocalGatewayURL reports whether a configured base URL addresses this
// gateway: the loopback host on the gateway's port, or a private-network host
// (a tunnel or Tailscale endpoint) on the same port.
//
// The port is part of the answer. "http://localhost:3000" is some other service,
// and calling it configured would be a badge that lies in the other direction.
func isLocalGatewayURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	// The server appends /v1; a base URL that already carries it is the same
	// endpoint for this comparison.
	port := u.Port()
	if port == "" && (u.Scheme == "http" || u.Scheme == "https") {
		// No explicit port: only a default-port spelling could be this
		// gateway, and the gateway never runs on 80/443.
		return false
	}
	if port != gatewayPort {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1", "host.docker.internal":
		return true
	}
	return isPrivateHost(strings.ToLower(u.Hostname()))
}

// isPrivateHost reports whether host names a private-network address: a
// 10/8, 192.168/16 or 172.16/12 literal, the Tailscale CGNAT range, or a
// *.ts.net name. These are the endpoints a tunnel or Tailscale exposes.
func isPrivateHost(host string) bool {
	switch {
	case strings.HasSuffix(host, ".ts.net"):
		return true
	case strings.HasPrefix(host, "10."):
		return true
	case strings.HasPrefix(host, "192.168."):
		return true
	case strings.HasPrefix(host, "172."):
		// 172.16.0.0/12 only; a coarse prefix match would also cover public
		// 172.x addresses, so the second octet is parsed.
		return is172Private(host)
	case strings.HasPrefix(host, "100."):
		// 100.64.0.0/10 is Tailscale's CGNAT block. A coarser prefix match is
		// acceptable: it only ever turns a badge green on a machine that
		// already points a client at that address.
		return true
	}
	return false
}

// is172Private reports whether a 172.x.y.z literal falls in 172.16.0.0/12.
func is172Private(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) < 2 || len(parts[1]) == 0 || len(parts[1]) > 2 {
		return false
	}
	second := 0
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return false
		}
		second = second*10 + int(c-'0')
	}
	return second >= 16 && second <= 31
}
