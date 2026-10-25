package chat

import (
	"net/http"
	"sync/atomic"
)

type comboAttemptWriter struct {
	http.ResponseWriter
	onCommit func()
}

func (w comboAttemptWriter) WriteHeader(status int) {
	w.onCommit()
	w.ResponseWriter.WriteHeader(status)
}

func (w comboAttemptWriter) Write(data []byte) (int, error) {
	w.onCommit()
	return w.ResponseWriter.Write(data)
}

func (w comboAttemptWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w comboAttemptWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type committedResponseWriter struct {
	http.ResponseWriter
	committed int32 // atomic: 1 if headers have been sent
}

func newCommittedResponseWriter(w http.ResponseWriter) *committedResponseWriter {
	return &committedResponseWriter{ResponseWriter: w}
}

func (cw *committedResponseWriter) WriteHeader(code int) {
	atomic.StoreInt32(&cw.committed, 1)
	cw.ResponseWriter.WriteHeader(code)
}

func (cw *committedResponseWriter) Write(b []byte) (int, error) {
	atomic.StoreInt32(&cw.committed, 1)
	return cw.ResponseWriter.Write(b)
}

// IsCommitted returns true if response headers have been sent.
func (cw *committedResponseWriter) IsCommitted() bool {
	return atomic.LoadInt32(&cw.committed) == 1
}

// Flush implements http.Flusher if the underlying writer supports it.
func (cw *committedResponseWriter) Flush() {
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
