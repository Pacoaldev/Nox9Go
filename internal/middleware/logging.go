package middleware

import (
	"net/http"
	"strings"
	"time"

	"nox9go/proxy/internal/log"
)

// statusWriter wraps http.ResponseWriter to capture the status code
// and guard against duplicate WriteHeader calls.
type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.written = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer if it implements http.Flusher.
// Without this, SSE streaming handlers lose mid-stream flushing (and
// first-token latency) because the type assertion w.(http.Flusher) fails
// on the wrapped writer.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// isQuietPath reports whether path is a routine UI asset or dashboard polling endpoint
// that should not flood terminal logs under standard INFO/WARN log levels.
func isQuietPath(path string) bool {
	if strings.HasPrefix(path, "/providers/") ||
		strings.HasPrefix(path, "/assets/") ||
		path == "/favicon.ico" ||
		path == "/favicon.svg" ||
		path == "/icons.svg" {
		return true
	}
	if path == "/api/connections" ||
		path == "/api/provider-nodes" ||
		path == "/api/usage/stats" ||
		path == "/api/usage/stream" ||
		path == "/usage/stream" ||
		path == "/health" ||
		path == "/api/tunnel/status" {
		return true
	}
	return false
}

// RequestLogger returns a middleware that logs each HTTP request with
// method, path, status code, duration, and request ID using the
// structured logger. It also strips repeated /v1/ prefixes from paths.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		path := req.URL.Path
		for len(path) > 3 && path[:4] == "/v1/" {
			path = path[3:]
		}
		req.URL.Path = path
		reqID := GetRequestIDFromContext(req.Context())
		if reqID != "" {
			w.Header().Set("X-Request-ID", reqID)
		}
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, req)

		msg := req.Method + " " + path
		durStr := time.Since(start).String()

		switch {
		case ww.status >= 500:
			log.Error("request", msg, "status", ww.status, "duration", durStr, "id", reqID)
		case isQuietPath(path):
			log.Debug("request", msg, "status", ww.status, "duration", durStr, "id", reqID)
		case ww.status >= 400:
			log.Warn("request", msg, "status", ww.status, "duration", durStr, "id", reqID)
		default:
			log.Info("request", msg, "status", ww.status, "duration", durStr, "id", reqID)
		}
	})
}
