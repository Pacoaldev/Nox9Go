package providers

import (
	"testing"
)

func TestXKiroProvider_Config(t *testing.T) {
	cfg, ok := KnownProviders["xkiro"]
	if !ok {
		t.Fatal("expected KnownProviders to contain 'xkiro'")
	}
	if cfg.BaseURL != "https://api.xkiro.com/v1/chat/completions" {
		t.Errorf("expected BaseURL https://api.xkiro.com/v1/chat/completions, got %q", cfg.BaseURL)
	}
	if cfg.AuthHeader != "Authorization" || cfg.AuthScheme != "bearer" {
		t.Errorf("expected Authorization/bearer, got %s/%s", cfg.AuthHeader, cfg.AuthScheme)
	}
	if cfg.ImageURL != "https://api.xkiro.com/v1/images/generations" {
		t.Errorf("expected ImageURL https://api.xkiro.com/v1/images/generations, got %q", cfg.ImageURL)
	}
	if cfg.TTSURL != "https://api.xkiro.com/v1/audio/speech" {
		t.Errorf("expected TTSURL https://api.xkiro.com/v1/audio/speech, got %q", cfg.TTSURL)
	}
	if cfg.VoicesURL != "https://api.xkiro.com/v1/audio/voices" {
		t.Errorf("expected VoicesURL https://api.xkiro.com/v1/audio/voices, got %q", cfg.VoicesURL)
	}
	if cfg.UsageURL != "https://api.xkiro.com/v1/usage" {
		t.Errorf("expected UsageURL https://api.xkiro.com/v1/usage, got %q", cfg.UsageURL)
	}
	if got := ModelsListURL("xkiro"); got != "https://api.xkiro.com/v1/models" {
		t.Errorf("ModelsListURL('xkiro') = %q, want https://api.xkiro.com/v1/models", got)
	}
}

func TestXKiroProvider_Aliases(t *testing.T) {
	if got := ResolveAlias("xk"); got != "xkiro" {
		t.Errorf("ResolveAlias('xk') = %q, want 'xkiro'", got)
	}
	if got := RegistryAliases["xkiro"]; got != "xk" {
		t.Errorf("RegistryAliases['xkiro'] = %q, want 'xk'", got)
	}
}

func TestXKiroProvider_ModelsAndCapabilities(t *testing.T) {
	models := GetProviderModels("xkiro")
	if len(models) != 106 {
		t.Errorf("expected 106 models for 'xkiro', got %d", len(models))
	}
	xkModels := GetProviderModels("xk")
	if len(xkModels) != 106 {
		t.Errorf("expected 106 models for alias 'xk', got %d", len(xkModels))
	}

	caps := GetCapabilitiesForModel("xkiro", "google/gemini-3.6-flash")
	if !caps.Vision || !caps.Tools || !caps.Reasoning {
		t.Errorf("expected Vision/Tools/Reasoning=true for google/gemini-3.6-flash on xkiro, got %+v", caps)
	}
	if caps.ThinkingFormat != "openai" {
		t.Errorf("expected ThinkingFormat 'openai', got %q", caps.ThinkingFormat)
	}
	if caps.ContextWindow != 1000000 || caps.MaxOutput != 65536 {
		t.Errorf("expected ContextWindow=1000000, MaxOutput=65536, got %d/%d", caps.ContextWindow, caps.MaxOutput)
	}

	if kind := GetProviderModelKind("xkiro", "openai/gpt-image-2.5"); kind != "image" {
		t.Errorf("GetProviderModelKind('xkiro', 'openai/gpt-image-2.5') = %q, want 'image'", kind)
	}
	if kind := GetProviderModelKind("xkiro", "sensenova/sensenova-u1.5-lite"); kind != "image" {
		t.Errorf("GetProviderModelKind('xkiro', 'sensenova/sensenova-u1.5-lite') = %q, want 'image'", kind)
	}
	if kind := GetProviderModelKind("xkiro", "xkiro-voice"); kind != "tts" {
		t.Errorf("GetProviderModelKind('xkiro', 'xkiro-voice') = %q, want 'tts'", kind)
	}
}

