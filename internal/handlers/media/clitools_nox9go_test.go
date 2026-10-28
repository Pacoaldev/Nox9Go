package media

import "testing"

// The user's real config: provider named after the product, on the default
// loopback port.
func TestOpenCodeConfigTargetsGateway_RenamedProvider(t *testing.T) {
	raw := []byte(`{
  "model": "NOX9GO/MinionsPRO",
  "provider": {
    "NOX9GO": {
      "npm": "@ai-sdk/openai-compatible",
      "options": { "apiKey": "sk-x", "baseURL": "http://localhost:20130/v1" }
    }
  }
}`)
	if !opencodeConfigTargetsGateway(raw) {
		t.Fatal("a provider pointing at the gateway must be detected whatever it is called")
	}
}

func TestOpenCodeConfigTargetsGateway_UpstreamName(t *testing.T) {
	raw := []byte(`{"provider": {"9router": {"options": {"baseURL": "http://127.0.0.1:20130/v1"}}}}`)
	if !opencodeConfigTargetsGateway(raw) {
		t.Fatal("the upstream provider name must still be recognised")
	}
}

func TestOpenCodeConfigTargetsGateway_OtherEndpoint(t *testing.T) {
	cases := map[string]string{
		"other port":  `{"provider": {"x": {"options": {"baseURL": "http://localhost:3000/v1"}}}}`,
		"remote host": `{"provider": {"x": {"options": {"baseURL": "https://api.openai.com/v1"}}}}`,
		"no provider": `{"model": "NOX9GO/MinionsPRO"}`,
		"empty file":  ``,
	}
	for name, raw := range cases {
		if opencodeConfigTargetsGateway([]byte(raw)) {
			t.Errorf("%s: must not report configured", name)
		}
	}
}

func TestIsLocalGatewayURL(t *testing.T) {
	yes := []string{
		"http://127.0.0.1:20130",
		"http://localhost:20130/v1",
		"http://127.0.0.1:20130/v1/",
		"HTTP://LOCALHOST:20130/v1",
	}
	no := []string{
		"", "http://localhost:3000", "http://localhost", "https://api.anthropic.com",
		"http://192.168.1.10:8080",
	}
	for _, u := range yes {
		if !isLocalGatewayURL(u) {
			t.Errorf("%q: want true", u)
		}
	}
	for _, u := range no {
		if isLocalGatewayURL(u) {
			t.Errorf("%q: want false", u)
		}
	}
}
