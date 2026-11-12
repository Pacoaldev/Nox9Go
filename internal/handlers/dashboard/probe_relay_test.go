package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"nox9go/proxy/internal/db"
)

// "Test Connection" on a relayed connection must exercise the relay. It used to
// skip the pool entirely, so the probe answered from the host's own IP: it could
// pass while production traffic failed, fail while the relay was healthy, and
// it published the egress IP on the very request run to confirm a proxy.

func TestProbeRelayClient_RoutesThroughTheRelay(t *testing.T) {
	var gotTarget, gotPath, gotHost string
	var hitRelay bool

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitRelay = true
		gotTarget = r.Header.Get("x-relay-target")
		gotPath = r.Header.Get("x-relay-path")
		gotHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
	}))
	defer relay.Close()

	client := newProbeRelayClient(relay.URL, "https://api.openai.com/v1/models")
	if client == nil {
		t.Fatal("a relay pool must produce a probe client, got nil")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("probe through relay: %v", err)
	}
	defer resp.Body.Close()

	if !hitRelay {
		t.Fatal("the probe never reached the relay host")
	}
	if gotTarget != "https://api.openai.com" {
		t.Errorf("x-relay-target = %q, want the provider origin", gotTarget)
	}
	if gotPath != "/v1/models" {
		t.Errorf("x-relay-path = %q, want the provider path", gotPath)
	}
	if gotHost == "api.openai.com" {
		t.Error("the request must be addressed to the relay host, not the provider")
	}
}

// The relay's own scheme must be honoured, not forced to https: a self-hosted
// relay on http would otherwise get a TLS handshake it cannot answer.
func TestProbeRelayClient_KeepsRelayScheme(t *testing.T) {
	var sawPlaintext bool
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sawPlaintext = true
		w.WriteHeader(http.StatusOK)
	}))
	defer relay.Close()

	client := newProbeRelayClient(relay.URL, "http://provider.internal/v1/models")
	if client == nil {
		t.Fatal("expected a probe client for an http relay")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://provider.internal/v1/models", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("probe through an http relay: %v", err)
	}
	defer resp.Body.Close()
	if !sawPlaintext {
		t.Error("the probe did not reach the relay over its own scheme")
	}
}

func TestSplitRelayTarget(t *testing.T) {
	tests := []struct {
		raw        string
		wantOrigin string
		wantPath   string
		wantOK     bool
	}{
		{"https://api.openai.com/v1/models", "https://api.openai.com", "/v1/models", true},
		{"https://api.openai.com", "https://api.openai.com", "/", true},
		{"https://api.openai.com/v1/models?limit=5", "https://api.openai.com", "/v1/models?limit=5", true},
		{"https://opencode.ai/zen/v1/usage", "https://opencode.ai", "/zen/v1/usage", true},
		{"not a url", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		got, ok := splitRelayTarget(tt.raw)
		if ok != tt.wantOK {
			t.Errorf("splitRelayTarget(%q) ok = %v, want %v", tt.raw, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got.origin != tt.wantOrigin || got.path != tt.wantPath {
			t.Errorf("splitRelayTarget(%q) = %s + %s, want %s + %s",
				tt.raw, got.origin, got.path, tt.wantOrigin, tt.wantPath)
		}
	}
}

func TestNewProbeRelayClient_RejectsUnusableInput(t *testing.T) {
	if c := newProbeRelayClient("", "https://api.openai.com/v1/models"); c != nil {
		t.Error("no relay host must produce no client, not a direct-dial one")
	}
	// An unusable upstream no longer suppresses the client: the probe derives
	// the relay target from the request it builds, so only the relay host is
	// required here.
	if c := newProbeRelayClient("https://relay.example", "garbage"); c == nil {
		t.Error("a relay host is enough; the target comes from the request")
	}
}

// A registry provider (baidu/qianfan is the case that exposed this) stores no
// per-connection baseUrl, so the probe had no upstream to name and the relay
// client came back nil — the probe then dialed the provider directly from the
// host's own IP and reported the provider's edge block. The relay's whole
// contract is "dial me and I forward to x-relay-target/x-relay-path", so the
// target can be derived from the request the probe already built.
func TestProbeRelayClient_DerivesTargetFromRequest(t *testing.T) {
	var gotTarget, gotPath, gotHost string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.Header.Get("x-relay-target")
		gotPath = r.Header.Get("x-relay-path")
		gotHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"ernie-4.5"}]}`))
	}))
	defer relay.Close()

	client := newProbeRelayClient(relay.URL, "")
	if client == nil {
		t.Fatal("a relay pool must produce a probe client even without a stored baseUrl")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://qianfan.baidubce.com/v2/models", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("probe through relay: %v", err)
	}
	defer resp.Body.Close()

	if gotHost == "qianfan.baidubce.com" {
		t.Fatal("the probe reached the provider directly instead of the relay")
	}
	if gotTarget != "https://qianfan.baidubce.com" {
		t.Errorf("x-relay-target = %q, want the provider origin", gotTarget)
	}
	if gotPath != "/v2/models" {
		t.Errorf("x-relay-path = %q, want the provider path", gotPath)
	}
}

// The chat pipeline accepts a per-connection `vercelRelayUrl` in
// providerSpecificData as a relay, but the probe only read a proxy pool id — so
// a connection relayed that way was tested straight at the provider and could
// report the provider's edge block while production traffic was flowing fine.
func TestProbeHTTPClient_HonoursPerConnectionRelayURL(t *testing.T) {
	var hitRelay bool
	var gotTarget string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitRelay = true
		gotTarget = r.Header.Get("x-relay-target")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"ernie-4.5"}]}`))
	}))
	defer relay.Close()

	h := &DashboardHandler{}
	data := connectionProbeData{
		ProviderSpecificData: map[string]any{"vercelRelayUrl": relay.URL},
	}
	client, proxyURL := h.probeHTTPClient(data, map[string]any{})
	if proxyURL != "" {
		t.Fatalf("a relay is not a pre-checked HTTP proxy, got %q", proxyURL)
	}
	if client == nil {
		t.Fatal("a per-connection relay url must produce a probe client")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://qianfan.baidubce.com/v2/models", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("probe through relay: %v", err)
	}
	defer resp.Body.Close()

	if !hitRelay {
		t.Fatal("the probe never reached the relay host")
	}
	if gotTarget != "https://qianfan.baidubce.com" {
		t.Errorf("x-relay-target = %q, want the provider origin", gotTarget)
	}
}

// The pool-type test endpoint and the connection probe must agree on what a
// relay is. Both now read ProxyPool.IsEdgeRelay, so a fifth edge platform is
// added in one place rather than to every caller's own list — and the two paths
// cannot drift apart again. netlify joined in issue #250.
func TestEdgeRelayPoolTypesAreClassifiedOnce(t *testing.T) {
	for _, poolType := range []string{"vercel", "cloudflare", "deno", "netlify"} {
		pool := &db.ProxyPool{Type: poolType}
		if !pool.IsEdgeRelay() {
			t.Errorf("pool type %q is an edge relay and must not be dialed as an HTTP proxy", poolType)
		}
	}
	for _, poolType := range []string{"http", "", "socks5"} {
		pool := &db.ProxyPool{Type: poolType}
		if pool.IsEdgeRelay() {
			t.Errorf("pool type %q is a dialable proxy, not a relay", poolType)
		}
	}
	var missing *db.ProxyPool
	if missing.IsEdgeRelay() {
		t.Error("a missing pool is not a relay")
	}
}
