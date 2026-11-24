package chat

import (
	"strings"
	"testing"

	"nox9go/proxy/internal/providers"
)

// An enabled capacity-adapter pool with no models of its own resolves to
// defaultAdapterFallbackModel so the toggle is never a no-op. That substitution
// only helps when the default names a model the gateway can actually dispatch
// AND carries the capability the pool was declared for.
//
// A stale id passed the capability pattern match, was injected ahead of the
// combo, failed at dispatch, and the request fell back to a text-only member
// that answered "this model cannot read images" — the pool read ON in the
// dashboard and did nothing.
func TestDefaultAdapterFallbackModelIsDispatchableAndVisionCapable(t *testing.T) {
	provider, model, ok := strings.Cut(defaultAdapterFallbackModel, "/")
	if !ok || provider == "" || model == "" {
		t.Fatalf("defaultAdapterFallbackModel %q is not a provider/model pair", defaultAdapterFallbackModel)
	}

	catalog := providers.GetProviderModels(provider)
	if len(catalog) == 0 {
		t.Fatalf("provider %q has no catalog entry; %q cannot be dispatched", provider, defaultAdapterFallbackModel)
	}
	found := false
	for _, id := range catalog {
		if id == model {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("defaultAdapterFallbackModel %q is not in the %q catalog %v", defaultAdapterFallbackModel, provider, catalog)
	}

	if caps := providers.GetCapabilitiesForModel(provider, model); !caps.Vision {
		t.Errorf("defaultAdapterFallbackModel %q must be vision-capable: the vision pool injects it", defaultAdapterFallbackModel)
	}
}

func TestUpgradeLegacyAdapterModelPointsAtTheCurrentDefault(t *testing.T) {
	if got := upgradeLegacyAdapterModel("oc/mimo-v2.5-free"); got != defaultAdapterFallbackModel {
		t.Errorf("upgradeLegacyAdapterModel(%q) = %q, want %q", "oc/mimo-v2.5-free", got, defaultAdapterFallbackModel)
	}
}
