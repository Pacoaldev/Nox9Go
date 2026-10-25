package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	json "encoding/json/v2"

	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/proxy"
	"nox9go/proxy/internal/translator"
)

// ForwardPerplexityWeb routes a chat completion to Perplexity's own web
// endpoint. Perplexity Web has no OpenAI-compatible API: it answers
// text/event-stream frames carrying `blocks`/`markdown_block`, and it insists on
// `query_str` at the top level and inside `params`. A request that carries
// OpenAI's `messages` is refused with 422 "Field required: body.query_str".
//
// The executor therefore does two translations the provider would otherwise be
// asked for: the inbound conversation is folded into the site's own query
// envelope, and the site's events are turned back into OpenAI chunks. Parity
// reference: decolua/9router open-sse/executors/perplexity-web.js.
//
// The credential is a browser session cookie rather than an API key, so it
// travels in the Cookie header. Everything the router owns — model resolution,
// account rotation, usage accounting, cooling down — stays with the router;
// this returns an *UpstreamError carrying the status it has to act on.
func ForwardPerplexityWeb(w http.ResponseWriter, req *Request) error {
	ctx := reqCtx(req)

	var inbound pplxInboundRequest
	if err := json.Unmarshal(req.Body, &inbound); err != nil {
		return proxy.UpstreamFailure(http.StatusBadRequest, "perplexity-web: parse request: "+err.Error())
	}
	if len(inbound.Messages) == 0 {
		return proxy.UpstreamFailure(http.StatusBadRequest, "perplexity-web: missing or empty messages array")
	}

	parsed := parsePplxMessages(inbound.Messages)
	thinking := pplxThinkingRequested(inbound)
	mode, modelPref := resolvePplxModel(inbound.Model, thinking)

	followUpUUID := globalPplxSessions.Lookup(parsed.history)
	query := buildPplxQuery(parsed, followUpUUID, inbound.Tools)
	if strings.TrimSpace(query) == "" {
		return proxy.UpstreamFailure(http.StatusBadRequest, "perplexity-web: empty query after processing")
	}

	body, err := json.Marshal(buildPplxRequestBody(query, mode, modelPref, followUpUUID))
	if err != nil {
		return proxy.UpstreamFailure(http.StatusInternalServerError, "perplexity-web: encode request: "+err.Error())
	}

	headers := map[string]string{
		"Content-Type":     "application/json",
		"Accept":           "text/event-stream",
		"Origin":           "https://www.perplexity.ai",
		"Referer":          "https://www.perplexity.ai/",
		"User-Agent":       pplxUserAgent,
		"X-App-ApiClient":  "default",
		"X-App-ApiVersion": pplxAPIVersion,
		"Cookie":           pplxCookieHeader(req.APIKey),
	}

	log.Info("perplexity-web", "turn",
		"model", inbound.Model, "pref", modelPref, "mode", mode,
		"query_len", len(query), "follow_up", followUpUUID != "")

	resp, err := proxy.DoRequest(ctx, req.Client, http.MethodPost, pplxEndpointOf(req), headers, body)
	if err != nil {
		return fmt.Errorf("ForwardPerplexityWeb upstream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return pplxStatusError(resp)
	}

	if req.IsStream {
		if err := writePplxStream(ctx, w, req, resp.Body); err != nil {
			return err
		}
	} else {
		completion, err := pplxFoldCompletion(ctx, req, resp.Body)
		if err != nil {
			return err
		}
		if err := writePplxJSON(w, req, completion); err != nil {
			return err
		}
	}

	return nil
}

// pplxEndpointOf resolves the endpoint the turn is sent to: the registry
// default, or the base URL an operator configured on the connection.
func pplxEndpointOf(req *Request) string {
	if req.Config != nil {
		if base := strings.TrimSpace(req.Config.BaseURL); base != "" {
			return base
		}
	}
	return pplxWebEndpoint
}

// pplxThinkingRequested reports whether the client asked for reasoning, reading
// the two fields the Chat Completions contract offers for it.
func pplxThinkingRequested(inbound pplxInboundRequest) bool {
	if inbound.Thinking != nil {
		return *inbound.Thinking
	}
	effort := strings.ToLower(strings.TrimSpace(inbound.ReasoningEffort))
	return effort != "" && effort != "none"
}

// pplxStatusError maps a refused upstream response onto the status the router
// acts on, naming the cause the operator can fix.
func pplxStatusError(resp *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	message := fmt.Sprintf("perplexity-web: Perplexity returned HTTP %d", resp.StatusCode)
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		message = "perplexity-web: Perplexity auth failed — the session cookie may be expired. Re-paste your __Secure-next-auth.session-token."
	case http.StatusTooManyRequests:
		message = "perplexity-web: Perplexity rate limited. Wait a moment and retry."
	}

	log.Warn("perplexity-web", "upstream refused", "status", resp.StatusCode, "message", message, "body", string(body))
	return &proxy.UpstreamError{StatusCode: resp.StatusCode, Body: body, Header: resp.Header, ReadErr: readErr}
}

// pplxFoldCompletion consumes a whole event stream and folds it into one
// chat.completion.
func pplxFoldCompletion(ctx context.Context, req *Request, upstream io.Reader) (*pplxCompletion, error) {
	acc := newPplxAccumulator(req)
	if err := scanPplxEvents(upstream, acc.apply); err != nil {
		return nil, fmt.Errorf("perplexity-web: read stream: %w", err)
	}
	if acc.failed() {
		return nil, acc.failure()
	}
	if !acc.answered() {
		return nil, proxy.UpstreamFailure(http.StatusBadGateway, proxy.NoCompletionInStream)
	}
	return acc.completion(), nil
}

// writePplxStream emits the site's events as OpenAI chunks as they arrive.
func writePplxStream(ctx context.Context, w http.ResponseWriter, req *Request, upstream io.Reader) error {
	acc := newPplxAccumulator(req)
	acc.emitter = newPplxEmitter(w, req, acc)
	defer acc.finish()

	if err := scanPplxEventsWithContext(ctx, upstream, acc.applyStream); err != nil {
		acc.abort(err)
		return err
	}
	if acc.failed() {
		err := acc.failure()
		acc.abort(err)
		return err
	}
	if !acc.answered() {
		err := proxy.UpstreamFailure(http.StatusBadGateway, proxy.NoCompletionInStream)
		acc.abort(err)
		return err
	}
	return acc.emitter.close()
}

// writePplxJSON writes one folded completion.
func writePplxJSON(w http.ResponseWriter, req *Request, completion *pplxCompletion) error {
	raw, err := json.Marshal(completion)
	if err != nil {
		return proxy.UpstreamFailure(http.StatusInternalServerError, "perplexity-web: encode response: "+err.Error())
	}
	if req.ResponseBuf != nil {
		req.ResponseBuf.Write(raw)
	}
	return jsonResponse(req.Ctx, w, strings.NewReader(string(raw)), req.TranslateResp, nil)
}

// pplxUsage records token accounting.
func pplxUsage(ctx context.Context, prompt, answer, thinking string) map[string]any {
	promptTokens := len([]rune(prompt)) / 4
	completionTokens := len([]rune(answer+thinking)) / 4
	if promptTokens == 0 && prompt != "" {
		promptTokens = 1
	}
	usage := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      promptTokens + completionTokens,
	}
	if ctx != nil {
		translator.SetUsage(ctx, &translator.OpenAIUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
		})
	}
	return usage
}

// pplxNow is the clock the response metadata reads.
var pplxNow = time.Now
