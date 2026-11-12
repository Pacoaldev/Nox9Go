package executor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nox9go/proxy/internal/proxy"
)

// qoderFastQueueRetries shrinks the queue backoff to zero so a test does not
// wait out production-scale delays. Restored on cleanup.
func qoderFastQueueRetries(t *testing.T) {
	t.Helper()
	d, mn, mx, floor := qoderQueueDefaultRetry, qoderQueueMinRetry, qoderQueueMaxRetry, qoderQueueStatusFloor
	qoderQueueDefaultRetry, qoderQueueMinRetry, qoderQueueMaxRetry, qoderQueueStatusFloor = 0, 0, 0, 0
	t.Cleanup(func() {
		qoderQueueDefaultRetry, qoderQueueMinRetry, qoderQueueMaxRetry, qoderQueueStatusFloor = d, mn, mx, floor
	})
}

func qoderQueueInner() string {
	return `{"code":"10605","message":"{\"isQueued\":true,\"modelKey\":\"model\",\"queueType\":\"p3\",\"retryAfterSeconds\":30,\"serviceAvailable\":false,\"subCode\":\"RESOURCE_LIMIT\",\"waitTime\":30}"}`
}

func TestQoderParseQueueInfo(t *testing.T) {
	tests := []struct {
		name  string
		inner string
		want  bool
	}{
		{"queued flag", `{"isQueued":true,"modelKey":"auto","queueType":"p3","waitTime":30}`, true},
		{"service unavailable", `{"serviceAvailable":false,"modelKey":"auto"}`, true},
		{"resource limit subcode", `{"subCode":"RESOURCE_LIMIT"}`, true},
		{"plain queue code", `{"code":"10605"}`, true},
		{"double encoded", `{"code":"10605","message":"{\"isQueued\":true,\"queueType\":\"p3\"}"}`, true},
		{"qoder 504 queue timeout is not a queue signal", `{"code":"504","message":"Queue wait timed out"}`, false},
		{"ordinary model error", `{"code":400,"message":"flow nodes not found"}`, false},
		{"empty", ``, false},
		{"not json", `upstream status 500`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := qoderParseQueueInfo(tt.inner)
			if (got != nil) != tt.want {
				t.Fatalf("qoderParseQueueInfo(%q) = %v, want present=%v", tt.inner, got, tt.want)
			}
		})
	}
}

func TestQoderParseQueueStatus(t *testing.T) {
	if got := qoderParseQueueStatus(`{"isQueued":false,"modelKey":"auto","queueType":"p3"}`); got == nil || got.IsQueued {
		t.Errorf("ready status = %v, want IsQueued=false", got)
	}
	if got := qoderParseQueueStatus(`{"isQueued":true,"queueCount":3,"retryAfterSeconds":2}`); got == nil || !got.IsQueued {
		t.Errorf("queued status = %v, want IsQueued=true", got)
	}
	if got := qoderParseQueueStatus(`{"unexpected":"shape"}`); got != nil {
		t.Errorf("unknown shape = %v, want nil", got)
	}
}

func TestQoderQueueDelayClamps(t *testing.T) {
	qoderFastQueueRetries(t)
	min, max := 15*time.Second, 60*time.Second
	qoderQueueMinRetry, qoderQueueMaxRetry = min, max

	// A hint below the floor clamps up; a hint above the ceiling clamps down.
	low, high := 1, 600
	if got := qoderQueueDelayMs(&low, nil, false); got != min {
		t.Errorf("low hint = %v, want %v", got, min)
	}
	if got := qoderQueueDelayMs(&high, nil, false); got != max {
		t.Errorf("high hint = %v, want %v", got, max)
	}
}

// A queued model must not fail the request: the send waits, polls the status
// endpoint, and replays once the model is ready.
func TestForwardQoder_QueueWaitReplaysThenSucceeds(t *testing.T) {
	qoderFastQueueRetries(t)
	var posts, statuses int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/queue/status") {
			atomic.AddInt32(&statuses, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isQueued":false,"modelKey":"model","queueType":"p3"}`))
			return
		}
		n := atomic.AddInt32(&posts, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			_, _ = w.Write([]byte(qoderEnvelopeError(500, qoderQueueInner())))
			return
		}
		_, _ = w.Write([]byte(qoderSSE(
			qoderChunk(`{"content":"served"}`, `null`),
			qoderChunk(`{}`, `"stop"`),
			"[DONE]",
		)))
	}))
	t.Cleanup(srv.Close)

	rec := httptest.NewRecorder()
	if err := ForwardQoder(rec, qoderRequest(t, srv)); err != nil {
		t.Fatalf("ForwardQoder: %v", err)
	}
	if got := atomic.LoadInt32(&posts); got != 2 {
		t.Errorf("inference attempts = %d, want 2 (one queued, one served)", got)
	}
	if got := atomic.LoadInt32(&statuses); got == 0 {
		t.Errorf("status endpoint was never polled")
	}
	if !strings.Contains(rec.Body.String(), "served") {
		t.Errorf("body = %s, want the served completion", rec.Body.String())
	}
}

// Once the queue budget is spent the request is released as a retryable 503
// instead of holding the client indefinitely.
func TestForwardQoder_QueueBudgetExhaustedReturns503(t *testing.T) {
	qoderFastQueueRetries(t)
	oldMax := qoderQueueDefaultMaxWait
	qoderQueueDefaultMaxWait = time.Nanosecond
	t.Cleanup(func() { qoderQueueDefaultMaxWait = oldMax })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(qoderEnvelopeError(500, qoderQueueInner())))
	}))
	t.Cleanup(srv.Close)

	rec := httptest.NewRecorder()
	err := ForwardQoder(rec, qoderRequest(t, srv))
	var upErr *proxy.UpstreamError
	if !errors.As(err, &upErr) {
		t.Fatalf("error = %v, want a *proxy.UpstreamError", err)
	}
	if upErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", upErr.StatusCode)
	}
	if !strings.Contains(string(upErr.Body), "still queued") {
		t.Errorf("body = %s, want a 'still queued' message", upErr.Body)
	}
}

// A first-frame billing block is a real quota refusal: it must surface as a 403
// so the fallback layer locks the model, not be mistaken for the queue throttle.
func TestForwardQoder_FirstFrameBillingBlockIsForbidden(t *testing.T) {
	inner := `{"code":110,"message":"billing daily count exceeded"}`
	srv := qoderTestServer(t, qoderEnvelopeError(403, inner))

	rec := httptest.NewRecorder()
	err := ForwardQoder(rec, qoderRequest(t, srv))
	var upErr *proxy.UpstreamError
	if !errors.As(err, &upErr) {
		t.Fatalf("error = %v, want a *proxy.UpstreamError", err)
	}
	if upErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", upErr.StatusCode)
	}
}

// A queue signal on a streaming request must not leak as a 200 SSE body: the
// request is held (and eventually released with a retryable error), exactly like
// the non-streaming path.
func TestForwardQoder_StreamQueueSignalIsHeldNotForwarded(t *testing.T) {
	qoderFastQueueRetries(t)
	oldMax := qoderQueueDefaultMaxWait
	qoderQueueDefaultMaxWait = time.Nanosecond
	t.Cleanup(func() { qoderQueueDefaultMaxWait = oldMax })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(qoderEnvelopeError(500, qoderQueueInner())))
	}))
	t.Cleanup(srv.Close)

	req := qoderRequest(t, srv)
	req.IsStream = true
	req.Body = []byte(`{"model":"qd/model","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	rec := httptest.NewRecorder()
	err := ForwardQoder(rec, req)
	var upErr *proxy.UpstreamError
	if !errors.As(err, &upErr) || upErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %v, want a 503 *proxy.UpstreamError", err)
	}
	if strings.Contains(rec.Body.String(), "qoder_error") {
		t.Errorf("queue signal leaked into the client stream: %s", rec.Body.String())
	}
}
