package chat

import (
	"context"
	"errors"
	"math"

	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/resilience"
)

// breakerGate refuses a request when the provider's circuit breaker is open.
//
// The refusal is a providerLimitError, so the failover loops treat it like any
// provider-side refusal: no account cooldown is written and the pass moves on
// to the next candidate. A dashboard probe is exempt, as it is for the other
// provider gates: it exists to learn whether the provider answers at all.
func (h *ChatHandler) breakerGate(ctx context.Context, provider string) error {
	if h == nil || h.Breakers == nil || provider == "" || handlerutil.IsProbeContext(ctx) {
		return nil
	}
	b := h.Breakers.For(provider, resilience.BreakerOptions{})
	if b.Allow() {
		return nil
	}
	retry := int(math.Ceil(b.RetryAfter().Seconds()))
	log.Warn("resilience", "circuit open, refusing request", "provider", provider, "retryAfterSec", retry)
	return h.providerLimitError(ctx, provider, providerLimitAttempt{
		Provider:   provider,
		Axis:       axisCircuit,
		RetryAfter: retry,
	})
}

// breakerReport charges the dispatch outcome to the provider's breaker. Only a
// success or a provider-level status (5xx family) counts: a 401, 429 or 400
// says something about the account or the request, and charging it would trip
// the breaker for every healthy account.
func (h *ChatHandler) breakerReport(ctx context.Context, provider string, err error) {
	if h == nil || h.Breakers == nil || provider == "" || handlerutil.IsProbeContext(ctx) {
		return
	}
	b := h.Breakers.For(provider, resilience.BreakerOptions{})
	if err == nil {
		b.RecordSuccess()
		return
	}
	var ue *upstreamError
	if errors.As(err, &ue) && resilience.IsProviderFailureStatus(ue.StatusCode) {
		b.RecordFailure()
	}
}
