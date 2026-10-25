package executor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

)

// PerplexityWeb protocol constants.
const (
	pplxWebEndpoint  = "https://www.perplexity.ai/rest/sse/perplexity_ask"
	pplxUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	pplxCookieName = "__Secure-next-auth.session-token"
	pplxAPIVersion    = "2.18"
	pplxSessionTTL    = time.Hour
	pplxSessionMax  = 200
	pplxMaxQueryChars = 96000
)

var (
	pplxCitationRE  = regexp.MustCompile(`\[\d+\]`)
	pplxGrokTagRE   = regexp.MustCompile(`(?s)<grok:[^>]*>.*?</grok:[^>]*>`)
	pplxGrokSelfRE  = regexp.MustCompile(`<grok:[^>]*/>`)
	pplxXMLDeclRE   = regexp.MustCompile(`<\?xml[^?]*\?>`)
	pplxResponseTagRE = regexp.MustCompile(`(?i)</?response\b[^>]*>`)
	pplxMultiSpaceRE = regexp.MustCompile(` {2,}`)
	pplxMultiNLRE    = regexp.MustCompile(`\n{3,}`)
)

// Model map for Perplexity Web (parity: decolua/9router open-sse/executors/perplexity-web.js)
var pplxModelMap = map[string][2]string{
	"pplx-auto":     {"concise", "pplx_pro"},
	"pplx-sonar":    {"copilot", "experimental"},
	"pplx-gpt":      {"copilot", "gpt54"},
	"pplx-gemini":   {"copilot", "gemini31pro_high"},
	"pplx-sonnet":   {"copilot", "claude46sonnet"},
	"pplx-opus":     {"copilot", "claude46opus"},
	"pplx-nemotron": {"copilot", "nv_nemotron_3_super"},
}

// Thinking preference map.
var pplxThinkingMap = map[string]string{
	"pplx-gpt":    "gpt54_thinking",
	"pplx-sonnet": "claude46sonnetthinking",
	"pplx-opus":   "claude46opusthinking",
}

// --- Inbound request shapes ---

type pplxInboundMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type pplxInboundTool struct {
	Type     string `json:"type"`
	Function *struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"function"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type pplxInboundRequest struct {
	Model           string              `json:"model"`
	Messages        []pplxInboundMessage `json:"messages"`
	Stream          bool                `json:"stream"`
	Thinking        *bool               `json:"thinking"`
	ReasoningEffort string              `json:"reasoning_effort"`
	Tools           []pplxInboundTool   `json:"tools"`
}

// --- Outbound request shapes ---

type pplxParams struct {
	QueryStr            string   `json:"query_str"`
	SearchFocus         string   `json:"search_focus"`
	Mode                string   `json:"mode"`
	ModelPreference     string   `json:"model_preference"`
	Sources             []string `json:"sources"`
	Attachments         []any    `json:"attachments"`
	FrontendUUID        string   `json:"frontend_uuid"`
	FrontendContextUUID string   `json:"frontend_context_uuid"`
	Version             string   `json:"version"`
	Language            string   `json:"language"`
	Timezone            string   `json:"timezone"`
	SearchRecencyFilter any      `json:"search_recency_filter"`
	IsIncognito         bool     `json:"is_incognito"`
	UseSchematizedAPI   bool     `json:"use_schematized_api"`
	LastBackendUUID     *string  `json:"last_backend_uuid"`
}

type pplxRequestBody struct {
	QueryStr string     `json:"query_str"`
	Params   pplxParams `json:"params"`
}

// --- Parsed history ---

type pplxHistoryEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type pplxParsedMessages struct {
	systemMsg  string
	history    []pplxHistoryEntry
	currentMsg string
}

func parsePplxMessages(messages []pplxInboundMessage) pplxParsedMessages {
	var system strings.Builder
	var history []pplxHistoryEntry

	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			role = "user"
		}
		if role == "developer" {
			role = "system"
		}

		content := extractPplxContent(msg.Content)
		if strings.TrimSpace(content) == "" {
			continue
		}

		switch role {
		case "system":
			system.WriteString(content)
			system.WriteString("\n")
		case "user", "assistant":
			history = append(history, pplxHistoryEntry{Role: role, Content: content})
		}
	}

	var currentMsg string
	if len(history) > 0 && history[len(history)-1].Role == "user" {
		currentMsg = history[len(history)-1].Content
		history = history[:len(history)-1]
	}

	return pplxParsedMessages{
		systemMsg:  strings.TrimSpace(system.String()),
		history:    history,
		currentMsg: currentMsg,
	}
}

func extractPplxContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if txt, _ := m["text"].(string); txt != "" {
						if b.Len() > 0 {
							b.WriteString(" ")
						}
						b.WriteString(txt)
					}
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

// --- Query builder ---

type pplxQueryEnvelope struct {
	Instructions []string           `json:"instructions,omitempty"`
	History      []pplxHistoryEntry `json:"history,omitempty"`
	Query        string             `json:"query"`
}

func buildPplxQuery(parsed pplxParsedMessages, followUpUUID string, tools []pplxInboundTool) string {
	if followUpUUID != "" {
		return parsed.currentMsg
	}

	env := pplxQueryEnvelope{
		Query: parsed.currentMsg,
	}

	var instr []string
	if parsed.systemMsg != "" {
		instr = append(instr, parsed.systemMsg)
	}
	if hint := pplxToolsHint(tools); hint != "" {
		instr = append(instr, hint)
	}
	instr = append(instr, "You have built-in web search. Answer questions directly using search results.")
	env.Instructions = instr

	if len(parsed.history) > 0 {
		env.History = parsed.history
	}

	raw, err := json.Marshal(env)
	if err != nil {
		return parsed.currentMsg
	}

	out := string(raw)
	if len(out) > pplxMaxQueryChars {
		return out[len(out)-pplxMaxQueryChars:]
	}
	return out
}

func pplxToolsHint(tools []pplxInboundTool) string {
	if len(tools) == 0 {
		return ""
	}
	var lines []string
	for _, t := range tools {
		name := t.Name
		desc := t.Description
		if t.Function != nil {
			if t.Function.Name != "" {
				name = t.Function.Name
			}
			if t.Function.Description != "" {
				desc = t.Function.Description
			}
		}
		if name == "" {
			name = "unnamed"
		}
		firstLine := strings.SplitN(desc, "\n", 2)[0]
		if len(firstLine) > 200 {
			firstLine = firstLine[:200]
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", name, firstLine))
	}
	return "Available tools (reference only, cannot invoke):\n" + strings.Join(lines, "\n")
}

func buildPplxRequestBody(query, mode, modelPref, followUpUUID string) pplxRequestBody {
	tz := time.Now().Location().String()
	var lastBackend *string
	if followUpUUID != "" {
		lastBackend = &followUpUUID
	}

	return pplxRequestBody{
		QueryStr: query,
		Params: pplxParams{
			QueryStr:            query,
			SearchFocus:         "internet",
			Mode:                mode,
			ModelPreference:     modelPref,
			Sources:             []string{"web"},
			Attachments:         []any{},
			FrontendUUID:        uuid.NewString(),
			FrontendContextUUID: uuid.NewString(),
			Version:             pplxAPIVersion,
			Language:            "en-US",
			Timezone:            tz,
			IsIncognito:         true,
			UseSchematizedAPI:   true,
			LastBackendUUID:     lastBackend,
		},
	}
}

func pplxCookieHeader(apiKey string) string {
	token := strings.TrimSpace(apiKey)
	if strings.HasPrefix(token, pplxCookieName+"=") {
		return token
	}
	return pplxCookieName + "=" + token
}

func resolvePplxModel(model string, thinking bool) (mode, preference string) {
	clean := strings.TrimSpace(model)
	if idx := strings.LastIndex(clean, "/"); idx >= 0 {
		clean = clean[idx+1:]
	}

	if thinking {
		if thPref, ok := pplxThinkingMap[clean]; ok {
			return "copilot", thPref
		}
	}

	if mapping, ok := pplxModelMap[clean]; ok {
		return mapping[0], mapping[1]
	}

	return "copilot", clean
}

// --- Session cache ---

type pplxSessionEntry struct {
	backendUUID string
	ts          time.Time
}

type pplxSessionCache struct {
	mu      sync.Mutex
	entries map[string]pplxSessionEntry
}

var globalPplxSessions = &pplxSessionCache{
	entries: make(map[string]pplxSessionEntry),
}

func fnv1aKey(history []pplxHistoryEntry) string {
	var b strings.Builder
	for _, h := range history {
		b.WriteString(h.Role)
		b.WriteString(":")
		b.WriteString(h.Content)
		b.WriteString("\n")
	}
	data := b.String()

	var hash uint32 = 0x811c9dc5
	for i := 0; i < len(data); i++ {
		hash ^= uint32(data[i])
		hash *= 0x01000193
	}
	return fmt.Sprintf("%08x", hash)
}

func (c *pplxSessionCache) Lookup(history []pplxHistoryEntry) string {
	if len(history) == 0 {
		return ""
	}
	key := fnv1aKey(history)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return ""
	}
	if time.Since(entry.ts) > pplxSessionTTL {
		delete(c.entries, key)
		return ""
	}
	return entry.backendUUID
}

func (c *pplxSessionCache) Store(history []pplxHistoryEntry, currentMsg, responseText, backendUUID string) {
	if backendUUID == "" {
		return
	}
	full := make([]pplxHistoryEntry, 0, len(history)+2)
	full = append(full, history...)
	full = append(full, pplxHistoryEntry{Role: "user", Content: currentMsg})
	full = append(full, pplxHistoryEntry{Role: "assistant", Content: responseText})

	key := fnv1aKey(full)

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= pplxSessionMax {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, v := range c.entries {
			if first || v.ts.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.ts
				first = false
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}

	c.entries[key] = pplxSessionEntry{
		backendUUID: backendUUID,
		ts:          time.Now(),
	}
}

// --- Response cleanup ---

func cleanPplxResponse(text string, strip bool) string {
	t := text
	t = pplxXMLDeclRE.ReplaceAllString(t, "")
	t = pplxCitationRE.ReplaceAllString(t, "")
	t = pplxGrokTagRE.ReplaceAllString(t, "")
	t = pplxGrokSelfRE.ReplaceAllString(t, "")
	t = pplxResponseTagRE.ReplaceAllString(t, "")
	if strip {
		t = pplxMultiSpaceRE.ReplaceAllString(t, " ")
		t = pplxMultiNLRE.ReplaceAllString(t, "\n\n")
		t = strings.TrimSpace(t)
	}
	return t
}
