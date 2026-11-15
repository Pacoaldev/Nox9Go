package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nox9go/proxy/internal/constants"
	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/quotalearn"
)

// Handler answers MCP JSON-RPC 2.0 requests. Every tool it exposes is
// read-only: an MCP client inspects the gateway, it never mutates routing.
// Mutating routing from a language model would be a control-plane action and
// belongs to the dashboard, behind the operator's own session.
type Handler struct {
	repo  *db.Repo
	quota *quotalearn.Learner
}

// NewHandler builds an MCP handler over the gateway's repository. quota may be
// nil: the quota tool then reports the feature as unavailable instead of
// failing the whole endpoint.
func NewHandler(repo *db.Repo, quota *quotalearn.Learner) *Handler {
	return &Handler{repo: repo, quota: quota}
}

// ServeHTTP accepts one JSON-RPC message and returns one response. A request
// that asks for text/event-stream gets SSE framing, which is what the MCP
// streamable-HTTP transport negotiates; anything else gets plain JSON.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// GET on /mcp is the MCP client's optional server-push channel. This
		// server pushes nothing, so it answers with an empty stream instead of
		// holding the connection open forever.
		w.Header().Set(constants.HeaderContentType, constants.ContentTypeEventStream)
		w.WriteHeader(http.StatusOK)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.write(w, r, Response{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &Error{Code: codeParseError, Message: "failed to read request body"}})
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		h.write(w, r, Response{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &Error{Code: codeParseError, Message: "invalid JSON"}})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		h.write(w, r, Response{JSONRPC: "2.0", ID: req.ID,
			Error: &Error{Code: codeInvalidRequest, Message: "expected a JSON-RPC 2.0 request with a method"}})
		return
	}

	h.write(w, r, h.dispatch(req))
}

// dispatch routes one request to its method.
func (h *Handler) dispatch(req Request) Response {
	resp := Response{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "nox9go",
				"version": "1.0.0",
			},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": h.tools()}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				resp.Error = &Error{Code: codeInvalidParams, Message: "invalid tools/call params"}
				return resp
			}
		}
		if params.Name == "" {
			resp.Error = &Error{Code: codeInvalidParams, Message: "tools/call requires a tool name"}
			return resp
		}
		resp.Result = h.callTool(params.Name, params.Arguments)
	case "notifications/initialized", "notifications/cancelled":
		// Notifications carry no id and expect no response body. Signalling
		// that with an empty ID lets write() answer 204 instead of emitting a
		// JSON-RPC object with a null id, which a client would misread as an
		// unsolicited response.
		resp.ID = nil
	default:
		resp.Error = &Error{Code: codeMethodNotFound, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
	return resp
}

// write serialises one response, honouring the client's framing preference.
func (h *Handler) write(w http.ResponseWriter, r *http.Request, resp Response) {
	if resp.ID == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		payload = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"failed to encode response"}}`)
	}

	if !wantsSSE(r) {
		w.Header().Set(constants.HeaderContentType, constants.ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
		return
	}

	// A single-event stream: SSE framing with the JSON-RPC response as the
	// event data. Closing straight after the event is valid — the client reads
	// one message per response, exactly as the JSON body form.
	w.Header().Set(constants.HeaderContentType, constants.ContentTypeEventStream)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// wantsSSE reports whether the client negotiated the SSE framing.
func wantsSSE(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		if strings.HasPrefix(strings.TrimSpace(part), "text/event-stream") {
			return true
		}
	}
	return false
}
