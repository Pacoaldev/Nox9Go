package dashboard

// Tests for the edge-block discrimination in provider probes.
//
// The same HTTP 403 carries two very different meanings: "your key is wrong"
// and "an edge/WAF refused you before the key was read". Telling them apart is
// the whole point of looksEdgeBlocked, so these tests pin both directions —
// a network-flavoured body must be reported as a block, and an auth-flavoured
// body must keep its credential verdict.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"nox9go/proxy/internal/providers"
)

// probeTestProviderConfig is a minimal OpenAI-shaped provider for probing.
func probeTestProviderConfig() providers.ProviderConfig {
	return providers.ProviderConfig{
		BaseURL:    "https://api.example.com/openai/v1/chat/completions",
		AuthHeader: "Authorization",
	}
}

func TestLooksEdgeBlocked(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "groq cloudflare region block",
			body: `{"error":{"message":"Access denied. Please check your network settings."}}`,
			want: true,
		},
		{
			name: "cloudflare html challenge",
			body: "<html><head><title>Attention Required! | Cloudflare</title></head></html>",
			want: true,
		},
		{
			name: "cloudflare 1020 access denied",
			body: "<html><title>Error 1020: Access denied</title></html>",
			want: true,
		},
		{
			name: "cloudflare just a moment",
			body: "<html><title>Just a moment...</title></html>",
			want: true,
		},
		{
			name: "openai style key rejection",
			body: `{"error":{"message":"Incorrect API key provided: sk-abc***","type":"invalid_request_error"}}`,
			want: false,
		},
		{
			name: "generic unauthorized body",
			body: `{"error":{"message":"Unauthorized"}}`,
			want: false,
		},
		{
			name: "missing api key",
			body: `{"error":"missing api key"}`,
			want: false,
		},
		{
			// The key verdict must win even when the body also mentions access.
			name: "auth phrase beats edge marker",
			body: `{"error":{"message":"Access denied: invalid api key"}}`,
			want: false,
		},
		{
			name: "empty body",
			body: "",
			want: false,
		},
		{
			name: "unrelated 403 body",
			body: `{"error":"forbidden"}`,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksEdgeBlocked([]byte(tc.body)); got != tc.want {
				t.Errorf("looksEdgeBlocked(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// A blocked generic probe must say the key was never evaluated, instead of
// claiming the key is invalid and sending the operator after a credential bug.
func TestValidateGeneric_EdgeBlockIsNotReportedAsBadKey(t *testing.T) {
	original := validateProbeDo
	defer func() { validateProbeDo = original }()

	validateProbeDo = func(context.Context, string, string, map[string]string, []byte) (int, []byte, error) {
		return http.StatusForbidden, []byte(`{"error":{"message":"Access denied. Please check your network settings."}}`), nil
	}

	cfg := probeTestProviderConfig()
	out := validateGeneric(context.Background(), "groq", cfg, "sk-test")

	if out.valid {
		t.Fatal("a blocked probe must not be reported as valid")
	}
	if strings.Contains(out.message, "Invalid API key") {
		t.Errorf("blocked probe reported as a bad key: %q", out.message)
	}
	if !strings.Contains(out.message, "Network/region block") {
		t.Errorf("want a network-block message, got %q", out.message)
	}
	if !strings.Contains(out.message, "not evaluated") {
		t.Errorf("message must state the key was not evaluated, got %q", out.message)
	}
}

// The same status with an auth-flavoured body must keep the credential verdict,
// so a genuinely wrong key is never relabelled as a network problem.
func TestValidateGeneric_RealKeyRejectionKeepsCredentialMessage(t *testing.T) {
	original := validateProbeDo
	defer func() { validateProbeDo = original }()

	validateProbeDo = func(context.Context, string, string, map[string]string, []byte) (int, []byte, error) {
		return http.StatusForbidden, []byte(`{"error":{"message":"Invalid API key provided"}}`), nil
	}

	cfg := probeTestProviderConfig()
	out := validateGeneric(context.Background(), "groq", cfg, "sk-test")

	if out.valid {
		t.Fatal("a rejected key must not be reported as valid")
	}
	if !strings.Contains(out.message, "Invalid API key") {
		t.Errorf("want the credential message, got %q", out.message)
	}
}

// Baidu Qianfan serves no /v2/models route: its edge answers
// `403 {"code":"AccessDenied","message":"Access denied."}` for that path while
// the chat route the gateway actually uses evaluates the key and answers
// `400/invalid_iam_token` for a bad one. Treating that 403 as an edge block made
// a perfectly reachable provider look like a network problem and never tested
// the credential. A blocked /models probe must therefore be confirmed against
// the chat route before it is reported as a block.
func TestValidateGeneric_EdgeBlockConfirmedAgainstChatRoute(t *testing.T) {
	calls := withProbeStub(t, func(call int, p capturedProbe) (int, []byte, error) {
		if call == 0 {
			return http.StatusForbidden,
				[]byte(`{"requestId":"ab2a2fbe","code":"AccessDenied","message":"Access denied."}`), nil
		}
		return http.StatusBadRequest,
			[]byte(`{"error":{"code":"invalid_iam_token","message":"invalid_iam_token"}}`), nil
	})

	cfg := probeTestProviderConfig()
	cfg.BaseURL = "https://qianfan.baidubce.com/v2/chat/completions"
	out := validateGeneric(context.Background(), "baidu", cfg, "sk-test")

	if len(*calls) < 2 {
		t.Fatalf("the blocked /models probe must be confirmed against the chat route, got %d probe(s)", len(*calls))
	}
	if strings.Contains(out.message, "Network/region block") {
		t.Errorf("a provider whose chat route answers must not be reported as blocked: %q", out.message)
	}
	if !out.valid {
		t.Errorf("the chat route answered the key, want a valid outcome, got %+v", out)
	}
}

// A provider genuinely behind an edge block answers the same refusal on the
// chat route, so the block verdict must survive the confirmation.
func TestValidateGeneric_RealEdgeBlockStillReported(t *testing.T) {
	withProbeStub(t, func(int, capturedProbe) (int, []byte, error) {
		return http.StatusForbidden,
			[]byte(`{"error":{"message":"Access denied. Please check your network settings."}}`), nil
	})

	cfg := probeTestProviderConfig()
	out := validateGeneric(context.Background(), "groq", cfg, "sk-test")

	if out.valid {
		t.Fatal("a blocked probe must not be reported as valid")
	}
	if !strings.Contains(out.message, "Network/region block") {
		t.Errorf("want the network-block message, got %q", out.message)
	}
}
