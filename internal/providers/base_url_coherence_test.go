package providers

import (
	"strings"
	"testing"
)

// TestBaseURLIsCompletionsOnlyWhenChatIsServed is the regression for the
// failure that made a real Replicate key look invalid.
//
// The chat lane forwards to BaseURL verbatim, so a chat provider legitimately
// ends its BaseURL in /chat/completions or /messages. The media handlers take
// the same field, strip that suffix, and append the endpoint they serve. That
// makes an entry whose provider serves *no* chat a trap: it advertises a
// completions path that nothing will ever strip correctly for a media call,
// and the upstream answers 404 — which Replicate reports as "no API key found",
// reading like a bad credential.
//
// The rule enforced here: if the entry declares no chat URL at all and carries
// no chat Format, its BaseURL must not be a completions path. Replicate was
// exactly that — BaseURL .../v1/chat/completions with no chat endpoint behind
// it.
func TestBaseURLIsCompletionsOnlyWhenChatIsServed(t *testing.T) {
	completionsSuffixes := []string{"/chat/completions", "/messages"}

	for id, cfg := range KnownProviders {
		// An explicit chat-capable format means the entry is on the chat lane.
		if cfg.Format != "" {
			continue
		}
		base := strings.TrimRight(cfg.BaseURL, "/")
		endsInCompletions := false
		for _, suffix := range completionsSuffixes {
			if strings.HasSuffix(base, suffix) {
				endsInCompletions = true
			}
		}
		if !endsInCompletions {
			continue
		}
		// A completion suffix is fine when the provider also declares the
		// media endpoints it serves under their own URLs: every lane then has
		// an exact destination and none relies on the suffix stripping.
		if cfg.ImageURL != "" || cfg.VideoURL != "" || cfg.TTSURL != "" || cfg.STTURL != "" {
			continue
		}
		// Pure chat providers are the normal case and are correct as written.
		// The ones that must not pass are those registered only to resolve an
		// id for a non-chat service. There is no field stating that, so this
		// test names the known offenders instead of guessing.
		if knownNonChatOnly[id] {
			t.Errorf(
				"provider %q declares no chat Format but BaseURL %q ends in a completions path. "+
					"A provider with no chat endpoint must point BaseURL at a transport root; "+
					"otherwise every media request 404s and the upstream reports an auth failure",
				id, cfg.BaseURL,
			)
		}
	}
}

// knownNonChatOnly lists providers registered in the catalog for a non-chat
// service. When one of these gains its own protocol lane, it moves into
// internal/handlers/media and must drop the completions suffix from BaseURL.
//
// replicate is the case this test was written for and is kept listed so a
// regression reintroducing the path is caught rather than silently passing.
var knownNonChatOnly = map[string]bool{
	"replicate": true,
}

// TestReplicateBaseURLIsTheAPIRoot pins the specific fix: Replicate has no
// /chat/completions at all, so its entry must point at the API root and must
// not claim a VideoURL constant — the prediction path carries the model id.
func TestReplicateBaseURLIsTheAPIRoot(t *testing.T) {
	cfg, ok := KnownProviders["replicate"]
	if !ok {
		t.Fatal("replicate is missing from KnownProviders")
	}
	if want := "https://api.replicate.com/v1"; cfg.BaseURL != want {
		t.Errorf("replicate BaseURL = %q, want %q", cfg.BaseURL, want)
	}
	if strings.Contains(cfg.BaseURL, "chat/completions") {
		t.Errorf("replicate BaseURL = %q; Replicate serves no chat completions", cfg.BaseURL)
	}
	if cfg.VideoURL != "" {
		t.Errorf(
			"replicate VideoURL = %q; the prediction path embeds the model id "+
				"(/v1/models/{owner}/{name}/predictions) and cannot be a constant",
			cfg.VideoURL,
		)
	}
	if cfg.ImageURL != "" {
		t.Errorf("replicate ImageURL = %q; same reason as VideoURL", cfg.ImageURL)
	}
}
