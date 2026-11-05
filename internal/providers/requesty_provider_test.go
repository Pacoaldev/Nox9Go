package providers

import "testing"

// TestRequestyProvider pins the Requesty AI router entry: transport, alias
// resolution, executor reach, the live catalogue endpoint and the curated
// model fallback. Requesty publishes 815 ids live, so the static catalog is a
// curated subset — one of them, muse-glimmer-30b, is the model the operator
// pasted the curl for, and it only resolves as nvidia/muse-glimmer-30b.
func TestRequestyProvider(t *testing.T) {
	cfg, ok := KnownProviders["requesty"]
	if !ok {
		t.Fatal("KnownProviders has no requesty entry")
	}
	if cfg.BaseURL != "https://router.requesty.ai/v1/chat/completions" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.AuthHeader != "Authorization" || cfg.AuthScheme != "bearer" {
		t.Errorf("auth = %q/%q, want Authorization/bearer", cfg.AuthHeader, cfg.AuthScheme)
	}

	for _, alias := range []string{"requesty", "rq"} {
		if got := ResolveAlias(alias); got != "requesty" {
			t.Errorf("ResolveAlias(%q) = %q, want requesty", alias, got)
		}
	}
	if got := GetProviderAlias("requesty"); got != "rq" {
		t.Errorf("GetProviderAlias(requesty) = %q, want rq", got)
	}

	if got := ModelsListURL("requesty"); got != "https://router.requesty.ai/v1/models" {
		t.Errorf("ModelsListURL = %q", got)
	}

	models := GetProviderModels("requesty")
	if len(models) == 0 {
		t.Fatal("static catalog is empty")
	}
	for _, want := range []string{"nvidia/muse-glimmer-30b", "fireworks/muse-glimmer-30b", "meta/muse-spark-1.3"} {
		found := false
		for _, m := range models {
			if m == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("catalog missing %q", want)
		}
	}

	// A vendor-prefixed id must still resolve a window and vision, otherwise the
	// dashboard publishes the 128k floor for a model with 131k and image input.
	caps := GetCapabilitiesDetailForModel("requesty", "nvidia/muse-glimmer-30b")
	if caps.ContextWindow < 131072 {
		t.Errorf("context window = %d, want >= 131072", caps.ContextWindow)
	}
	if !caps.Vision {
		t.Error("muse-glimmer-30b accepts images upstream")
	}
}
