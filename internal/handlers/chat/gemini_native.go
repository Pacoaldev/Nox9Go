package chat

// Gemini-native inbound API.
//
// The gateway translated *to* Gemini upstream but never accepted a Gemini
// client: a Gemini SDK or Gemini CLI could not point at it. These routes add the
// other direction. A Gemini GenerateContentRequest is converted to an OpenAI
// Chat Completions request, routed through the same handler every other client
// uses (so combos, fallback, quota and usage logging all apply), and the answer
// is converted back to the Gemini wire format.
//
// Streaming is decided by the URL action suffix — the canonical Gemini
// convention, and what the @google/genai SDK always sends — never by a body
// field: ":streamGenerateContent" streams, ":generateContent" does not.

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	json "encoding/json/v2"

	"nox9go/proxy/internal/handlerutil"
)

const geminiNativePrefix = "/v1beta/models"

// ---- Gemini request shapes (camelCase, unlike the translator's snake_case) ----

type geminiNativeRequest struct {
	SystemInstruction *geminiNativeContent  `json:"systemInstruction"`
	Contents          []geminiNativeContent `json:"contents"`
	Tools             []geminiNativeTool    `json:"tools"`
	GenerationConfig  *geminiNativeConfig   `json:"generationConfig"`
}

type geminiNativeContent struct {
	Role  string             `json:"role"`
	Parts []geminiNativePart `json:"parts"`
}

type geminiNativePart struct {
	Text             string               `json:"text,omitempty"`
	Thought          *bool                `json:"thought,omitempty"`
	FunctionCall     *geminiNativeCall    `json:"functionCall,omitempty"`
	FunctionResponse *geminiNativeCallRes `json:"functionResponse,omitempty"`
	InlineData       *geminiNativeInline  `json:"inlineData,omitempty"`
}

type geminiNativeCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
	ID   string         `json:"id,omitempty"`
}

type geminiNativeCallRes struct {
	Name     string                     `json:"name"`
	ID       string                     `json:"id,omitempty"`
	Response *geminiNativeCallResResult `json:"response,omitempty"`
}

type geminiNativeCallResResult struct {
	Result any `json:"result,omitempty"`
}

type geminiNativeInline struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiNativeTool struct {
	FunctionDeclarations []geminiNativeFunctionDecl `json:"functionDeclarations"`
}

type geminiNativeFunctionDecl struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiNativeConfig struct {
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"topP,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
}

// ---- OpenAI shapes this file writes and reads ----

type openAIOutMessage struct {
	Role       string          `json:"role"`
	Content    any             `json:"content,omitempty"`
	ToolCalls  []openAIOutCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type openAIOutCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIOutTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Parameters  any    `json:"parameters,omitempty"`
	} `json:"function"`
}

// HandleGeminiModelsList answers GET /v1beta/models in the Gemini list format.
func (h *ChatHandler) HandleGeminiModelsList(w http.ResponseWriter, r *http.Request) {
	setGeminiCORS(w)
	result := h.buildModelsListResult(r.Context(), modeListConnected)
	type geminiModel struct {
		Name                       string   `json:"name"`
		DisplayName                string   `json:"displayName"`
		Description                string   `json:"description"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		InputTokenLimit            int      `json:"inputTokenLimit"`
		OutputTokenLimit           int      `json:"outputTokenLimit"`
	}
	models := make([]geminiModel, 0, len(result.Models))
	seen := make(map[string]bool, len(result.Models))
	for _, m := range result.Models {
		name := "models/" + m.ID
		if m.ID == "" || seen[name] {
			continue
		}
		seen[name] = true
		entry := geminiModel{
			Name:                       name,
			DisplayName:                m.ID,
			Description:                m.OwnedBy + " model: " + m.ID,
			SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
			InputTokenLimit:            128000,
			OutputTokenLimit:           8192,
		}
		if m.ContextLength != nil && *m.ContextLength > 0 {
			entry.InputTokenLimit = *m.ContextLength
		}
		if m.MaxCompletionTokens != nil && *m.MaxCompletionTokens > 0 {
			entry.OutputTokenLimit = *m.MaxCompletionTokens
		}
		models = append(models, entry)
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"models": models})
}

// HandleGeminiGenerateContent answers POST /v1beta/models/{...}:action.
func (h *ChatHandler) HandleGeminiGenerateContent(w http.ResponseWriter, r *http.Request) {
	setGeminiCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	model, stream, ok := parseGeminiModelPath(r.URL.Path)
	if !ok {
		writeGeminiError(w, http.StatusNotFound, "invalid Gemini model path; expected /v1beta/models/<provider>/<model>:generateContent")
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	var geminiReq geminiNativeRequest
	if len(bytes.TrimSpace(rawBody)) > 0 {
		if err := json.Unmarshal(rawBody, &geminiReq); err != nil {
			writeGeminiError(w, http.StatusBadRequest, "invalid Gemini request body")
			return
		}
	}

	openAIBody, err := geminiRequestToOpenAI(&geminiReq, model, stream)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The Gemini action suffix is authoritative: an @google/genai client never
	// sets generationConfig.stream, and honouring a body field here would let the
	// two disagree.
	innerReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/v1/chat/completions", bytes.NewReader(openAIBody))
	if err != nil {
		writeGeminiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	innerReq.Header = r.Header.Clone()
	innerReq.Header.Set("Content-Type", "application/json")

	if stream {
		sw := newGeminiStreamWriter(w, model)
		h.HandleChatCompletions(sw, innerReq)
		sw.Finish()
		return
	}

	capture := &geminiCaptureWriter{header: make(http.Header)}
	h.HandleChatCompletions(capture, innerReq)
	convertOpenAIResponseToGemini(w, capture.statusCode, capture.body.Bytes(), model)
}

// parseGeminiModelPath splits "/v1beta/models/<provider>/<model>:<action>" (or
// "<model>:<action>") into the model string and the streaming intent.
func parseGeminiModelPath(path string) (model string, stream bool, ok bool) {
	rest := strings.TrimPrefix(path, geminiNativePrefix)
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", false, false
	}
	switch {
	case strings.HasSuffix(rest, ":streamGenerateContent"):
		rest = strings.TrimSuffix(rest, ":streamGenerateContent")
		stream = true
	case strings.HasSuffix(rest, ":generateContent"):
		rest = strings.TrimSuffix(rest, ":generateContent")
	default:
		return "", false, false
	}
	rest = strings.Trim(rest, "/")
	rest = strings.TrimPrefix(rest, "models/")
	if rest == "" || strings.ContainsAny(rest, "\\?#") {
		return "", false, false
	}
	return rest, stream, true
}

// geminiRequestToOpenAI converts a Gemini GenerateContentRequest into an OpenAI
// Chat Completions body.
func geminiRequestToOpenAI(req *geminiNativeRequest, model string, stream bool) ([]byte, error) {
	messages := make([]openAIOutMessage, 0, len(req.Contents)+1)

	if req.SystemInstruction != nil {
		if text := geminiPartsText(req.SystemInstruction.Parts); text != "" {
			messages = append(messages, openAIOutMessage{Role: "system", Content: text})
		}
	}

	for _, content := range req.Contents {
		role := "user"
		if content.Role == "model" {
			role = "assistant"
		}
		messages = append(messages, geminiContentMessages(content, role)...)
	}

	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   stream,
	}
	if cfg := req.GenerationConfig; cfg != nil {
		if cfg.MaxOutputTokens > 0 {
			body["max_tokens"] = cfg.MaxOutputTokens
		}
		if cfg.Temperature != nil {
			body["temperature"] = *cfg.Temperature
		}
		if cfg.TopP != nil {
			body["top_p"] = *cfg.TopP
		}
		if len(cfg.StopSequences) > 0 {
			body["stop"] = cfg.StopSequences
		}
		if cfg.ResponseMimeType == "application/json" {
			body["response_format"] = map[string]any{"type": "json_object"}
		}
	}
	if tools := geminiToolsToOpenAI(req.Tools); len(tools) > 0 {
		body["tools"] = tools
	}
	return json.Marshal(body)
}

// geminiContentMessages turns one Gemini content block into the OpenAI messages
// it corresponds to. A block that mixes parts becomes several messages: OpenAI
// has no single message that carries text, a tool call and a tool result at once.
func geminiContentMessages(content geminiNativeContent, role string) []openAIOutMessage {
	var out []openAIOutMessage
	var textParts []string
	var mediaParts []map[string]any
	var calls []openAIOutCall

	for _, part := range content.Parts {
		switch {
		case part.FunctionCall != nil:
			call := openAIOutCall{ID: part.FunctionCall.ID, Type: "function"}
			if call.ID == "" {
				call.ID = "call_" + part.FunctionCall.Name
			}
			call.Function.Name = part.FunctionCall.Name
			if args, err := json.Marshal(part.FunctionCall.Args); err == nil {
				call.Function.Arguments = string(args)
			}
			calls = append(calls, call)
		case part.FunctionResponse != nil:
			result := any(map[string]any{})
			if part.FunctionResponse.Response != nil && part.FunctionResponse.Response.Result != nil {
				result = part.FunctionResponse.Response.Result
			}
			payload, err := json.Marshal(result)
			if err != nil {
				payload = []byte("{}")
			}
			id := part.FunctionResponse.ID
			if id == "" {
				id = "call_" + part.FunctionResponse.Name
			}
			out = append(out, openAIOutMessage{Role: "tool", Content: string(payload), ToolCallID: id})
		case part.InlineData != nil && part.InlineData.MimeType != "" && part.InlineData.Data != "":
			mediaParts = append(mediaParts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:" + part.InlineData.MimeType + ";base64," + part.InlineData.Data},
			})
		case strings.TrimSpace(part.Text) != "":
			textParts = append(textParts, part.Text)
		}
	}

	if len(calls) > 0 {
		out = append(out, openAIOutMessage{Role: "assistant", ToolCalls: calls})
	}
	if len(textParts) > 0 || len(mediaParts) > 0 {
		if len(mediaParts) == 0 {
			out = append(out, openAIOutMessage{Role: role, Content: strings.Join(textParts, "\n")})
		} else {
			parts := make([]map[string]any, 0, len(mediaParts)+1)
			parts = append(parts, mediaParts...)
			if len(textParts) > 0 {
				parts = append(parts, map[string]any{"type": "text", "text": strings.Join(textParts, "\n")})
			}
			out = append(out, openAIOutMessage{Role: role, Content: parts})
		}
	}
	return out
}

func geminiPartsText(parts []geminiNativePart) string {
	var texts []string
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func geminiToolsToOpenAI(tools []geminiNativeTool) []openAIOutTool {
	var out []openAIOutTool
	for _, tool := range tools {
		for _, decl := range tool.FunctionDeclarations {
			if strings.TrimSpace(decl.Name) == "" {
				continue
			}
			entry := openAIOutTool{Type: "function"}
			entry.Function.Name = decl.Name
			entry.Function.Description = decl.Description
			entry.Function.Parameters = decl.Parameters
			out = append(out, entry)
		}
	}
	return out
}

// ---- Response conversion ----

var geminiFinishReason = map[string]string{
	"stop":           "STOP",
	"length":         "MAX_TOKENS",
	"tool_calls":     "STOP",
	"function_call":  "STOP",
	"content_filter": "SAFETY",
}

func geminiFinishFromOpenAI(reason string) string {
	if mapped, ok := geminiFinishReason[reason]; ok {
		return mapped
	}
	return "STOP"
}

// convertOpenAIResponseToGemini writes an OpenAI Chat Completions response as a
// Gemini GenerateContentResponse.
func convertOpenAIResponseToGemini(w http.ResponseWriter, status int, body []byte, model string) {
	if status != 0 && status != http.StatusOK {
		writeGeminiError(w, status, openAIErrorMessage(body))
		return
	}

	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens            int `json:"prompt_tokens"`
			CompletionTokens        int `json:"completion_tokens"`
			TotalTokens             int `json:"total_tokens"`
			CompletionTokensDetails *struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeGeminiError(w, http.StatusBadGateway, "upstream returned an unparseable completion")
		return
	}
	if len(parsed.Choices) == 0 {
		writeGeminiError(w, http.StatusBadGateway, "upstream returned no choices")
		return
	}

	choice := parsed.Choices[0]
	parts := make([]map[string]any, 0, 2)
	if choice.Message.ReasoningContent != "" {
		parts = append(parts, map[string]any{"text": choice.Message.ReasoningContent, "thought": true})
	}
	if choice.Message.Content != "" {
		parts = append(parts, map[string]any{"text": choice.Message.Content})
	}
	for _, call := range choice.Message.ToolCalls {
		args := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		}
		parts = append(parts, map[string]any{
			"functionCall": map[string]any{"name": call.Function.Name, "args": args},
		})
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": ""})
	}

	response := map[string]any{
		"candidates": []map[string]any{{
			"content":      map[string]any{"role": "model", "parts": parts},
			"finishReason": geminiFinishFromOpenAI(choice.FinishReason),
			"index":        0,
		}},
		"modelVersion": firstNonEmptyStringOf(parsed.Model, model),
	}
	if parsed.Usage != nil {
		usage := map[string]any{
			"promptTokenCount":     parsed.Usage.PromptTokens,
			"candidatesTokenCount": parsed.Usage.CompletionTokens,
			"totalTokenCount":      parsed.Usage.TotalTokens,
		}
		if d := parsed.Usage.CompletionTokensDetails; d != nil && d.ReasoningTokens > 0 {
			usage["thoughtsTokenCount"] = d.ReasoningTokens
		}
		response["usageMetadata"] = usage
	}
	handlerutil.WriteJSON(w, http.StatusOK, response)
}

func firstNonEmptyStringOf(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func openAIErrorMessage(body []byte) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Error.Message != "" {
			return parsed.Error.Message
		}
		if parsed.Error.Type != "" {
			return parsed.Error.Type
		}
	}
	return "upstream request failed"
}

// ---- Writers ----

func setGeminiCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}

// writeGeminiError answers with the Gemini error envelope, which is what a
// Gemini client parses.
func writeGeminiError(w http.ResponseWriter, status int, message string) {
	handlerutil.WriteJSON(w, status, map[string]any{
		"error": map[string]any{"code": status, "message": message, "status": http.StatusText(status)},
	})
}

// geminiCaptureWriter records a non-streamed response so it can be converted
// before anything reaches the client.
type geminiCaptureWriter struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
}

func (c *geminiCaptureWriter) Header() http.Header { return c.header }

func (c *geminiCaptureWriter) WriteHeader(status int) {
	if c.statusCode == 0 {
		c.statusCode = status
	}
}

func (c *geminiCaptureWriter) Write(p []byte) (int, error) {
	if c.statusCode == 0 {
		c.statusCode = http.StatusOK
	}
	return c.body.Write(p)
}

// geminiStreamWriter turns the OpenAI SSE frames the chat handler produces into
// Gemini SSE frames on the fly.
type geminiStreamWriter struct {
	http.ResponseWriter
	model       string
	status      int
	wroteHeader bool
	pending     []byte
	finished    bool
}

func newGeminiStreamWriter(w http.ResponseWriter, model string) *geminiStreamWriter {
	return &geminiStreamWriter{ResponseWriter: w, model: model}
}

func (g *geminiStreamWriter) WriteHeader(status int) {
	if g.status == 0 {
		g.status = status
	}
}

func (g *geminiStreamWriter) Flush() {
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *geminiStreamWriter) Write(p []byte) (int, error) {
	if !g.wroteHeader {
		g.wroteHeader = true
		if g.status == 0 {
			g.status = http.StatusOK
		}
		if g.status != http.StatusOK {
			// An error body is not a stream: hand it over untouched, so the
			// client reads the same failure the OpenAI lane produced.
			g.ResponseWriter.WriteHeader(g.status)
			return g.ResponseWriter.Write(p)
		}
		g.Header().Set("Content-Type", "text/event-stream")
		g.Header().Set("Cache-Control", "no-cache")
		g.Header().Set("Access-Control-Allow-Origin", "*")
		g.ResponseWriter.WriteHeader(http.StatusOK)
	}
	if g.status != http.StatusOK {
		return g.ResponseWriter.Write(p)
	}
	g.pending = append(g.pending, p...)
	g.drain()
	return len(p), nil
}

// drain converts every complete line buffered so far.
func (g *geminiStreamWriter) drain() {
	for {
		idx := bytes.IndexByte(g.pending, '\n')
		if idx < 0 {
			return
		}
		line := strings.TrimRight(string(g.pending[:idx]), "\r")
		g.pending = g.pending[idx+1:]
		g.writeLine(line)
	}
}

// Finish flushes any unterminated remainder, then closes the stream. Gemini SSE
// has no [DONE] sentinel: the stream simply ends.
func (g *geminiStreamWriter) Finish() {
	if len(bytes.TrimSpace(g.pending)) > 0 {
		g.writeLine(strings.TrimRight(string(g.pending), "\r"))
		g.pending = nil
	}
	g.Flush()
}

func (g *geminiStreamWriter) writeLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "event:") || strings.HasPrefix(trimmed, ":") {
		return
	}
	payload := strings.TrimPrefix(trimmed, "data:")
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return
	}
	chunk, ok := openAIChunkToGemini(payload, g.model)
	if !ok {
		return
	}
	_, _ = g.ResponseWriter.Write([]byte("data: " + string(chunk) + "\r\n\r\n"))
	g.Flush()
}

// openAIChunkToGemini maps one OpenAI stream chunk to one Gemini stream chunk.
// It reports false when the chunk carries nothing a Gemini client could read,
// so a role-only preamble does not produce an empty candidate.
func openAIChunkToGemini(payload, model string) ([]byte, bool) {
	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens            int `json:"prompt_tokens"`
			CompletionTokens        int `json:"completion_tokens"`
			TotalTokens             int `json:"total_tokens"`
			CompletionTokensDetails *struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, false
	}
	if len(parsed.Choices) == 0 {
		return nil, false
	}

	choice := parsed.Choices[0]
	parts := make([]map[string]any, 0, 2)
	if choice.Delta.ReasoningContent != "" {
		parts = append(parts, map[string]any{"text": choice.Delta.ReasoningContent, "thought": true})
	}
	if choice.Delta.Content != "" {
		parts = append(parts, map[string]any{"text": choice.Delta.Content})
	}
	for _, call := range choice.Delta.ToolCalls {
		args := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		}
		parts = append(parts, map[string]any{
			"functionCall": map[string]any{"name": call.Function.Name, "args": args},
		})
	}

	finish := ""
	if choice.FinishReason != nil {
		finish = geminiFinishFromOpenAI(*choice.FinishReason)
	}
	if len(parts) == 0 && finish == "" {
		return nil, false
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": ""})
	}

	candidate := map[string]any{
		"content": map[string]any{"role": "model", "parts": parts},
		"index":   0,
	}
	if finish != "" {
		candidate["finishReason"] = finish
	}
	chunk := map[string]any{"candidates": []map[string]any{candidate}}
	if finish != "" {
		chunk["modelVersion"] = firstNonEmptyStringOf(parsed.Model, model)
		if parsed.Usage != nil {
			usage := map[string]any{
				"promptTokenCount":     parsed.Usage.PromptTokens,
				"candidatesTokenCount": parsed.Usage.CompletionTokens,
				"totalTokenCount":      parsed.Usage.TotalTokens,
			}
			if d := parsed.Usage.CompletionTokensDetails; d != nil && d.ReasoningTokens > 0 {
				usage["thoughtsTokenCount"] = d.ReasoningTokens
			}
			chunk["usageMetadata"] = usage
		}
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// compile-time assertion that the capture writer is usable anywhere a handler
// writes a response.
var _ http.ResponseWriter = (*geminiCaptureWriter)(nil)
var _ http.ResponseWriter = (*geminiStreamWriter)(nil)
