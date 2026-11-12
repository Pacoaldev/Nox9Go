package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"nox9go/proxy/internal/handlers/shared"
	"nox9go/proxy/internal/models"
)

// TestFetchGithubLiveCatalog_FiltersToUsableChatModels pins the rules the
// Copilot catalogue endpoint imposes on a listing.
//
// The static registry in registry_models.go lists models this account cannot
// call (gpt-5.4 answers 400 model_not_available_for_integrator), because model
// availability is decided per account and per plan upstream. GET
// https://api.githubcopilot.com/models with the connection's short-lived
// copilotToken is the only authority, and this resolver is what makes
// /v1/models agree with it.
func TestFetchGithubLiveCatalog_FiltersToUsableChatModels(t *testing.T) {
	var gotAuth, gotIntegration string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIntegration = r.Header.Get("copilot-integration-id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"id":"gpt-4.1","name":"GPT-4.1","model_picker_enabled":true,
				 "capabilities":{"type":"chat","limits":{"max_context_window_tokens":128000,"max_output_tokens":16384},
				 "supports":{"tool_calls":true,"vision":true}},
				 "policy":{"state":"enabled"}},
				{"id":"gpt-5.4","name":"GPT-5.4","model_picker_enabled":true,
				 "capabilities":{"type":"chat"},
				 "policy":{"state":"disabled"}},
				{"id":"text-embedding-3-small","name":"Embedding",
				 "capabilities":{"type":"embeddings"},
				 "policy":{"state":"enabled"}},
				{"id":"o4-mini","name":"o4-mini","model_picker_enabled":false,
				 "capabilities":{"type":"chat"},
				 "policy":{"state":"enabled"}}
			]
		}`))
	}))
	defer srv.Close()

	old := githubCopilotModelsURL
	githubCopilotModelsURL = srv.URL
	defer func() { githubCopilotModelsURL = old }()

	h := NewChatHandler(nil)
	conn := &models.ProviderConnection{ID: "conn-gh-live", Provider: "github", IsActive: 1}
	connData := &shared.ConnectionData{
		ProviderSpecificData: map[string]any{"copilotToken": "tid=copilot-token"},
	}

	live := h.fetchGithubLiveCatalog(context.Background(), conn, connData)

	if len(live) != 1 {
		t.Fatalf("got %d models (%v), want only the usable chat model", len(live), liveModelIDs(live))
	}
	if live[0].ID != "gpt-4.1" {
		t.Errorf("got model %q, want %q", live[0].ID, "gpt-4.1")
	}
	if live[0].ContextLength != 128000 || live[0].MaxOutput != 16384 {
		t.Errorf("limits = (%d,%d), want (128000,16384)", live[0].ContextLength, live[0].MaxOutput)
	}
	if !live[0].LimitsAuthoritative {
		t.Error("LimitsAuthoritative = false, want true: the catalogue states this model's own windows")
	}
	if live[0].Capabilities == nil || !live[0].Capabilities.Tools || !live[0].Capabilities.Vision {
		t.Errorf("capabilities = %+v, want tools and vision from the catalogue", live[0].Capabilities)
	}
	if gotAuth != "Bearer tid=copilot-token" {
		t.Errorf("Authorization = %q, want the connection's copilot token", gotAuth)
	}
	if gotIntegration != "vscode-chat" {
		t.Errorf("copilot-integration-id = %q, want vscode-chat", gotIntegration)
	}
}

// TestFetchGithubLiveCatalog_NoTokenMakesNoCall pins that a connection without
// a minted copilot token does not spend a request on an answer it cannot use.
func TestFetchGithubLiveCatalog_NoTokenMakesNoCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	old := githubCopilotModelsURL
	githubCopilotModelsURL = srv.URL
	defer func() { githubCopilotModelsURL = old }()

	h := NewChatHandler(nil)
	conn := &models.ProviderConnection{ID: "conn-gh-notoken", Provider: "github", IsActive: 1}
	if got := h.fetchGithubLiveCatalog(context.Background(), conn, &shared.ConnectionData{}); got != nil {
		t.Fatalf("got %v, want nil without a copilot token", liveModelIDs(got))
	}
	if called {
		t.Error("upstream was called without a credential")
	}
}

// TestFetchGithubLiveCatalog_RejectionFallsBack pins that an upstream refusal
// yields no catalog, so callers keep the static registry instead of serving an
// empty list.
func TestFetchGithubLiveCatalog_RejectionFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	old := githubCopilotModelsURL
	githubCopilotModelsURL = srv.URL
	defer func() { githubCopilotModelsURL = old }()

	h := NewChatHandler(nil)
	conn := &models.ProviderConnection{ID: "conn-gh-401", Provider: "github", IsActive: 1}
	connData := &shared.ConnectionData{ProviderSpecificData: map[string]any{"copilotToken": "tid=x"}}
	if got := h.fetchGithubLiveCatalog(context.Background(), conn, connData); got != nil {
		t.Fatalf("got %v, want nil on a rejected catalog", liveModelIDs(got))
	}
}

func liveModelIDs(models []LiveModel) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}
