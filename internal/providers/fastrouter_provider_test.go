package providers

import "testing"

func TestFastRouterProvider(t *testing.T) {
	cfg, ok := KnownProviders["fastrouter"]
	if !ok {
		t.Fatal("KnownProviders has no fastrouter entry")
	}
	if cfg.BaseURL != "https://api.fastrouter.ai/api/v1/chat/completions" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.AuthHeader != "Authorization" || cfg.AuthScheme != "bearer" {
		t.Errorf("auth = %q/%q, want Authorization/bearer", cfg.AuthHeader, cfg.AuthScheme)
	}

	for _, alias := range []string{"fastrouter", "fr"} {
		if got := ResolveAlias(alias); got != "fastrouter" {
			t.Errorf("ResolveAlias(%q) = %q, want fastrouter", alias, got)
		}
	}
	if got := GetProviderAlias("fastrouter"); got != "fr" {
		t.Errorf("GetProviderAlias(fastrouter) = %q, want fr", got)
	}

	if got := ModelsListURL("fastrouter"); got != "https://api.fastrouter.ai/api/v1/models" {
		t.Errorf("ModelsListURL = %q", got)
	}

	models := GetProviderModels("fastrouter")
	if len(models) == 0 {
		t.Fatal("static catalog is empty")
	}
	if models[0] != "fastrouter/auto" {
		t.Errorf("models[0] = %q, want fastrouter/auto", models[0])
	}
}
