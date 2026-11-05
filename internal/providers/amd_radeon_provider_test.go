package providers

import (
	"testing"
)

func TestAMDRadeonProvider_Config(t *testing.T) {
	cfg, ok := KnownProviders["amd-radeon"]
	if !ok {
		t.Fatal("expected KnownProviders to contain 'amd-radeon'")
	}
	if cfg.BaseURL != "https://developer.amd.com.cn/radeon/api/v1/chat/completions" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.AuthHeader != "Authorization" || cfg.AuthScheme != "bearer" {
		t.Errorf("expected Authorization/bearer, got %s/%s", cfg.AuthHeader, cfg.AuthScheme)
	}
	if got := ModelsListURL("amd-radeon"); got != "https://developer.amd.com.cn/radeon/api/v1/models" {
		t.Errorf("ModelsListURL(amd-radeon) = %q", got)
	}
}

func TestAMDRadeonProvider_Aliases(t *testing.T) {
	if got := ResolveAlias("amd"); got != "amd-radeon" {
		t.Fatalf("ResolveAlias(amd) = %q, want amd-radeon", got)
	}
	if got := RegistryAliases["amd-radeon"]; got != "amd" {
		t.Fatalf("RegistryAliases[amd-radeon] = %q, want amd", got)
	}
	// `rc` stays runcrate — AMD keys look like rc-… but that prefix is taken.
	if got := ResolveAlias("rc"); got != "runcrate" {
		t.Fatalf("ResolveAlias(rc) = %q, want runcrate (must not steal for amd-radeon)", got)
	}
}

func TestAMDRadeonProvider_Models(t *testing.T) {
	models := GetProviderModels("amd-radeon")
	if len(models) != 11 {
		t.Fatalf("expected 11 static chat models, got %d: %v", len(models), models)
	}
	if len(GetProviderModels("amd")) != 11 {
		t.Fatalf("alias amd must resolve the same static catalog")
	}
	found := false
	for _, id := range models {
		if id == "DeepSeek-V4-Flash" {
			found = true
		}
		if id == "DeepSeek-V4-Flash-0731" {
			t.Fatalf("catalog must use wire id DeepSeek-V4-Flash, not card title %q", id)
		}
	}
	if !found {
		t.Fatal("expected DeepSeek-V4-Flash in static catalog")
	}
}

func TestAMDRadeonProvider_Capabilities(t *testing.T) {
	flash := GetCapabilitiesForModel("amd-radeon", "DeepSeek-V4-Flash")
	if flash.Vision {
		t.Error("DeepSeek-V4-Flash is text-only")
	}
	if !flash.Tools || !flash.Reasoning || flash.ThinkingFormat != "openai" {
		t.Errorf("DeepSeek-V4-Flash caps = %+v", flash)
	}
	if flash.ContextWindow != 1048576 {
		t.Errorf("DeepSeek-V4-Flash ContextWindow = %d", flash.ContextWindow)
	}

	vision := GetCapabilitiesForModel("amd", "DeepSeek-V4.1-Flash")
	if !vision.Vision {
		t.Error("DeepSeek-V4.1-Flash should have Vision")
	}

	qwen := GetCapabilitiesForModel("amd-radeon", "Qwen3.8-27B")
	if qwen.ContextWindow != 262144 || !qwen.Vision {
		t.Errorf("Qwen3.8-27B caps = %+v", qwen)
	}
}
