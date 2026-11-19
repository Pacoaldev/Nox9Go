package providers

import "testing"

// Hetzner Inference API (experimental phase: free while it lasts, no SLA).
// OpenAI-compatible chat completions behind a bearer token; the live catalog
// endpoint validates credentials (401 without one), so the static snapshot is
// the only thing the dashboard can show before a key is stored.
func TestHetznerProvider_Config(t *testing.T) {
	cfg, ok := KnownProviders["hetzner"]
	if !ok {
		t.Fatal("expected KnownProviders to contain 'hetzner'")
	}
	if cfg.BaseURL != "https://inference.hetzner.com/api/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want the OmniRoute registry endpoint", cfg.BaseURL)
	}
	if cfg.AuthHeader != "Authorization" || cfg.AuthScheme != "bearer" {
		t.Errorf("expected Authorization/bearer, got %s/%s", cfg.AuthHeader, cfg.AuthScheme)
	}
	if got := ModelsListURL("hetzner"); got != "https://inference.hetzner.com/api/v1/models" {
		t.Errorf("ModelsListURL(hetzner) = %q, want the /v1/models catalogue", got)
	}
}

func TestHetznerProvider_Aliases(t *testing.T) {
	// ResolveAlias passes unknown ids through, so the bare id still resolves.
	if got := ResolveAlias("hetzner"); got != "hetzner" {
		t.Fatalf("ResolveAlias(hetzner) = %q, want hetzner (id passes through)", got)
	}
	// OmniRoute publishes the provider with alias "hetzner" — no short form —
	// so RegistryAliases maps it to itself like sensenova/nan.
	if got := RegistryAliases["hetzner"]; got != "hetzner" {
		t.Fatalf("RegistryAliases[hetzner] = %q, want hetzner", got)
	}
	// ProviderAliasMap forbids self-maps (TestProviderAliasMap_NoSelfCycle);
	// hetzner must rely on passthrough instead of a self entry.
	if _, ok := ProviderAliasMap["hetzner"]; ok {
		t.Fatal("ProviderAliasMap must not contain a hetzner->hetzner self entry")
	}
}

func TestHetznerProvider_Models(t *testing.T) {
	models := GetProviderModels("hetzner")
	if len(models) != 1 {
		t.Fatalf("expected exactly 1 static model (experimental endpoint serves one), got %d: %v", len(models), models)
	}
	if models[0] != "Qwen/Qwen3.6-35B-A3B-FP8" {
		t.Fatalf("models[0] = %q, want Qwen/Qwen3.6-35B-A3B-FP8 (OmniRoute CHAT_OPENAI_COMPAT_MODELS)", models[0])
	}
}

func TestHetznerProvider_Capabilities(t *testing.T) {
	caps := GetCapabilitiesForModel("hetzner", "Qwen/Qwen3.6-35B-A3B-FP8")
	if !caps.Reasoning || !caps.Tools {
		t.Errorf("Qwen3.6-35B-A3B-FP8 caps = %+v, want Reasoning+Tools", caps)
	}
	if caps.ThinkingFormat != "openai" {
		t.Errorf("ThinkingFormat = %q, want openai", caps.ThinkingFormat)
	}
	if caps.ContextWindow != 262144 {
		t.Errorf("ContextWindow = %d, want 262144 (same Qwen3.6-35B window as openrouter :free)", caps.ContextWindow)
	}
}
