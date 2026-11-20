// Package mcp exposes a Model Context Protocol server over JSON-RPC 2.0,
// following the FreeLLMAPI router design: an MCP client can discover the
// gateway's state (usable models, provider health, learned quota, active combo
// strategy) through one endpoint, and ask the gateway to generate an image
// with the operator's own connected account.
//
// The transport here is deliberately the simplest one that every MCP client
// understands: a single POST carrying one JSON-RPC message, answered by a
// single JSON body. Server-Sent Events framing is returned when the request
// asks for it via Accept: text/event-stream, which is what the MCP
// streamable-HTTP transport negotiates.
package mcp

import "encoding/json"

// ProtocolVersion is the MCP protocol revision this server implements.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Request is one inbound JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is one outbound JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC 2.0 error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool describes one callable MCP tool.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Content is one entry of a tool call result payload. Data and MimeType are
// carried only by image blocks, so a text result serialises exactly as it did
// before image generation existed.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// CallToolResult is the result shape MCP defines for tools/call.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// textResult is the convenience constructor for a single text block result.
func textResult(s string) CallToolResult {
	return CallToolResult{Content: []Content{{Type: "text", Text: s}}}
}

// imageResult is the convenience constructor for a single image block. MCP
// carries the image itself as base64 in data, with the media type alongside.
func imageResult(mimeType, data string) CallToolResult {
	return CallToolResult{Content: []Content{{Type: "image", Data: data, MimeType: mimeType}}}
}

// errorResult builds a tool-level failure: MCP reports tool failures inside a
// successful JSON-RPC response, never as a JSON-RPC error.
func errorResult(s string) CallToolResult {
	return CallToolResult{Content: []Content{{Type: "text", Text: s}}, IsError: true}
}
