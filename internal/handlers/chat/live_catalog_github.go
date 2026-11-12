package chat

// Live catalogue resolution for the GitHub Copilot provider.
//
// Copilot decides per account and per plan which models an account may use, and
// the static registry cannot know that: it lists models this account is not
// entitled to, which then answer 400 model_not_available_for_integrator. Copilot
// publishes the real set at GET https://api.githubcopilot.com/models, authorised
// by the short-lived copilotToken the OAuth pipeline mints (see
// internal/proxy/oauth/github.go). This resolver replaces the static catalogue
// for a GitHub connection, exactly like the Zed resolver does for Zed.

import (
	"context"
	"io"
	"net/http"
	"strings"

	json "encoding/json/v2"

	"nox9go/proxy/internal/handlers/shared"
	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/models"
)

// githubCopilotModelsURL is a variable so tests can point the resolver at a
// local server.
var githubCopilotModelsURL = "https://api.githubcopilot.com/models"

// githubCopilotCatalogHeaders mirrors the executor's Copilot identity headers.
// Copilot rejects a catalogue request whose integration id does not match the
// editor it thinks it is serving.
func githubCopilotCatalogHeaders(copilotToken string) map[string]string {
	return map[string]string{
		"Authorization":          "Bearer " + strings.TrimSpace(copilotToken),
		"Accept":                 "application/json",
		"copilot-integration-id": "vscode-chat",
		"editor-version":         "vscode/1.110.0",
		"editor-plugin-version":  "copilot-chat/0.38.0",
		"user-agent":             "GitHubCopilotChat/0.38.0",
		"x-github-api-version":   "2025-04-01",
	}
}

// githubCopilotModel is the subset of one catalogue entry this resolver reads.
type githubCopilotModel struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ModelPickerEnabled *bool  `json:"model_picker_enabled"`
	Capabilities       struct {
		Type   string `json:"type"`
		Family string `json:"family"`
		Limits struct {
			MaxContextWindowTokens int `json:"max_context_window_tokens"`
			MaxOutputTokens        int `json:"max_output_tokens"`
		} `json:"limits"`
		Supports struct {
			ToolCalls bool `json:"tool_calls"`
			Vision    bool `json:"vision"`
		} `json:"supports"`
	} `json:"capabilities"`
	Policy struct {
		State string `json:"state"`
	} `json:"policy"`
}

// fetchGithubLiveCatalog returns the models Copilot reports for this account.
func (h *ChatHandler) fetchGithubLiveCatalog(ctx context.Context, conn *models.ProviderConnection, connData *shared.ConnectionData) []LiveModel {
	copilotToken := githubCopilotToken(connData)
	if copilotToken == "" {
		return nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, liveCatalogFastHTTP)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, githubCopilotModelsURL, nil)
	if err != nil {
		return nil
	}
	for key, value := range githubCopilotCatalogHeaders(copilotToken) {
		req.Header.Set(key, value)
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		log.Warn("models", "github catalog fetch failed", "conn", conn.ID, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Warn("models", "github catalog rejected", "conn", conn.ID, "status", resp.StatusCode)
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	return parseGithubLiveCatalog(body)
}

// githubCopilotToken reads the short-lived bearer Copilot accepts from the
// connection's providerSpecificData block.
func githubCopilotToken(connData *shared.ConnectionData) string {
	if connData == nil || connData.ProviderSpecificData == nil {
		return ""
	}
	token, _ := connData.ProviderSpecificData["copilotToken"].(string)
	return strings.TrimSpace(token)
}

// parseGithubLiveCatalog keeps the chat models this account may actually pick.
//
// A model is published when Copilot states it as chat-capable and does not mark
// it disabled. Entries that omit the capability block are kept: the catalogue
// shape has changed before, and dropping an entry the resolver does not model
// would hide a model that answers, which is a worse failure than listing one
// that does not.
func parseGithubLiveCatalog(body []byte) []LiveModel {
	var payload struct {
		Data   []githubCopilotModel `json:"data"`
		Models []githubCopilotModel `json:"models"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	entries := payload.Data
	if len(entries) == 0 {
		entries = payload.Models
	}

	out := make([]LiveModel, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" || !githubModelUsable(entry) {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = id
		}
		ctxLen := entry.Capabilities.Limits.MaxContextWindowTokens
		maxOut := entry.Capabilities.Limits.MaxOutputTokens
		caps := &LiveCapabilities{
			Tools:         entry.Capabilities.Supports.ToolCalls,
			Vision:        entry.Capabilities.Supports.Vision,
			ContextWindow: ctxLen,
			MaxOutput:     maxOut,
			Thinking:      githubFamilyReasons(entry.Capabilities.Family),
		}
		out = append(out, LiveModel{
			ID:                  id,
			Name:                name,
			ContextLength:       ctxLen,
			MaxOutput:           maxOut,
			Capabilities:        caps,
			LimitsAuthoritative: ctxLen > 0 || maxOut > 0,
		})
	}
	return out
}

// githubModelUsable applies the three filters the catalogue states, each only
// when the entry actually carries it.
func githubModelUsable(entry githubCopilotModel) bool {
	if entry.Policy.State != "" && entry.Policy.State != "enabled" {
		return false
	}
	if entry.ModelPickerEnabled != nil && !*entry.ModelPickerEnabled {
		return false
	}
	switch entry.Capabilities.Type {
	case "", "chat":
		return true
	default:
		return false
	}
}

// githubFamilyReasons reports whether a catalogue capability family names a
// reasoning model. Copilot publishes no boolean for it, only the family, and the
// dashboard uses the flag to decide whether to offer a thinking control.
func githubFamilyReasons(family string) bool {
	family = strings.ToLower(strings.TrimSpace(family))
	if family == "" {
		return false
	}
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5"} {
		if strings.HasPrefix(family, prefix) {
			return true
		}
	}
	return false
}
