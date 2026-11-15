package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
)

// newTestHandler builds an MCP handler over a temporary SQLite file with no
// connections, which is the honest empty-gateway baseline.
func newTestHandler(t *testing.T) (*Handler, *db.Repo) {
	t.Helper()
	tmp, err := os.CreateTemp("", "mcp_handlers_*.sqlite")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	tmp.Close()

	database, err := db.OpenDatabase(tmp.Name())
	if err != nil {
		os.Remove(tmp.Name())
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(tmp.Name())
	})

	repo := db.NewRepo(database)
	if err := db.EnsureCoreSchema(database); err != nil {
		database.Close()
		os.Remove(tmp.Name())
		t.Fatalf("ensure schema: %v", err)
	}
	return NewHandler(repo, nil), repo
}

// post sends one JSON body to the handler and returns the recorded response.
func post(t *testing.T, h *Handler, body string, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMCPInitialize(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want object", resp.Result)
	}
	if got := result["protocolVersion"]; got != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", got, ProtocolVersion)
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo is %T, want object", result["serverInfo"])
	}
	if info["name"] != "nox9go" {
		t.Errorf("serverInfo.name = %v, want nox9go", info["name"])
	}
}

func TestMCPPing(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":"abc","method":"ping"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if string(resp.ID) != `"abc"` {
		t.Errorf("id = %s, want \"abc\"", resp.ID)
	}
}

func TestMCPToolsList(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no inputSchema", tool.Name)
		}
	}

	for _, want := range []string{"list_models", "provider_health", "usage", "combo_strategy", "quota_state"} {
		if !names[want] {
			t.Errorf("tools/list missing %q", want)
		}
	}
}

func TestMCPToolsCallUnknownToolIsToolError(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Error  *Error          `json:"error"`
		Result *CallToolResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unknown tool must be a tool error, got JSON-RPC error %+v", resp.Error)
	}
	if resp.Result == nil || !resp.Result.IsError {
		t.Fatalf("result = %+v, want isError true", resp.Result)
	}
}

func TestMCPToolsCallListModelsEmptyGateway(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_models"}}`, "")
	var resp struct {
		Result *CallToolResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Result == nil || resp.Result.IsError {
		t.Fatalf("result = %+v, want a successful empty listing", resp.Result)
	}
	if len(resp.Result.Content) == 0 || resp.Result.Content[0].Text == "" {
		t.Fatalf("result content is empty: %+v", resp.Result)
	}
}

func TestMCPToolsCallUsage(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"usage"}}`, "")
	var resp struct {
		Error  *Error          `json:"error"`
		Result *CallToolResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	if resp.Result == nil || resp.Result.IsError {
		t.Fatalf("result = %+v, want successful usage payload", resp.Result)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, "activeRequests") {
		t.Errorf("usage payload missing activeRequests: %+v", resp.Result.Content)
	}
}

func TestMCPToolsCallQuotaStateWithoutLearner(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"quota_state","arguments":{"provider":"openai","model":"gpt-4.1"}}}`, "")
	var resp struct {
		Result *CallToolResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Result == nil || !resp.Result.IsError {
		t.Fatalf("result = %+v, want isError true when the learner is absent", resp.Result)
	}
}

func TestMCPInvalidJSON(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{not json`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Fatalf("error = %+v, want parse error %d", resp.Error, codeParseError)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`, "")
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("error = %+v, want method not found %d", resp.Error, codeMethodNotFound)
	}
}

func TestMCPNotificationHasNoBody(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestMCPSSEFraming(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := post(t, h, `{"jsonrpc":"2.0","id":7,"method":"ping"}`, "text/event-stream")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: message") {
		t.Errorf("body missing SSE event name: %q", body)
	}
	dataLine := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
		}
	}
	if dataLine == "" {
		t.Fatalf("body has no data line: %q", body)
	}
	var resp Response
	if err := json.Unmarshal([]byte(dataLine), &resp); err != nil {
		t.Fatalf("SSE data is not a JSON-RPC response: %v (%q)", err, dataLine)
	}
	if string(resp.ID) != "7" {
		t.Errorf("id = %s, want 7", resp.ID)
	}
}

func TestMCPGetReturnsEmptyStream(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}
