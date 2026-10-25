package executor

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

func TestPerplexityWeb_ParseOpenAIMessages(t *testing.T) {
	tests := []struct {
		name        string
		messages    []pplxInboundMessage
		wantSys     string
		wantHistory int
		wantCurrent string
	}{
		{
			name: "extracts system, history and current user turn",
			messages: []pplxInboundMessage{
				{Role: "system", Content: "Be helpful"},
				{Role: "user", Content: "Q1"},
				{Role: "assistant", Content: "A1"},
				{Role: "user", Content: "Q2"},
			},
			wantSys:     "Be helpful",
			wantHistory: 2, // Q1, A1
			wantCurrent: "Q2",
		},
		{
			name: "converts developer role to system",
			messages: []pplxInboundMessage{
				{Role: "developer", Content: "Internal instructions"},
				{Role: "user", Content: "Hello"},
			},
			wantSys:     "Internal instructions",
			wantHistory: 0,
			wantCurrent: "Hello",
		},
		{
			name: "handles empty history with single user turn",
			messages: []pplxInboundMessage{
				{Role: "user", Content: "Lone query"},
			},
			wantSys:     "",
			wantHistory: 0,
			wantCurrent: "Lone query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parsePplxMessages(tt.messages)
			if strings.TrimSpace(parsed.systemMsg) != tt.wantSys {
				t.Errorf("systemMsg = %q, want %q", parsed.systemMsg, tt.wantSys)
			}
			if len(parsed.history) != tt.wantHistory {
				t.Errorf("history len = %d, want %d", len(parsed.history), tt.wantHistory)
			}
			if parsed.currentMsg != tt.wantCurrent {
				t.Errorf("currentMsg = %q, want %q", parsed.currentMsg, tt.wantCurrent)
			}
		})
	}
}

func TestPerplexityWeb_BuildPplxRequestBody_DualQueryStr(t *testing.T) {
	// S1: query_str must be present at both top-level AND inside params
	body := buildPplxRequestBody("what is go?", "copilot", "claude46opus", "uuid-prev-123")
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	topLevel, ok := parsed["query_str"].(string)
	if !ok || topLevel != "what is go?" {
		t.Errorf("top-level query_str = %v, want %q", parsed["query_str"], "what is go?")
	}

	params, ok := parsed["params"].(map[string]any)
	if !ok {
		t.Fatalf("params is not an object: %v", parsed["params"])
	}

	paramsQuery, ok := params["query_str"].(string)
	if !ok || paramsQuery != "what is go?" {
		t.Errorf("params.query_str = %v, want %q", params["query_str"], "what is go?")
	}

	if params["mode"] != "copilot" {
		t.Errorf("params.mode = %v, want 'copilot'", params["mode"])
	}
	if params["model_preference"] != "claude46opus" {
		t.Errorf("params.model_preference = %v, want 'claude46opus'", params["model_preference"])
	}
	if params["last_backend_uuid"] != "uuid-prev-123" {
		t.Errorf("params.last_backend_uuid = %v, want 'uuid-prev-123'", params["last_backend_uuid"])
	}
	if params["version"] != "2.18" {
		t.Errorf("params.version = %v, want '2.18'", params["version"])
	}
}

func TestPerplexityWeb_BuildQuery_FirstTurnVsFollowUp(t *testing.T) {
	// S7: First turn creates JSON with instructions and query; follow-up uses plain text
	firstTurn := pplxParsedMessages{
		systemMsg:  "You are caveman.",
		history:    nil,
		currentMsg: "How make fire?",
	}
	q1 := buildPplxQuery(firstTurn, "", nil)
	var obj map[string]any
	if err := json.Unmarshal([]byte(q1), &obj); err != nil {
		t.Fatalf("first turn query is not JSON: %v, got %q", err, q1)
	}
	if obj["query"] != "How make fire?" {
		t.Errorf("obj.query = %v, want 'How make fire?'", obj["query"])
	}

	// Follow-up: backend uuid present -> returns plain text
	followUp := pplxParsedMessages{
		systemMsg: "You are caveman.",
		history: []pplxHistoryEntry{
			{Role: "user", Content: "How make fire?"},
			{Role: "assistant", Content: "Rub sticks together."},
		},
		currentMsg: "Sticks broke, now what?",
	}
	q2 := buildPplxQuery(followUp, "prev-uuid-456", nil)
	if q2 != "Sticks broke, now what?" {
		t.Errorf("follow-up query = %q, want 'Sticks broke, now what?'", q2)
	}
}

func TestPerplexityWeb_CookieHeader(t *testing.T) {
	// S2: Pasted cookie value becomes Cookie: __Secure-next-auth.session-token=<val>
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "my-token-123",
			want:  "__Secure-next-auth.session-token=my-token-123",
		},
		{
			input: "__Secure-next-auth.session-token=already-prefixed-456",
			want:  "__Secure-next-auth.session-token=already-prefixed-456",
		},
	}

	for _, tt := range tests {
		got := pplxCookieHeader(tt.input)
		if got != tt.want {
			t.Errorf("pplxCookieHeader(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPerplexityWeb_ModelResolution(t *testing.T) {
	// S5: Model map + thinking map
	tests := []struct {
		model    string
		thinking bool
		wantMode string
		wantPref string
	}{
		{"pplx-auto", false, "concise", "pplx_pro"},
		{"pplx-sonar", false, "copilot", "experimental"},
		{"pplx-gpt", false, "copilot", "gpt54"},
		{"pplx-gemini", false, "copilot", "gemini31pro_high"},
		{"pplx-sonnet", false, "copilot", "claude46sonnet"},
		{"pplx-opus", false, "copilot", "claude46opus"},
		{"pplx-nemotron", false, "copilot", "nv_nemotron_3_super"},
		// Thinking variants
		{"pplx-gpt", true, "copilot", "gpt54_thinking"},
		{"pplx-sonnet", true, "copilot", "claude46sonnetthinking"},
		{"pplx-opus", true, "copilot", "claude46opusthinking"},
	}

	for _, tt := range tests {
		mode, pref := resolvePplxModel(tt.model, tt.thinking)
		if mode != tt.wantMode || pref != tt.wantPref {
			t.Errorf("resolvePplxModel(%q, %v) = (%q, %q), want (%q, %q)",
				tt.model, tt.thinking, mode, pref, tt.wantMode, tt.wantPref)
		}
	}
}
