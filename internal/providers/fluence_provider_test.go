package providers

import "testing"

// Fluence (https://inference.fluence.cloud/docs) is a paid OpenAI-compatible
// router: prepaid USD credits or a monthly plan, bearer key with the
// `inference` permission. Its catalogue is per key, not global — GET /v1/models
// lists the key's routing profiles (fluence/architect, fluence/coder, …) plus
// the concrete models it may call — so there is no static snapshot to ship and
// discovery is the only source of truth.
func TestFluenceProvider_Config(t *testing.T) {
	cfg, ok := KnownProviders["fluence"]
	if !ok {
		t.Fatal("expected KnownProviders to contain 'fluence'")
	}
	if cfg.BaseURL != "https://api.fluence.cloud/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want the documented OpenAI-compatible endpoint", cfg.BaseURL)
	}
	if cfg.AuthHeader != "Authorization" || cfg.AuthScheme != "bearer" {
		t.Errorf("expected Authorization/bearer, got %s/%s", cfg.AuthHeader, cfg.AuthScheme)
	}
	if got := ModelsListURL("fluence"); got != "https://api.fluence.cloud/v1/models" {
		t.Errorf("ModelsListURL(fluence) = %q, want the key-scoped /v1/models catalogue", got)
	}
	// The docs publish Anthropic Messages and Responses on the same host, but
	// the gateway entry speaks OpenAI chat completions; an upstream that is
	// Anthropic-native would need the translation flag and must not get it.
	if cfg.UpstreamIsAnthropic {
		t.Error("UpstreamIsAnthropic must stay false: the entry is the OpenAI-compatible /v1 surface")
	}
	if cfg.Hidden {
		t.Error("fluence is a live paid provider with a working key-gated catalogue; it must not be hidden")
	}
}

func TestFluenceProvider_Aliases(t *testing.T) {
	// Fluence publishes model ids as "fluence/coder", so the published prefix
	// is the provider id itself.
	if got := GetProviderAlias("fluence"); got != "fluence" {
		t.Fatalf("GetProviderAlias(fluence) = %q, want fluence", got)
	}
	if got := RegistryAliases["fluence"]; got != "fluence" {
		t.Fatalf("RegistryAliases[fluence] = %q, want fluence", got)
	}
	// ProviderAliasMap forbids self-maps (TestProviderAliasMap_NoSelfCycle), so
	// a bare "fluence" resolves through passthrough instead.
	if _, ok := ProviderAliasMap["fluence"]; ok {
		t.Fatal("ProviderAliasMap must not contain a fluence->fluence self entry")
	}
	if got := ResolveAlias("fluence"); got != "fluence" {
		t.Fatalf("ResolveAlias(fluence) = %q, want fluence (id passes through)", got)
	}
}

// The catalogue is per key, so the static snapshot stays empty: a model row
// invented here would be a model the gateway claims to serve and the upstream
// may refuse.
func TestFluenceProvider_NoStaticModels(t *testing.T) {
	if models, ok := ProviderModels["fluence"]; ok && len(models) > 0 {
		t.Fatalf("ProviderModels[fluence] = %v, want no static rows (catalogue is key-scoped)", models)
	}
}

// Fluence converts legacy functions/function_call to tools and serves tool
// calls on every chat route, so tools stay on. Reasoning control is per
// routing profile and per serving model (effort_mode/efforts live in the
// profile), so no static thinking format is claimed here.
func TestFluenceProvider_Capabilities(t *testing.T) {
	caps := GetCapabilitiesForModel("fluence", "fluence/coder")
	if !caps.Tools {
		t.Error("Tools must stay on: Fluence serves tool calls and converts legacy functions")
	}
	if caps.ThinkingFormat != "" {
		t.Errorf("ThinkingFormat = %q, want empty (effort is a routing-profile setting, not a per-model wire shape)", caps.ThinkingFormat)
	}
}
