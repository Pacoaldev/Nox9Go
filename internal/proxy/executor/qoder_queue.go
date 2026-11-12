package executor

import (
	"bufio"
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"nox9go/proxy/internal/proxy"
)

// Qoder model-queue handling — port of open-sse/shared/qoder/queue.js.
//
// When a model is temporarily out of capacity Qoder answers the inference
// request with a `{statusCodeValue, body}` SSE envelope whose body carries:
//
//	{ "code": "10605",
//	  "message": "{\"isQueued\":true,\"modelKey\":\"auto\",\"queueCount\":0,
//	               \"queueType\":\"p3\",\"retryAfterSeconds\":30,
//	               \"serviceAvailable\":false,\"subCode\":\"RESOURCE_LIMIT\",
//	               \"waitTime\":30}",
//	  "subCode": "RESOURCE_LIMIT" }
//
// That is a transient capacity signal, not a quota block: the model becomes
// available again in ~30s and the same request then succeeds. qodercli keeps
// the request open, polls the model-queue status endpoint, and replays the
// inference once the model is ready. The payload is frequently double- or
// triple-encoded, so every lookup walks the nested tree and parses JSON-encoded
// string fields as it goes.

// qoderQueueCode is the model-queue throttle code. Deliberately kept out of
// qoderIsBillingBlock: it is a capacity signal, not a billing block.
const qoderQueueCode = "10605"

// qoderQueueMaxWaitEnv raises the total budget a queued request may hold open.
const qoderQueueMaxWaitEnv = "QODER_MODEL_QUEUE_MAX_WAIT_MS"

// Queue timing. Declared as vars (not consts) so a test can shrink them instead
// of waiting out production-scale backoff. Defaults mirror upstream queue.js.
var (
	qoderQueueDefaultRetry   = 30 * time.Second
	qoderQueueMinRetry       = 15 * time.Second
	qoderQueueMaxRetry       = 60 * time.Second
	qoderQueueStatusFloor    = 30 * time.Second
	qoderQueueDefaultMaxWait = 10 * time.Minute
)

// qoderQueueNestedKeys are the fields upstream walks when the queue object is
// wrapped in one or more JSON strings.
var qoderQueueNestedKeys = []string{"error", "data", "body", "message", "cause", "result", "payload"}

// qoderQueueInfo is a normalized queue descriptor parsed from a qoder error
// body. Pointer fields distinguish "absent" from "zero".
type qoderQueueInfo struct {
	Code              string
	SubCode           string
	IsQueued          *bool
	ModelKey          string
	QueueType         string
	QueueCount        *int
	ServiceAvailable  *bool
	RetryAfterSeconds *int
	WaitTime          *int
}

// qoderQueueStatus is the parsed model-queue status endpoint response.
type qoderQueueStatus struct {
	IsQueued          bool
	ModelKey          string
	QueueType         string
	QueueCount        *int
	RetryAfterSeconds *int
	WaitTime          *int
}

// qoderQueueMaxWait is the total budget for holding a request open while its
// model is queued.
func qoderQueueMaxWait() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(qoderQueueMaxWaitEnv)); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return qoderQueueDefaultMaxWait
}

// qoderParseQueueInfo returns a queue descriptor when the body carries a queue
// signal, or nil when it does not. A plain `code: "10605"` with no inner object
// still counts: the code alone is the signal.
func qoderParseQueueInfo(input string) *qoderQueueInfo {
	root := qoderTryParseJSON(input)
	if root == nil {
		return nil
	}
	info := &qoderQueueInfo{}
	for _, obj := range qoderWalkObjects(root) {
		if info.Code == "" {
			info.Code = qoderStr(obj["code"])
		}
		if info.SubCode == "" {
			info.SubCode = qoderStr(obj["subCode"])
		}
		if info.IsQueued == nil {
			info.IsQueued = qoderBool(obj["isQueued"])
		}
		if info.ModelKey == "" {
			info.ModelKey = qoderStr(obj["modelKey"])
		}
		if info.QueueType == "" {
			info.QueueType = qoderStr(obj["queueType"])
		}
		if info.QueueCount == nil {
			info.QueueCount = qoderInt(obj["queueCount"])
		}
		if info.ServiceAvailable == nil {
			info.ServiceAvailable = qoderBool(obj["serviceAvailable"])
		}
		if info.RetryAfterSeconds == nil {
			info.RetryAfterSeconds = qoderInt(obj["retryAfterSeconds"])
		}
		if info.WaitTime == nil {
			info.WaitTime = qoderInt(obj["waitTime"])
		}
	}

	queued := (info.IsQueued != nil && *info.IsQueued) ||
		(info.ServiceAvailable != nil && !*info.ServiceAvailable) ||
		info.SubCode == "RESOURCE_LIMIT" ||
		info.Code == qoderQueueCode
	if !queued {
		return nil
	}
	return info
}

// qoderParseQueueStatus parses the model-queue status endpoint body. Returns nil
// when the shape is not what we expect, which the caller treats as "status
// endpoint unusable" and falls back to timed replays.
func qoderParseQueueStatus(input string) *qoderQueueStatus {
	root := qoderTryParseJSON(input)
	if root == nil {
		return nil
	}
	m, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	isQueued := qoderBool(m["isQueued"])
	if isQueued == nil {
		return nil
	}
	return &qoderQueueStatus{
		IsQueued:          *isQueued,
		ModelKey:          qoderStr(m["modelKey"]),
		QueueType:         qoderStr(m["queueType"]),
		QueueCount:        qoderInt(m["queueCount"]),
		RetryAfterSeconds: qoderInt(m["retryAfterSeconds"]),
		WaitTime:          qoderInt(m["waitTime"]),
	}
}

// qoderQueueDelayMs is the delay before the next attempt. The inference refusal
// carries waitTime/retryAfterSeconds for the queue the request actually sits in,
// so its hint is honoured; a status-derived delay never drops below the status
// floor, because the status endpoint has been observed answering isQueued:false
// while the inference call is still refused. Both are clamped into
// [min, max].
func qoderQueueDelayMs(retryAfter, waitTime *int, fromStatus bool) time.Duration {
	delay := qoderQueueDefaultRetry
	if hint := qoderFirstInt(retryAfter, waitTime); hint != nil {
		delay = time.Duration(*hint) * time.Second
	}
	if fromStatus && delay < qoderQueueStatusFloor {
		delay = qoderQueueStatusFloor
	}
	if delay < qoderQueueMinRetry {
		delay = qoderQueueMinRetry
	}
	if delay > qoderQueueMaxRetry {
		delay = qoderQueueMaxRetry
	}
	return delay
}

// qoderQueueStatusURL builds the queue-status endpoint qodercli polls. Both
// requestSetId and modelKey are required — omitting either answers HTTP 400.
func qoderQueueStatusURL(base, requestSetID, modelKey, queueType string) string {
	base = strings.TrimRight(base, "/")
	q := url.Values{}
	if requestSetID != "" {
		q.Set("requestSetId", requestSetID)
	}
	if modelKey != "" {
		q.Set("modelKey", modelKey)
	}
	if queueType != "" {
		q.Set("queueType", queueType)
	}
	return base + "/algo/api/v2/service/ask/queue/status?" + q.Encode()
}

// qoderPeek is the result of reading the head of a Qoder SSE body.
type qoderPeek struct {
	// errorFrame is true when the first data frame is a non-200 envelope.
	errorFrame bool
	status     int
	inner      string
	// consumed is every byte read while peeking, so the caller can re-emit it.
	consumed []byte
}

// qoderPeekLimit caps how much of the head is buffered while peeking.
const qoderPeekLimit = 4 << 20

// qoderPeekFirstFrame reads the first `data:` frame of a Qoder SSE body without
// consuming the rest. Bytes already read are returned in consumed; the reader is
// left positioned right after them.
func qoderPeekFirstFrame(br *bufio.Reader) qoderPeek {
	var consumed []byte
	for {
		line, err := br.ReadBytes('\n')
		consumed = append(consumed, line...)
		if len(consumed) > qoderPeekLimit {
			return qoderPeek{consumed: consumed}
		}
		if err != nil {
			return qoderPeek{consumed: consumed}
		}
		trimmed := strings.TrimSpace(string(line))
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if data == "" || data == "[DONE]" {
			return qoderPeek{consumed: consumed}
		}
		var envelope qoderEnvelope
		if json.Unmarshal([]byte(data), &envelope) != nil {
			return qoderPeek{consumed: consumed}
		}
		status := envelope.StatusCodeValue
		if status == 0 {
			status = 200
		}
		if status != 200 {
			return qoderPeek{
				errorFrame: true,
				status:     status,
				inner:      qoderEnvelopeBody(envelope.Body),
				consumed:   consumed,
			}
		}
		return qoderPeek{consumed: consumed}
	}
}

// qoderRewoundBody re-emits a peeked head before the rest of the upstream body,
// and closes the upstream when the consumer is done.
type qoderRewoundBody struct {
	io.Reader
	closer io.Closer
}

func (b qoderRewoundBody) Close() error { return b.closer.Close() }

// qoderSleepCtx waits for d, or returns early when ctx ends. A plain time.Sleep
// would hold the request open past a client disconnect.
func qoderSleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// qoderFetchQueueStatus polls the model-queue status endpoint. ok=false means
// "fall back to timed replays" (endpoint missing, erroring, or unknown shape).
func qoderFetchQueueStatus(ctx context.Context, client *http.Client, base string, creds QoderCosyCreds, requestSetID, modelKey, queueType, requestID, sessionID string) (*qoderQueueStatus, bool) {
	statusURL := qoderQueueStatusURL(base, requestSetID, modelKey, queueType)
	headers, err := buildQoderCosyHeaders([]byte{}, statusURL, creds)
	if err != nil {
		return nil, false
	}
	headers["Accept"] = "application/json"
	headers["Accept-Encoding"] = "identity"
	// qodercli correlates the poll with the original inference.
	headers["X-Request-Id"] = requestID
	headers["X-Session-Id"] = sessionID

	resp, err := proxy.DoRequest(ctx, client, http.MethodGet, statusURL, headers, nil)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if readErr != nil {
		return nil, false
	}
	info := qoderParseQueueStatus(string(body))
	if info == nil {
		return nil, false
	}
	return info, true
}

// ---- nested JSON helpers ----

// qoderTryParseJSON parses a JSON object or array, or returns nil.
func qoderTryParseJSON(s string) any {
	t := strings.TrimSpace(s)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(t), &v); err != nil {
		return nil
	}
	switch v.(type) {
	case map[string]any, []any:
		return v
	}
	return nil
}

// qoderWalkObjects returns every plain object reachable from root, following the
// nested keys and re-parsing JSON-encoded string fields as it goes. The 2000
// object cap bounds work on a hostile payload; decoded JSON cannot contain a
// reference cycle, so no visited set is needed.
func qoderWalkObjects(root any) []map[string]any {
	var out []map[string]any
	queue := []any{root}
	for len(queue) > 0 && len(out) < 2000 {
		node := queue[0]
		queue = queue[1:]
		switch n := node.(type) {
		case map[string]any:
			out = append(out, n)
			for _, key := range qoderQueueNestedKeys {
				switch v := n[key].(type) {
				case map[string]any, []any:
					queue = append(queue, v)
				case string:
					if parsed := qoderTryParseJSON(v); parsed != nil {
						queue = append(queue, parsed)
					}
				}
			}
		case []any:
			queue = append(queue, n...)
		}
	}
	return out
}

func qoderStr(v any) string {
	if s, ok := v.(string); ok {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}

func qoderBool(v any) *bool {
	switch b := v.(type) {
	case bool:
		return &b
	case string:
		switch b {
		case "true":
			t := true
			return &t
		case "false":
			f := false
			return &f
		}
	}
	return nil
}

func qoderInt(v any) *int {
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return &i
		}
	}
	return nil
}

// qoderFirstInt returns the first non-nil value.
func qoderFirstInt(values ...*int) *int {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// qoderPayloadString reads a string field off the built Qoder payload.
func qoderPayloadString(payload map[string]any, key string) string {
	s, _ := payload[key].(string)
	return s
}

// qoderNewPeekReader wraps an upstream body so its head can be read without
// consuming the rest: the returned reader continues where the peek stopped.
func qoderNewPeekReader(body io.Reader) *bufio.Reader {
	return bufio.NewReaderSize(body, 64*1024)
}

// qoderSendWithQueueWait sends the signed inference request, holding it open
// while Qoder reports the model is queued. A queue signal (10605 / isQueued /
// RESOURCE_LIMIT) is transient: the request is replayed once the queue drains,
// bounded by qoderQueueMaxWait. It returns the response body with the peeked
// head already unwound, or an UpstreamError for a genuine failure.
//
// Every replay rebuilds the COSY signature (the signature covers the body and
// carries a per-request id), but reuses the same payload, so the queue entry the
// status endpoint is asked about belongs to this request.
func qoderSendWithQueueWait(ctx context.Context, req *Request, creds QoderCosyCreds, region QoderRegion, built qoderBuiltChat, payload []byte, targetURL string) (io.ReadCloser, error) {
	maxWait := qoderQueueMaxWait()
	start := time.Now()

	statusBase := qoderEndpoint(req)
	if statusBase == "" {
		statusBase = "https://" + qoderInferenceHost(region, creds.AuthToken)
	}
	requestSetID := qoderPayloadString(built.Payload, "request_set_id")
	requestID := qoderPayloadString(built.Payload, "request_id")
	sessionID := qoderPayloadString(built.Payload, "session_id")

	statusPollsEnabled := true
	readyReplayUsed := false

	for {
		headers, err := buildQoderCosyHeaders(payload, targetURL, creds)
		if err != nil {
			return nil, fmt.Errorf("build Qoder COSY headers: %w", err)
		}
		qoderApplyChatHeaders(headers, built)

		resp, err := proxy.DoRequest(ctx, req.Client, http.MethodPost, targetURL, headers, payload)
		if err != nil {
			return nil, fmt.Errorf("ForwardQoder: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			return nil, &proxy.UpstreamError{StatusCode: resp.StatusCode, Body: errBody, Header: resp.Header}
		}

		br := qoderNewPeekReader(resp.Body)
		peek := qoderPeekFirstFrame(br)

		if !peek.errorFrame {
			// Clean (or empty) head: re-emit what was peeked, then continue.
			return qoderRewoundBody{Reader: io.MultiReader(bytes.NewReader(peek.consumed), br), closer: resp.Body}, nil
		}

		info := qoderParseQueueInfo(peek.inner)
		if info == nil {
			// Not a queue signal: a genuine failure (billing block or upstream
			// error) the chat fallback layer acts on.
			resp.Body.Close()
			if qoderIsBillingBlock(peek.inner) {
				return nil, &proxy.UpstreamError{
					StatusCode: http.StatusForbidden,
					Body:       qoderErrorBody(orDefault(peek.inner, "qoder billing block"), http.StatusForbidden),
				}
			}
			status := peek.status
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			return nil, &proxy.UpstreamError{StatusCode: status, Body: qoderErrorBody(peek.inner, status)}
		}

		// Queue signal: the model is temporarily out of capacity.
		resp.Body.Close()
		waited := time.Since(start)
		if waited >= maxWait {
			return nil, &proxy.UpstreamError{
				StatusCode: http.StatusServiceUnavailable,
				Body: qoderErrorBody(
					fmt.Sprintf("qoder model %q is still queued after %ds; retry shortly", built.QoderKey, int(waited.Seconds())),
					http.StatusServiceUnavailable),
			}
		}

		delay := qoderQueueDelayMs(info.RetryAfterSeconds, info.WaitTime, false)
		if statusPollsEnabled {
			modelKey := info.ModelKey
			if modelKey == "" {
				modelKey = built.QoderKey
			}
			st, ok := qoderFetchQueueStatus(ctx, req.Client, statusBase, creds, requestSetID, modelKey, info.QueueType, requestID, sessionID)
			switch {
			case !ok:
				// No usable status endpoint — keep timed replays.
				statusPollsEnabled = false
			case !st.IsQueued:
				// The endpoint says ready, but it has been observed saying that
				// while the inference is still refused. Replay once, then stop
				// trusting it and fall back to timed replays.
				if readyReplayUsed {
					statusPollsEnabled = false
				} else {
					readyReplayUsed = true
					delay = 0
				}
			default:
				delay = qoderQueueDelayMs(st.RetryAfterSeconds, st.WaitTime, true)
			}
		}

		if err := qoderSleepCtx(ctx, delay); err != nil {
			return nil, fmt.Errorf("qoder queue wait: %w", err)
		}
	}
}
