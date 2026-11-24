package media

import (
	"os"
	"path/filepath"
	"testing"
)

// piSeedForHome writes the same models.json content into the Pi and Gentle
// Shell client directories under a temporary home and points the process home
// there.
func piSeedForHome(t *testing.T, seed string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, dir := range []string{
		filepath.Join(home, ".pi", "agent"),
		filepath.Join(home, ".gentle-shell", "agent"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(seed), 0o644); err != nil {
			t.Fatalf("write %s: %v", dir, err)
		}
	}
	return home
}

// TestPiHasNox9Go_ReadsBothClientFiles proves the badge detection reads the
// Gentle Shell file too. A user who migrated to Gentle Shell keeps only
// ~/.gentle-shell/agent/models.json, and the Pi-only reader reported the client
// as unconfigured while it was routing through the gateway.
func TestPiHasNox9Go_ReadsBothClientFiles(t *testing.T) {
	tests := []struct {
		name string
		seed string
		want bool
	}{
		{
			name: "provider key with a tunnel baseUrl",
			seed: `{"providers": {"NOX9GO": {"baseUrl": "https://abc-tunnel.us/v1", "api": "openai-completions"}}}`,
			want: true,
		},
		{
			name: "provider key matching the gateway port",
			seed: `{"providers": {"nox9go": {"baseUrl": "http://127.0.0.1:20130/v1"}}}`,
			want: true,
		},
		{
			name: "foreign provider on another host",
			seed: `{"providers": {"other": {"baseUrl": "https://api.example.com/v1"}}}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			piSeedForHome(t, tt.seed)
			if got := piHasNox9Go(); got != tt.want {
				t.Errorf("piHasNox9Go() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPiHasNox9Go_MissingFilesIsNotConfigured proves an operator with neither
// client file installed gets "not configured" instead of a crash.
func TestPiHasNox9Go_MissingFilesIsNotConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if piHasNox9Go() {
		t.Error("piHasNox9Go() must be false when no client file exists")
	}
}

// TestPiConfigTargetsGateway_ProviderKeyWithoutBaseURL covers the entry the
// adapter writes when the gateway is reached through a tunnel: the provider key
// itself is the evidence, and the reader must accept it without a loopback URL.
func TestPiConfigTargetsGateway_ProviderKeyWithoutBaseURL(t *testing.T) {
	raw := []byte(`{"providers": {"NOX9GO": {"api": "openai-completions", "baseUrl": "https://abc-tunnel.us/v1"}}}`)
	if !piConfigTargetsGateway(raw) {
		t.Fatal("a provider entry keyed NOX9GO must be detected even off loopback")
	}
}
