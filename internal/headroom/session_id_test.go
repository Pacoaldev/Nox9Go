package headroom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"nox9go/proxy/internal/handlerutil"
)

func TestHeadroomClient_ForwardsSessionID(t *testing.T) {
	var gotSessionID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionID = r.Header.Get("x-headroom-session-id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"role":"user","content":"c"}],"tokens_before":100,"tokens_after":20,"tokens_saved":80}`))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", ts.Client())
	ctx := handlerutil.WithSessionID(context.Background(), "session-xyz-123")
	body := []byte(`{"messages":[{"role":"user","content":"some long prompt that needs compression"}],"model":"gpt-4"}`)
	_, did, err := client.Compress(ctx, body, "gpt-4", 2000)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	if !did {
		t.Fatal("expected compression to succeed")
	}
	if gotSessionID != "session-xyz-123" {
		t.Errorf("got session id %q, want session-xyz-123", gotSessionID)
	}
}

func TestHeadroomClient_OmitsSessionIDWhenUnset(t *testing.T) {
	var (
		value   string
		present bool
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value = r.Header.Get("x-headroom-session-id")
		_, present = r.Header[http.CanonicalHeaderKey("x-headroom-session-id")]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"role":"user","content":"c"}],"tokens_before":100,"tokens_after":20,"tokens_saved":80}`))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", ts.Client())
	body := []byte(`{"messages":[{"role":"user","content":"some long prompt that needs compression"}],"model":"gpt-4"}`)
	if _, did, err := client.Compress(context.Background(), body, "gpt-4", 2000); err != nil || !did {
		t.Fatalf("Compress: err=%v did=%v, want nil error and compression", err, did)
	}
	if present {
		t.Errorf("x-headroom-session-id header sent without a session in context")
	}
	if value != "" {
		t.Errorf("got session id %q without a session in context, want none", value)
	}
}
