package executor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"nox9go/proxy/internal/providers"
)

// A provider that rejects max_tokens above the model cap names the cap in its
// 400. ForwardOpenAI must clamp the request to that number and retry once, so a
// client asking for more still gets an answer instead of a 400.
func TestForwardOpenAI_ClampsMaxTokensAndRetries(t *testing.T) {
	var calls int32
	var retriedMaxTokens atomic.Value
	retriedMaxTokens.Store("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"` + "`max_tokens`" + ` must be less than or equal to ` + "`16384`" + `, the maximum value for ` + "`max_tokens`" + ` is less than the ` + "`context_window`" + ` for this model","type":"invalid_request_error","param":"max_tokens"}}`))
			return
		}
		if strings.Contains(string(body), `"max_tokens":16384`) {
			retriedMaxTokens.Store("16384")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	req := &Request{
		Ctx:      t.Context(),
		Provider: "groq",
		Client:   srv.Client(),
		Config:   &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"},
		APIKey:   "k",
		Body:     []byte(`{"model":"qwen/qwen3.8-27b","messages":[{"role":"user","content":"hi"}],"max_tokens":64000}`),
	}
	rec := httptest.NewRecorder()
	if err := ForwardOpenAI(rec, req); err != nil {
		t.Fatalf("ForwardOpenAI returned %v, want a retried success", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (original + one retry)", got)
	}
	if retriedMaxTokens.Load() != "16384" {
		t.Errorf("the retry did not carry the clamped max_tokens; stored=%v", retriedMaxTokens.Load())
	}
	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d, want 200", rec.Code)
	}
}

// A 400 that is not a max_tokens cap must surface unchanged: retrying it would
// spend a second request on a body the upstream will reject again.
func TestForwardOpenAI_DoesNotRetryUnrelated400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid tool schema"}}`))
	}))
	defer srv.Close()

	req := &Request{
		Ctx:      t.Context(),
		Provider: "groq",
		Client:   srv.Client(),
		Config:   &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"},
		APIKey:   "k",
		Body:     []byte(`{"model":"m","max_tokens":64000}`),
	}
	rec := httptest.NewRecorder()
	if err := ForwardOpenAI(rec, req); err == nil {
		t.Fatal("expected the upstream 400 to surface")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (no retry for an unrelated 400)", got)
	}
}
