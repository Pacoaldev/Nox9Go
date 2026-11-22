package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxTelegramMessage is Telegram's hard cap on a single message body. A longer
// reply is split, never truncated.
const maxTelegramMessage = 4096

// chatMessage is one entry of the OpenAI messages array. Content stays raw
// because a text turn is a JSON string while a turn with an image is an array
// of typed parts; json.RawMessage carries either shape without re-encoding.
type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// textMessage builds a plain text turn. It is the shape every existing caller
// keeps using.
func textMessage(role, content string) chatMessage {
	return chatMessage{Role: role, Content: json.RawMessage(mustJSON(content))}
}

// userImageMessage builds a user turn carrying the caption and, when an image
// is attached, the OpenAI multimodal part array.	f (attachment == nil) a
// caption-only string turn is emitted.
func userImageMessage(caption string, attachment *imageAttachment) chatMessage {
	if attachment == nil {
		return textMessage("user", caption)
	}
	parts := []map[string]any{{"type": "text", "text": caption}}
	parts = append(parts, map[string]any{
		"type": "image_url",
		"image_url": map[string]string{"url": attachment.dataURL()},
	})
	return chatMessage{Role: "user", Content: json.RawMessage(mustJSON(parts))}
}

// mustJSON is one-shot encoding for values the code itself constructed.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("telegram: encode %T: %w", v, err))
	}
	return b
}

// gatewayClient is the pooled client used for every call to the instance's own
// chat route. It is package-level so the bot does not build a new transport per
// message: the gateway runs on small hardware and a fresh TLS/idle pool per turn
// is exactly the kind of waste that shows up as latency.
var gatewayClient = &http.Client{Timeout: 5 * time.Minute}

// forwardText sends a completion request to the instance's own
// /v1/chat/completions route and returns the assistant text.
//
// The credential is the channel's own client API key, not the CLI token: the
// engine's chat route guards itself with RequireApiKey, which the CLI token
// does not satisfy.
//
// The call goes through the gateway rather than to a provider directly, so combo
// expansion, failover, strike handling, quota accounting and usage history all
// apply to a Telegram turn exactly as they do to an API client.
func forwardText(ctx context.Context, baseURL, cliToken, model string, messages []chatMessage) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   false,
	})
	if err != nil {
		return "", fmt.Errorf("telegram: encode request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("telegram: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The channel's own API key identifies these turns to the engine. It is
	// never sent anywhere else: the URL is the local listener.
	req.Header.Set("Authorization", "Bearer "+cliToken)

	resp, err := gatewayClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("telegram: call gateway: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("telegram: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("telegram: gateway returned %d: %s", resp.StatusCode, upstreamErrorMessage(payload))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", fmt.Errorf("telegram: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("telegram: gateway returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}

// upstreamErrorMessage extracts the provider's own message when the body carries
// one, so a failure reads as "the provider said X" instead of a bare status code.
func upstreamErrorMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return envelope.Error.Message
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		text = text[:500] + "…"
	}
	if text == "" {
		return "empty response body"
	}
	return text
}

// splitMessage breaks text into chunks Telegram accepts.
//
// It prefers the last newline inside the window so a line, a sentence or a code
// block is not cut mid-way, and falls back to a hard cut when no newline is
// available. Concatenating the parts always reproduces the input exactly: no
// character is dropped, which is the property the tests pin.
func splitMessage(text string, limit int) []string {
	if limit <= 0 {
		limit = maxTelegramMessage
	}
	if len(text) <= limit {
		return []string{text}
	}

	var parts []string
	remaining := text
	for len(remaining) > limit {
		cut := limit
		if idx := strings.LastIndexByte(remaining[:limit], '\n'); idx > 0 {
			// Keep the newline with the part that ends there, so joining the
			// parts reproduces the original byte for byte.
			cut = idx + 1
		}
		parts = append(parts, remaining[:cut])
		remaining = remaining[cut:]
	}
	if remaining != "" {
		parts = append(parts, remaining)
	}
	return parts
}

// parseCommand reports whether text is a bot command and returns its name
// (without the leading slash and without the @bot suffix) plus its arguments.
func parseCommand(text string) (cmd, args string, isCommand bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") || len(text) < 2 {
		return "", "", false
	}
	body := text[1:]
	name, rest, _ := strings.Cut(body, " ")
	// "/status@my_bot" addresses one bot in a group; the name is what matters.
	if at := strings.IndexByte(name, '@'); at > 0 {
		name = name[:at]
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(rest), true
}

// fetchModelIDs reads the model catalog from this instance's own
// /api/channels/telegram/models route, so /model offers what this deployment can
// actually dispatch instead of a hardcoded list that goes stale the moment a
// connection is added.
//
// The route is a dashboard route, so it takes the CLI token, and it answers a
// plain {"models": [...]} list because it builds the list through the engine
// rather than through the OpenAI wire shape.
func fetchModelIDs(ctx context.Context, baseURL, cliToken string) ([]string, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/channels/telegram/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: build models request: %w", err)
	}
	req.Header.Set("x-9r-cli-token", cliToken)

	resp, err := gatewayClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: list models: status %d", resp.StatusCode)
	}

	var out struct {
		Models []string `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("telegram: decode models: %w", err)
	}
	ids := make([]string, 0, len(out.Models))
	for _, id := range out.Models {
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
