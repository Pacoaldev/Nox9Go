package executor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"

	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/proxy"
	"nox9go/proxy/internal/translator"
)

// pplxEvent is the event shape the site emits over text/event-stream.
type pplxEvent struct {
	BackendUUID  string      `json:"backend_uuid"`
	ErrorCode    string      `json:"error_code"`
	ErrorMessage string      `json:"error_message"`
	Status       string      `json:"status"`
	Final        bool        `json:"final"`
	Text         string      `json:"text"`
	Blocks       []pplxBlock `json:"blocks"`
}

type pplxBlock struct {
	IntendedUsage string             `json:"intended_usage"`
	MarkdownBlock *pplxMarkdownBlock `json:"markdown_block"`
	PlanBlock     *pplxPlanBlock     `json:"plan_block"`
}

type pplxMarkdownBlock struct {
	Chunks   []string `json:"chunks"`
	Progress string   `json:"progress"`
}

type pplxPlanBlock struct {
	Steps []pplxPlanStep `json:"steps"`
	Goals []pplxPlanGoal `json:"goals"`
}

type pplxPlanStep struct {
	StepType           string                  `json:"step_type"`
	SearchWebContent   *pplxSearchWebContent   `json:"search_web_content"`
	ReadResultsContent *pplxReadResultsContent `json:"read_results_content"`
}

type pplxSearchWebContent struct {
	Queries []struct {
		Query string `json:"query"`
	} `json:"queries"`
}

type pplxReadResultsContent struct {
	URLs []string `json:"urls"`
}

type pplxPlanGoal struct {
	Description string `json:"description"`
}

func scanPplxEvents(r io.Reader, onEvent func(ev *pplxEvent) error) error {
	return scanPplxEventsWithContext(context.Background(), r, onEvent)
}

func scanPplxEventsWithContext(ctx context.Context, r io.Reader, onEvent func(ev *pplxEvent) error) error {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = nil
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var ev pplxEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil
		}
		return onEvent(&ev)
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if line == "event: end_of_stream" {
			break
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return scanner.Err()
}

type pplxAccumulator struct {
	req          *Request
	inbound      pplxInboundRequest
	parsed       pplxParsedMessages
	fullAnswer   string
	seenLen      int
	backendUUID  string
	thinking     []string
	seenThinking map[string]bool
	errCode      string
	errMsg       string
	emitter      *pplxEmitter
}

func newPplxAccumulator(req *Request) *pplxAccumulator {
	var inbound pplxInboundRequest
	_ = json.Unmarshal(req.Body, &inbound)
	parsed := parsePplxMessages(inbound.Messages)
	return &pplxAccumulator{
		req:          req,
		inbound:      inbound,
		parsed:       parsed,
		seenThinking: make(map[string]bool),
	}
}

func (a *pplxAccumulator) apply(ev *pplxEvent) error {
	if ev.ErrorCode != "" || ev.ErrorMessage != "" {
		a.errCode = ev.ErrorCode
		a.errMsg = ev.ErrorMessage
		return nil
	}
	if ev.BackendUUID != "" {
		a.backendUUID = ev.BackendUUID
	}
	for _, block := range ev.Blocks {
		usage := block.IntendedUsage
		if usage == "pro_search_steps" && block.PlanBlock != nil {
			for _, step := range block.PlanBlock.Steps {
				if step.StepType == "SEARCH_WEB" && step.SearchWebContent != nil {
					for _, q := range step.SearchWebContent.Queries {
						if q.Query != "" && !a.seenThinking[q.Query] {
							a.seenThinking[q.Query] = true
							a.thinking = append(a.thinking, "Searching: "+q.Query)
						}
					}
				} else if step.StepType == "READ_RESULTS" && step.ReadResultsContent != nil {
					limit := 3
					if len(step.ReadResultsContent.URLs) < limit {
						limit = len(step.ReadResultsContent.URLs)
					}
					for _, u := range step.ReadResultsContent.URLs[:limit] {
						if u != "" && !a.seenThinking[u] {
							a.seenThinking[u] = true
							a.thinking = append(a.thinking, "Reading: "+u)
						}
					}
				}
			}
		}
		if usage == "plan" && block.PlanBlock != nil {
			for _, goal := range block.PlanBlock.Goals {
				desc := strings.TrimSpace(goal.Description)
				if desc != "" && !a.seenThinking[desc] {
					a.seenThinking[desc] = true
					a.thinking = append(a.thinking, desc)
				}
			}
		}
		if !strings.Contains(usage, "markdown") || block.MarkdownBlock == nil {
			continue
		}
		chunks := block.MarkdownBlock.Chunks
		if len(chunks) == 0 {
			continue
		}
		if block.MarkdownBlock.Progress == "DONE" {
			a.fullAnswer = strings.Join(chunks, "")
		} else {
			joined := strings.Join(chunks, "")
			cumulative := a.fullAnswer + joined
			if len(cumulative) > a.seenLen {
				a.fullAnswer = cumulative
				a.seenLen = len(cumulative)
			}
		}
	}
	if len(ev.Blocks) == 0 && ev.Text != "" {
		t := strings.TrimSpace(ev.Text)
		if len(t) > a.seenLen {
			a.fullAnswer = t
			a.seenLen = len(t)
		}
	}
	return nil
}

func (a *pplxAccumulator) applyStream(ev *pplxEvent) error {
	prevThinkLen := len(a.thinking)
	prevAnswerLen := len(a.fullAnswer)
	if err := a.apply(ev); err != nil {
		return err
	}
	if a.emitter == nil {
		return nil
	}
	for i := prevThinkLen; i < len(a.thinking); i++ {
		step := a.thinking[i] + "\n"
		if err := a.emitter.emitThinkingDelta(step); err != nil {
			return err
		}
	}
	if len(a.fullAnswer) > prevAnswerLen {
		delta := a.fullAnswer[prevAnswerLen:]
		deltaClean := cleanPplxResponse(delta, false)
		if deltaClean != "" {
			if err := a.emitter.emitContentDelta(deltaClean); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *pplxAccumulator) failed() bool { return a.errCode != "" || a.errMsg != "" }
func (a *pplxAccumulator) failure() error {
	msg := a.errMsg
	if msg == "" {
		msg = "Perplexity error: " + a.errCode
	}
	return proxy.UpstreamFailure(http.StatusBadGateway, msg)
}
func (a *pplxAccumulator) answered() bool { return strings.TrimSpace(a.fullAnswer) != "" || len(a.thinking) > 0 }
func (a *pplxAccumulator) finish() {
	cleaned := cleanPplxResponse(a.fullAnswer, true)
	globalPplxSessions.Store(a.parsed.history, a.parsed.currentMsg, cleaned, a.backendUUID)
}
func (a *pplxAccumulator) abort(err error) {
	if a.emitter != nil {
		a.emitter.abort(err)
	}
}

type pplxCompletion struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []pplxChoiceJSON `json:"choices"`
	Usage   map[string]any   `json:"usage"`
}
type pplxChoiceJSON struct {
	Index        int                `json:"index"`
	Message      pplxMessagePayload `json:"message"`
	FinishReason string             `json:"finish_reason"`
}
type pplxMessagePayload struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

func (a *pplxAccumulator) completion() *pplxCompletion {
	cleaned := cleanPplxResponse(a.fullAnswer, true)
	var reasoning string
	if len(a.thinking) > 0 {
		reasoning = strings.Join(a.thinking, "\n")
	}
	created := pplxNow().Unix()
	cid := fmt.Sprintf("chatcmpl-pplx-%d", created)
	model := a.inbound.Model
	if model == "" {
		model = "pplx-auto"
	}
	usage := pplxUsage(a.req.Ctx, a.parsed.currentMsg, cleaned, reasoning)
	a.finish()
	return &pplxCompletion{
		ID:      cid,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []pplxChoiceJSON{{
			Index: 0,
			Message: pplxMessagePayload{
				Role:             "assistant",
				Content:          cleaned,
				ReasoningContent: reasoning,
			},
			FinishReason: "stop",
		}},
		Usage: usage,
	}
}

type pplxEmitter struct {
	w          http.ResponseWriter
	req        *Request
	hw         *proxy.HeartbeatWriter
	flusher    http.Flusher
	id         string
	created    int64
	model      string
	acc        *pplxAccumulator
	roleSent   bool
	opened     bool
	claude     bool
	sessionKey string
	mu         sync.Mutex
}

func newPplxEmitter(w http.ResponseWriter, req *Request, acc *pplxAccumulator) *pplxEmitter {
	created := pplxNow().Unix()
	id := fmt.Sprintf("chatcmpl-pplx-%d", created)
	model := acc.inbound.Model
	if model == "" {
		model = "pplx-auto"
	}
	emitter := &pplxEmitter{
		w:       w,
		req:     req,
		hw:      proxy.NewHeartbeatWriter(reqCtx(req), w, 0),
		id:      id,
		created: created,
		model:   model,
		acc:     acc,
		claude:  req.ClaudeClient || req.TranslateResp,
	}
	if emitter.claude {
		emitter.sessionKey = translator.NewSessionKey("pplx-stream")
	}
	return emitter
}

func (e *pplxEmitter) open() {
	if e.opened {
		return
	}
	e.opened = true
	e.flusher = proxy.WriteSSEHeaders(e.hw)
}

func (e *pplxEmitter) emitContentDelta(delta string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.open()
	e.markTTFT()
	if e.req.ResponseBuf != nil {
		_, _ = e.req.ResponseBuf.Write([]byte(delta))
	}
	payload := map[string]any{"content": delta}
	if !e.roleSent {
		payload["role"] = "assistant"
		e.roleSent = true
	}
	return e.writeChunk(payload, nil, nil)
}

func (e *pplxEmitter) emitThinkingDelta(thinking string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.open()
	e.markTTFT()
	payload := map[string]any{"reasoning_content": thinking}
	if !e.roleSent {
		payload["role"] = "assistant"
		e.roleSent = true
	}
	return e.writeChunk(payload, nil, nil)
}

func (e *pplxEmitter) markTTFT() {
	if e.req.TTFT == nil || *e.req.TTFT != 0 {
		return
	}
	start := e.req.StartTime
	if start.IsZero() {
		start = pplxNow()
	}
	*e.req.TTFT = time.Since(start).Milliseconds()
}

func (e *pplxEmitter) writeChunk(delta map[string]any, finishReason any, usage map[string]any) error {
	choice := map[string]any{
		"index":         0,
		"delta":         delta,
		"finish_reason": finishReason,
	}
	chunk := map[string]any{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []map[string]any{choice},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("perplexity-web: marshal chunk: %w", err)
	}
	sseLine := []byte(fmt.Sprintf("data: %s\n\n", string(raw)))
	if e.claude {
		translated, terr := translator.TranslateOpenAIToClaudeStreamSession(e.sessionKey, sseLine)
		if terr != nil {
			log.Warn("perplexity-web", "claude translate chunk", "error", terr)
			return nil
		}
		if translated == nil {
			return nil
		}
		if _, werr := e.hw.Write(translated); werr != nil {
			return werr
		}
		if e.flusher != nil {
			e.flusher.Flush()
		}
		return nil
	}
	if _, werr := e.hw.Write(sseLine); werr != nil {
		return werr
	}
	if e.flusher != nil {
		e.flusher.Flush()
	}
	return nil
}

func (e *pplxEmitter) close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.open()
	cleaned := cleanPplxResponse(e.acc.fullAnswer, true)
	var reasoning string
	if len(e.acc.thinking) > 0 {
		reasoning = strings.Join(e.acc.thinking, "\n")
	}
	usage := pplxUsage(e.req.Ctx, e.acc.parsed.currentMsg, cleaned, reasoning)
	if err := e.writeChunk(map[string]any{}, "stop", usage); err != nil {
		return err
	}
	if e.claude {
		done := []byte("data: [DONE]\n\n")
		if translated, terr := translator.TranslateOpenAIToClaudeStreamSession(e.sessionKey, done); terr == nil && translated != nil {
			_, _ = e.hw.Write(translated)
		}
		translator.ClearStreamState(e.sessionKey)
		return e.hw.Close()
	}
	if _, err := e.hw.Write([]byte("data: [DONE]\n\n")); err != nil {
		return err
	}
	if e.flusher != nil {
		e.flusher.Flush()
	}
	return e.hw.Close()
}

func (e *pplxEmitter) abort(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.opened {
		format := proxy.SSEFormatOpenAI
		if e.claude {
			format = proxy.SSEFormatClaude
		}
		errBytes := proxy.BuildStreamErrorBytes(http.StatusBadGateway, err.Error(), format)
		_, _ = e.hw.Write(errBytes)
		if e.flusher != nil {
			e.flusher.Flush()
		}
	}
	_ = e.hw.Close()
}
