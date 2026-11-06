package providers

import (
	"net/http"
	"testing"
)

func TestClassifyError_CapacityExhausted(t *testing.T) {
	// Status 503 with MODEL_CAPACITY_EXHAUSTED
	cls := ClassifyError(http.StatusServiceUnavailable, "No capacity available for model gemini-3.8-flash-tiered on the server MODEL_CAPACITY_EXHAUSTED", 0)
	if !cls.ShouldFallback {
		t.Error("expected fallback for capacity exhausted error")
	}
	if cls.NewBackoffLevel != 1 {
		t.Errorf("expected backoff level 1, got %d", cls.NewBackoffLevel)
	}
	if cls.CooldownMs != 2000 {
		t.Errorf("expected 2000ms cooldown, got %d", cls.CooldownMs)
	}
}

func TestClassifyError_ResourceExhausted429(t *testing.T) {
	cls := ClassifyError(http.StatusTooManyRequests, "Resource has been exhausted (e.g. check quota). RESOURCE_EXHAUSTED", 0)
	if !cls.ShouldFallback {
		t.Error("expected fallback for 429 resource exhausted")
	}
	if cls.NewBackoffLevel != 1 {
		t.Errorf("expected backoff level 1, got %d", cls.NewBackoffLevel)
	}
}

func TestClassifyError_Status503Fallback(t *testing.T) {
	// Plain 503 without specific matching text should trigger backoff via status rule
	cls := ClassifyError(http.StatusServiceUnavailable, "", 0)
	if !cls.ShouldFallback {
		t.Error("expected fallback for 503")
	}
	if cls.NewBackoffLevel != 1 {
		t.Errorf("expected backoff level 1 for 503, got %d", cls.NewBackoffLevel)
	}
}

func TestClassifyError_RequestScoped4xxNoFallback(t *testing.T) {
	// Upstream parity (checkFallbackError): request-scoped 4xx must not lock the account.
	for _, status := range []int{http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusConflict, http.StatusUnprocessableEntity} {
		cls := ClassifyError(status, "bad request body", 0)
		if cls.ShouldFallback {
			t.Errorf("expected no fallback for %d, got %+v", status, cls)
		}
	}
	// Account-scoped statuses keep fallback.
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests} {
		cls := ClassifyError(status, "", 0)
		if !cls.ShouldFallback {
			t.Errorf("expected fallback for %d, got %+v", status, cls)
		}
	}
	// 404 keeps its rule (upstream NON_RETRIABLE + status rule).
	cls := ClassifyError(http.StatusNotFound, "", 0)
	if !cls.ShouldFallback {
		t.Errorf("expected fallback for 404, got %+v", cls)
	}
}

func TestIsModelFitError_GitHubAndGroqPatterns(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{
			name:   "github copilot model_max_prompt_tokens_exceeded",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"prompt token count of 265857 exceeds the limit of 128000","code":"model_max_prompt_tokens_exceeded"}}`,
			want:   true,
		},
		{
			name:   "groq context_window in max_tokens error",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"` + "`max_tokens`" + ` must be less than or equal to ` + "`16384`" + `, the maximum value for ` + "`max_tokens`" + ` is less than the ` + "`context_window`" + ` for this model","type":"invalid_request_error","param":"max_tokens"}}`,
			want:   true,
		},
		{
			name:   "request too large 413",
			status: http.StatusRequestEntityTooLarge,
			body:   `{"error":{"message":"Request too large for model"}}`,
			want:   true,
		},
		{
			name:   "unrelated 400",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"invalid tool use format"}}`,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsModelFitError(tt.status, []byte(tt.body)); got != tt.want {
				t.Errorf("IsModelFitError(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
			}
		})
	}
}
