package proxy

import (
	"context"
	"fmt"
	"net/url"
	"testing"
)

func TestIsTransientGeminiTransportError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{err: fmt.Errorf("Post %q: http2: timeout awaiting response headers", "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent"), want: true},
		{err: fmt.Errorf("timeout awaiting response headers"), want: true},
		{err: context.DeadlineExceeded, want: true},
		{err: &url.Error{Op: "Post", URL: "https://x", Err: fmt.Errorf("timeout")}, want: false},
		{err: fmt.Errorf("EOF"), want: true},
		{err: fmt.Errorf("connection reset by peer"), want: true},
		{err: fmt.Errorf("dial tcp: connection refused"), want: true},
		{err: fmt.Errorf("unknown error"), want: false},
		{err: nil, want: false},
	}
	for i, c := range cases {
		if got := isTransientGeminiTransportError(c.err); got != c.want {
			t.Errorf("case %d isTransientGeminiTransportError(%v) = %v, want %v", i, c.err, got, c.want)
		}
	}
}