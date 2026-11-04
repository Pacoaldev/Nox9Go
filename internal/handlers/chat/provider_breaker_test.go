package chat

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"nox9go/proxy/internal/resilience"
)

func TestBreakerGateRefusesOpenProviderWithoutAccountFault(t *testing.T) {
	const provider = "breaker-gate-test"
	h := &ChatHandler{Breakers: resilience.NewRegistry()}
	ctx := withProviderLimitState(context.Background())

	if err := h.breakerGate(ctx, provider); err != nil {
		t.Fatalf("closed breaker refused: %v", err)
	}
	for i := 0; i < 5; i++ {
		h.breakerReport(ctx, provider, &upstreamError{StatusCode: http.StatusBadGateway})
	}
	err := h.breakerGate(ctx, provider)
	if err == nil {
		t.Fatal("open breaker admitted a request")
	}
	if _, refused := providerLimitRefusal(err); !refused {
		t.Fatalf("refusal must be a providerLimitError so no account cooldown is written, got %T", err)
	}
	var ue *upstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %+v, want 503", ue)
	}
}

func TestBreakerReportIgnoresAccountLevelStatuses(t *testing.T) {
	const provider = "breaker-report-test"
	h := &ChatHandler{Breakers: resilience.NewRegistry()}
	for _, status := range []int{401, 429, 400, 403} {
		for i := 0; i < 10; i++ {
			h.breakerReport(context.Background(), provider, &upstreamError{StatusCode: status})
		}
	}
	if st := h.Breakers.For(provider, resilience.BreakerOptions{}).State(); st != resilience.StateClosed {
		t.Fatalf("account-level statuses tripped the breaker: %s", st)
	}
}