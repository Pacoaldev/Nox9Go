package media

// Target URL for a media lane (images, TTS, STT, video, systemone).
//
// The registry carries a fixed URL for a cloud provider, but a self-hosted
// server lives wherever the operator says, and that address arrives as the
// connection's baseUrl. getProviderConfig completes a bare connection host into
// a chat route ("…/v1/chat/completions"), so appending a media endpoint to
// providerCfg.BaseURL would ask for "/v1/chat/completions/v1/audio/speech". This
// helper strips the chat route back off and then joins the media endpoint the
// same way the single-model path always did.

import (
	"strings"

	"nox9go/proxy/internal/handlers/chat"
	"nox9go/proxy/internal/providers"
)

// mediaTargetURL returns the absolute URL a media request must be sent to.
func mediaTargetURL(providerCfg *providers.ProviderConfig, connData *chat.ConnectionData, endpoint string) string {
	if providerCfg == nil {
		return endpoint
	}
	baseURL := strings.TrimRight(providerCfg.BaseURL, "/")
	// The connection override wins: it is the only place a self-hosted host can
	// be named. It is read raw rather than through providerCfg because the
	// config value has already been completed into a chat route.
	if connData != nil && strings.TrimSpace(connData.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(connData.BaseURL), "/")
	}
	baseURL = strings.TrimSuffix(baseURL, "/chat/completions")
	baseURL = strings.TrimSuffix(baseURL, "/messages")

	hasCustomBaseURL := connData != nil && connData.BaseURL != "" && connData.BaseURL != providerCfg.BaseURL

	switch endpoint {
	case "/v1/images/generations", "/images/generations":
		if !hasCustomBaseURL && providerCfg.ImageURL != "" {
			return providerCfg.ImageURL
		}
		return strings.TrimSuffix(baseURL, "/v1") + "/v1/images/generations"
	case "/v1/audio/speech", "/audio/speech":
		if !hasCustomBaseURL && providerCfg.TTSURL != "" {
			return providerCfg.TTSURL
		}
		return strings.TrimSuffix(baseURL, "/v1") + "/v1/audio/speech"
	case "/v1/audio/transcriptions", "/audio/transcriptions":
		if !hasCustomBaseURL && providerCfg.STTURL != "" {
			return providerCfg.STTURL
		}
		return strings.TrimSuffix(baseURL, "/v1") + "/v1/audio/transcriptions"
	case "/v1/videos/generations", "/videos/generations":
		if !hasCustomBaseURL && providerCfg.VideoURL != "" {
			if strings.HasSuffix(providerCfg.VideoURL, "/generations") {
				return providerCfg.VideoURL
			}
			return strings.TrimRight(providerCfg.VideoURL, "/") + "/generations"
		}
		return strings.TrimSuffix(baseURL, "/v1") + "/v1/videos/generations"
	case "/v1/systemone", "/systemone":
		if !hasCustomBaseURL && providerCfg.SystemoneURL != "" {
			return providerCfg.SystemoneURL
		}
		return strings.TrimSuffix(baseURL, "/v1") + "/v1/systemone"
	}
	// web/fetch is deliberately NOT handled here: its registry FetchURL is
	// applied by the caller after this function, on both the single-model and
	// the combo path, and the generic branch below already builds the fallback
	// URL correctly.
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(endpoint, "/v1/") {
		return strings.TrimSuffix(baseURL, "/v1") + endpoint
	}
	return baseURL + endpoint
}
