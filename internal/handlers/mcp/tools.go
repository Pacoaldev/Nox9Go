package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nox9go/proxy/internal/usagetracker"
)

// tools returns the MCP tool catalog. Every tool is read-only: an MCP client
// inspects the gateway, it never changes routing from inside a model turn.
func (h *Handler) tools() []Tool {
	emptySchema := map[string]any{"type": "object", "properties": map[string]any{}}
	return []Tool{
		{
			Name:        "list_models",
			Description: "List the models the gateway can currently route to, grouped by provider, with the connection count that can serve each one.",
			InputSchema: emptySchema,
		},
		{
			Name:        "provider_health",
			Description: "Report each provider's active connection count and whether it has at least one usable connection.",
			InputSchema: emptySchema,
		},
		{
			Name:        "usage",
			Description: "Report in-flight and recently completed requests per model and provider, as the dashboard's usage tracker sees them.",
			InputSchema: emptySchema,
		},
		{
			Name:        "combo_strategy",
			Description: "Read the configured combos and their routing strategy. Optionally filter to one combo by name.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Return only the combo with this exact name.",
					},
				},
			},
		},
		{
			Name:        "quota_state",
			Description: "Report learned rate-limit ceilings and active cooldowns for one provider/model/connection, as learned from upstream rate-limit headers and 429 responses.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"provider", "model"},
				"properties": map[string]any{
					"provider":   map[string]any{"type": "string", "description": "Provider id, e.g. openai."},
					"model":      map[string]any{"type": "string", "description": "Model id as the provider names it."},
					"connection": map[string]any{"type": "string", "description": "Connection id. Omit for every connection."},
				},
			},
		},
	}
}

// callTool dispatches a tools/call by name. An unknown name is a tool-level
// error, not a JSON-RPC error: MCP clients read the failure from isError.
func (h *Handler) callTool(name string, args map[string]any) CallToolResult {
	switch name {
	case "list_models":
		return h.listModels()
	case "provider_health":
		return h.providerHealth()
	case "usage":
		return h.usage()
	case "combo_strategy":
		return h.comboStrategy(args)
	case "quota_state":
		return h.quotaState(args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name))
	}
}

// listModels groups active connections by provider.
func (h *Handler) listModels() CallToolResult {
	conns, err := h.repo.GetProviderConnections("", true)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read connections: %v", err))
	}

	counts := map[string]int{}
	for _, c := range conns {
		if c.Provider == "" {
			continue
		}
		counts[c.Provider]++
	}

	providers := make([]string, 0, len(counts))
	for p := range counts {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	var b strings.Builder
	if len(providers) == 0 {
		b.WriteString("No active provider connections are configured.")
	}
	for _, p := range providers {
		fmt.Fprintf(&b, "%s: %d active connection(s)\n", p, counts[p])
	}
	return textResult(b.String())
}

// providerHealth reports the same active-connection view as list_models, with
// an explicit usable/empty verdict per provider.
func (h *Handler) providerHealth() CallToolResult {
	conns, err := h.repo.GetProviderConnections("", true)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read connections: %v", err))
	}

	counts := map[string]int{}
	for _, c := range conns {
		if c.Provider == "" {
			continue
		}
		counts[c.Provider]++
	}

	providers := make([]string, 0, len(counts))
	for p := range counts {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	type row struct {
		Provider    string `json:"provider"`
		Connections int    `json:"connections"`
		Usable      bool   `json:"usable"`
	}
	rows := make([]row, 0, len(providers))
	for _, p := range providers {
		rows = append(rows, row{Provider: p, Connections: counts[p], Usable: counts[p] > 0})
	}

	payload, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("failed to encode health: %v", err))
	}
	return textResult(string(payload))
}

// comboStrategy reads the configured combos, optionally filtered by name.
func (h *Handler) comboStrategy(args map[string]any) CallToolResult {
	combos, err := h.repo.GetCombos()
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read combos: %v", err))
	}

	want, _ := args["name"].(string)
	want = strings.TrimSpace(want)

	type row struct {
		Name     string `json:"name"`
		Strategy string `json:"strategy"`
		Models   string `json:"models,omitempty"`
	}
	rows := make([]row, 0, len(combos))
	for _, c := range combos {
		if want != "" && c.Name != want {
			continue
		}
		rows = append(rows, row{Name: c.Name, Strategy: c.Strategy, Models: c.Models})
	}

	if want != "" && len(rows) == 0 {
		return errorResult(fmt.Sprintf("no combo named %q", want))
	}
	if len(rows) == 0 {
		return textResult("No combos are configured; requests route by explicit provider/model.")
	}

	payload, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("failed to encode combos: %v", err))
	}
	return textResult(string(payload))
}

// usage reports the usage tracker's in-flight and recent request view.
func (h *Handler) usage() CallToolResult {
	payload := usagetracker.GetTracker().GetActiveState(h.repo)

	type summary struct {
		ActiveRequests []usagetracker.ActiveRequest `json:"activeRequests"`
		RecentRequests []usagetracker.RecentRequest `json:"recentRequests"`
	}
	out := summary{ActiveRequests: payload.ActiveRequests, RecentRequests: payload.RecentRequests}
	if out.ActiveRequests == nil {
		out.ActiveRequests = []usagetracker.ActiveRequest{}
	}
	if out.RecentRequests == nil {
		out.RecentRequests = []usagetracker.RecentRequest{}
	}

	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("failed to encode usage: %v", err))
	}
	return textResult(string(encoded))
}

// quotaState reports the learner's view for one provider/model and either one
func (h *Handler) quotaState(args map[string]any) CallToolResult {
	if h.quota == nil {
		return errorResult("quota learning is not enabled in this build")
	}

	provider, _ := args["provider"].(string)
	model, _ := args["model"].(string)
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return errorResult("quota_state requires both provider and model")
	}

	if conn, _ := args["connection"].(string); strings.TrimSpace(conn) != "" {
		snap := h.quota.Snapshot(provider, model, strings.TrimSpace(conn))
		return textResult(encodeSnapshot(provider, model, conn, snap))
	}

	conns, err := h.repo.GetProviderConnections(provider, true)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read connections: %v", err))
	}
	if len(conns) == 0 {
		return errorResult(fmt.Sprintf("no active connections for provider %q", provider))
	}

	parts := make([]string, 0, len(conns))
	for _, c := range conns {
		parts = append(parts, encodeSnapshot(provider, model, c.ID, h.quota.Snapshot(provider, model, c.ID)))
	}
	return textResult(strings.Join(parts, "\n"))
}

// encodeSnapshot renders one quota snapshot as indented JSON.
func encodeSnapshot(provider, model, conn string, snap interface {
}) string {
	payload, err := json.MarshalIndent(map[string]any{
		"provider":   provider,
		"model":      model,
		"connection": conn,
		"quota":      snap,
	}, "", "  ")
	if err != nil {
		return fmt.Sprintf("failed to encode snapshot: %v", err)
	}
	return string(payload)
}
