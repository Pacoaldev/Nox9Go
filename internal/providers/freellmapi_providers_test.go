package providers

import "testing"

func TestFreeLLMAPIProviders(t *testing.T) {
	cases := []struct {
		id         string
		alias      string
		baseURL    string
		modelsURL  string
		noAuth     bool
		userAgent  string
		wantModels []string
	}{
		{
			id:         "iteracompute",
			alias:      "itc",
			baseURL:    "https://api.iteracompute.com/v1/chat/completions",
			modelsURL:  "https://api.iteracompute.com/v1/models",
			wantModels: []string{"qwen/qwen3.8-27b"},
		},
		{
			id:         "ainative",
			alias:      "ain",
			baseURL:    "https://api.ainative.studio/api/v1/chat/completions",
			modelsURL:  "https://api.ainative.studio/api/v1/models",
			wantModels: []string{"gpt-4o-mini", "claude-3-5-haiku-20241022"},
		},
		{
			id:         "navyai",
			alias:      "navy",
			baseURL:    "https://api.navy/v1/chat/completions",
			modelsURL:  "https://api.navy/v1/models",
			userAgent:  "NOX9GO/1.0",
			wantModels: []string{"gpt-4o-mini", "deepseek-v3", "llama-3.3-70b-instruct"},
		},
		{
			id:        "anyapi",
			alias:     "any",
			baseURL:   "https://api.anyapi.ai/v1/chat/completions",
			modelsURL: "https://api.anyapi.ai/v1/models",
			wantModels: []string{
				"meta-llama/llama-3.3-70b-instruct:free",
				"qwen/qwen3-coder:free",
				"nvidia/nemotron-3-ultra-550b-a55b:free",
				"google/gemma-4-26b-a4b-it:free",
			},
		},
		{
			id:         "ovhcloud",
			alias:      "ovh",
			baseURL:    "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1/chat/completions",
			modelsURL:  "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1/models",
			noAuth:     true,
			wantModels: []string{"gpt-oss-120b", "Meta-Llama-3_3-70B-Instruct"},
		},
		{
			id:        "sealion",
			alias:     "sl",
			baseURL:   "https://api.sea-lion.ai/v1/chat/completions",
			modelsURL: "https://api.sea-lion.ai/v1/models",
			wantModels: []string{
				"aisingapore/gemma-sea-lion-v4-27b-it",
				"aisingapore/llama-sea-lion-v3-70b-it",
			},
		},
		{
			id:         "longcat",
			alias:      "lc",
			baseURL:    "https://api.longcat.chat/openai/v1/chat/completions",
			modelsURL:  "https://api.longcat.chat/openai/v1/models",
			wantModels: []string{"LongCat-Flash", "LongCat-Pro"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			cfg, ok := KnownProviders[tc.id]
			if !ok {
				t.Fatalf("KnownProviders missing %q", tc.id)
			}
			if cfg.BaseURL != tc.baseURL {
				t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, tc.baseURL)
			}
			if cfg.AuthHeader != "Authorization" || cfg.AuthScheme != "bearer" {
				t.Errorf("auth = %q/%q, want Authorization/bearer", cfg.AuthHeader, cfg.AuthScheme)
			}
			if cfg.NoAuth != tc.noAuth {
				t.Errorf("NoAuth = %v, want %v", cfg.NoAuth, tc.noAuth)
			}
			if tc.userAgent != "" {
				if got := cfg.StaticHeaders["User-Agent"]; got != tc.userAgent {
					t.Errorf("StaticHeaders[User-Agent] = %q, want %q", got, tc.userAgent)
				}
			}

			if got := ResolveAlias(tc.alias); got != tc.id {
				t.Errorf("ResolveAlias(%q) = %q, want %q", tc.alias, got, tc.id)
			}
			if got := GetProviderAlias(tc.id); got != tc.alias {
				t.Errorf("GetProviderAlias(%q) = %q, want %q", tc.id, got, tc.alias)
			}
			if got := IsNoAuthProvider(tc.id); got != tc.noAuth {
				t.Errorf("IsNoAuthProvider(%q) = %v, want %v", tc.id, got, tc.noAuth)
			}
			if got := IsNoAuthProvider(tc.alias); got != tc.noAuth {
				t.Errorf("IsNoAuthProvider(%q) = %v, want %v", tc.alias, got, tc.noAuth)
			}

			if got := ModelsListURL(tc.id); got != tc.modelsURL {
				t.Errorf("ModelsListURL(%q) = %q, want %q", tc.id, got, tc.modelsURL)
			}
			if got := ModelsListURL(tc.alias); got != tc.modelsURL {
				t.Errorf("ModelsListURL(%q) = %q, want %q", tc.alias, got, tc.modelsURL)
			}

			models := GetProviderModels(tc.id)
			if len(models) != len(tc.wantModels) {
				t.Fatalf("GetProviderModels(%q) = %v, want %v", tc.id, models, tc.wantModels)
			}
			aliasModels := GetProviderModels(tc.alias)
			if len(aliasModels) != len(tc.wantModels) {
				t.Fatalf("GetProviderModels(%q) = %v, want %v", tc.alias, aliasModels, tc.wantModels)
			}
			for i, want := range tc.wantModels {
				if models[i] != want {
					t.Errorf("model[%d] = %q, want %q", i, models[i], want)
				}
			}
		})
	}
}

