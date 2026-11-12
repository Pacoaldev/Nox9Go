package media

import (
	"testing"

	"nox9go/proxy/internal/handlers/chat"
	"nox9go/proxy/internal/providers"
)

// TestMediaTargetURL_SelfHostedHonoursConnectionHost pins that a self-hosted
// media provider sends the request to the host the operator typed, not to a
// cloud URL and not behind a chat route.
//
// Ollama Local proved the shape: the dashboard stores its host in
// providerSpecificData.baseUrl, chat.ConnectionData hydrates it into BaseURL,
// and getProviderConfig completes a bare host into "…/v1/chat/completions" so
// the chat lane works. A media lane that reused that completed value asked for
// "/v1/chat/completions/v1/audio/speech" and 404'd.
func TestMediaTargetURL_SelfHostedHonoursConnectionHost(t *testing.T) {
	tests := []struct {
		name       string
		providerID string
		host       string
		endpoint   string
		want       string
	}{
		{
			name:       "selfhosted-tts bare host",
			providerID: "selfhosted-tts",
			host:       "http://192.168.1.50:8880",
			endpoint:   "/v1/audio/speech",
			want:       "http://192.168.1.50:8880/v1/audio/speech",
		},
		{
			name:       "selfhosted-stt bare host",
			providerID: "selfhosted-stt",
			host:       "http://192.168.1.50:8000",
			endpoint:   "/v1/audio/transcriptions",
			want:       "http://192.168.1.50:8000/v1/audio/transcriptions",
		},
		{
			name:       "selfhosted-embedding host with /v1",
			providerID: "selfhosted-embedding",
			host:       "http://127.0.0.1:8080/v1",
			endpoint:   "/embeddings",
			want:       "http://127.0.0.1:8080/v1/embeddings",
		},
		{
			name:       "connection host already naming a chat route",
			providerID: "selfhosted-tts",
			host:       "http://127.0.0.1:9000/v1/chat/completions",
			endpoint:   "/v1/audio/speech",
			want:       "http://127.0.0.1:9000/v1/audio/speech",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &providers.ProviderConfig{
				BaseURL:    "https://cloud.example.invalid/v1/chat/completions",
				AuthHeader: "Authorization",
				AuthScheme: "bearer",
			}
			connData := &chat.ConnectionData{BaseURL: tt.host}
			if got := mediaTargetURL(cfg, connData, tt.endpoint); got != tt.want {
				t.Errorf("mediaTargetURL = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMediaTargetURL_RegistryURLStillWinsWithoutAnOverride pins that a provider
// with its own dedicated media host keeps using it.
func TestMediaTargetURL_RegistryURLStillWinsWithoutAnOverride(t *testing.T) {
	cfg := &providers.ProviderConfig{
		BaseURL:    "https://api.example.invalid/v1/chat/completions",
		AuthHeader: "Authorization",
		AuthScheme: "bearer",
		TTSURL:     "https://tts.example.invalid/v1/audio/speech",
		STTURL:     "https://stt.example.invalid/v1/audio/transcriptions",
	}
	got := mediaTargetURL(cfg, nil, "/v1/audio/speech")
	if got != cfg.TTSURL {
		t.Errorf("tts target = %q, want the registry TTSURL", got)
	}
	got = mediaTargetURL(cfg, nil, "/v1/audio/transcriptions")
	if got != cfg.STTURL {
		t.Errorf("stt target = %q, want the registry STTURL", got)
	}
}
