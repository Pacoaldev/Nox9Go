package media

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeForChatUsesLeanTokenBudget(t *testing.T) {
	payload := probeFor("", "llm7/codestral-latest")
	body, err := io.ReadAll(payload.body)
	if err != nil {
		t.Fatalf("read probe body: %v", err)
	}
	if !strings.Contains(string(body), `"max_tokens":256`) {
		t.Fatalf("probe body = %s, want lean 256-token budget (Check All must not burn a 1024-token completion)", body)
	}
	if strings.Contains(string(body), `"max_tokens":1024`) {
		t.Fatalf("probe still asks for 1024 tokens: %s", body)
	}
}

func TestReadProbeResult_429SurfacesRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Retry-After", "1")
	rec.WriteHeader(http.StatusTooManyRequests)
	rec.Write([]byte(`{"error":{"message":"Rate limit exceeded. Retry after 1 seconds."}}`))

	res := readProbeResult(rec, "")
	if res.OK {
		t.Fatal("expected OK=false for 429")
	}
	if res.RetryAfter != 1 {
		t.Fatalf("RetryAfter = %d, want 1 from Retry-After header", res.RetryAfter)
	}
}

func TestReadProbeResult_Non429OmitsRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Retry-After", "30")
	rec.WriteHeader(http.StatusBadGateway)
	rec.Write([]byte(`{"error":{"message":"upstream"}}`))

	res := readProbeResult(rec, "")
	if res.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %d, want 0 on non-429", res.RetryAfter)
	}
}
