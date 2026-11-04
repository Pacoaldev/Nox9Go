package executor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nox9go/proxy/internal/providers"
)

func TestForwardOpenAI_DropsUnsupportedPromptCacheKeyForGroq(t *testing.T) {
	var forwarded string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		forwarded = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	reqBody := `{"model":"qwen/qwen3.8-27b","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"cache-1"}`
	rec := httptest.NewRecorder()
	err := ForwardOpenAI(rec, &Request{
		Ctx: t.Context(), Client: srv.Client(),
		Config:   &providers.ProviderConfig{BaseURL: srv.URL},
		Provider: "groq",
		Body:     []byte(reqBody),
	})
	if err != nil {
		t.Fatalf("ForwardOpenAI: %v", err)
	}
	if strings.Contains(forwarded, "prompt_cache_key") {
		t.Fatalf("Groq request retained unsupported prompt_cache_key: %s", forwarded)
	}
}
