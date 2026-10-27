package chat

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/providers"
)

// providerModelLockoutReason is the machine text stored in the lockout row,
// so the dashboard can tell a quota lockout from any other reason.
const providerModelLockoutReason = "quota_exhausted"

// providerModelQuotaMarkers are the phrases that turn an upstream failure into
// a provider+model lockout.
//
// The set is wider than isModelQuotaText on purpose. That helper answers "is
// this failure scoped to the model", a per-connection lock question, while
// this one answers "is the provider's quota for this model spent", a
// per-pair question. A pair lockout is the heavier verdict — it takes every
// key of the provider out of rotation for that model — so it earns the wider
// vocabulary.
//
// "rate limit" and "too many requests" are deliberately absent even though
// providers.ErrorRules uses them for the backoff ladder: they name a
// per-account or per-IP limit, which the operator's other keys still serve.
// Treating them as quota markers would park a whole provider on the first
// burst, which is the failure mode this feature exists to stop.
var providerModelQuotaMarkers = []string{
	"quota_exhausted",
	"resource_exhausted",
	"model_capacity_exhausted",
	"resource has been exhausted",
	"quota exceeded",
	"individual quota reached",
	"capacity",
	"overloaded",
}

// sharedQuotaProviders lists the providers whose keys are known to share an
// upstream quota or account limit, so a quota exhaustion on one key means the
// other keys answer the same way. Every other provider is assumed to have
// independent per-key quotas (the common case: each key has its own pool of
// tokens), and the pair is NOT parked so the remaining keys can serve the turn.
// Empty by default: the operator must explicitly opt a provider in.
var sharedQuotaProviders = map[string]bool{}

// EnableSharedQuotaProvider opts a provider in to pair-level lockouts.
// Exported for tests.
func EnableSharedQuotaProvider(provider string) {
	sharedQuotaProviders[strings.ToLower(provider)] = true
}

// ResetSharedQuotaProviders clears the opt-in list.
func ResetSharedQuotaProviders() {
	sharedQuotaProviders = map[string]bool{}
}

// isProviderModelLockout reports whether an upstream failure parks the whole
// (provider, model) pair.
//
// It only fires if the provider is opted in via sharedQuotaProviders. For all
// other providers, the failure is treated as per-connection only, so the
// remaining keys keep serving.
func isProviderModelLockout(provider string, statusCode int, body []byte) bool {
	if !sharedQuotaProviders[strings.ToLower(provider)] {
		return false
	}
	switch statusCode {
	case http.StatusPaymentRequired:
		return true
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return providerModelQuotaText(extractErrorText(body))
	}
	return false
}

// providerModelQuotaText reports whether the upstream text names a spent quota
// or a capacity limit for the model.
func providerModelQuotaText(errorText string) bool {
	if errorText == "" {
		return false
	}
	lower := strings.ToLower(errorText)
	for _, marker := range providerModelQuotaMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// providerModelLockoutUntil computes how long the pair stays parked.
//
// The classifier's own cooldown is the baseline, so a 429 without a wait keeps
// the backoff ladder and a 402 keeps the two minutes the rules give it. A
// Retry-After or a reset hint in the body overrides the baseline, and
// retryableCooldownSec caps the result at maxResetCooldown, so a hostile
// duration can never park a pair forever.
func providerModelLockoutUntil(provider string, statusCode int, backoffLevel int, ue *upstreamError) time.Time {
	text := ""
	if ue != nil {
		text = extractErrorText(ue.Body)
	}
	cls := providers.ClassifyError(statusCode, text, backoffLevel)
	base := time.Duration(cls.CooldownMs) * time.Millisecond
	if base <= 0 {
		base = time.Duration(providers.GetQuotaCooldown(backoffLevel)) * time.Millisecond
	}
	sec := 0
	if ue != nil {
		sec = retryableCooldownSec(provider, statusCode, base, ue)
	}
	if sec <= 0 {
		sec = ceilSeconds(base)
	}
	return time.Now().UTC().Add(time.Duration(sec) * time.Second)
}

// recordProviderModelLockout parks the pair after a failed upstream attempt.
//
// A probe is a no-op: the dashboard's Test button reaches the provider for
// real, and parking a pair because the operator tested it would take a
// working pair out of rotation for traffic that never failed.
func (h *ChatHandler) recordProviderModelLockout(ctx context.Context, provider, model, connID string, ue *upstreamError) {
	if handlerutil.IsProbeContext(ctx) {
		return
	}
	if h.Repo == nil || provider == "" || model == "" || ue == nil {
		return
	}
	if !isProviderModelLockout(provider, ue.StatusCode, ue.Body) {
		return
	}
	key := canonicalLockModel(provider, model)
	until := providerModelLockoutUntil(provider, ue.StatusCode, h.Repo.GetConnectionBackoffLevel(connID), ue)
	if err := h.Repo.LockProviderModel(provider, key, until, providerModelLockoutReason, ue.StatusCode); err != nil {
		log.Warn("provider-model", "lock failed", "provider", provider, "model", key, "error", err)
		return
	}
	log.Warn("provider-model", "pair parked", "provider", provider, "model", key,
		"status", ue.StatusCode, "until", until.Format(time.RFC3339))
}

// providerModelLocked reports whether the pair is parked, and until when.
//
// A read error fails open: the row is a hint that the pair is dead, and
// refusing to route on a database hiccup would take the whole provider out of
// rotation for a reason that has nothing to do with the provider.
func (h *ChatHandler) providerModelLocked(provider, model string) (time.Time, bool) {
	if h.Repo == nil || provider == "" || model == "" {
		return time.Time{}, false
	}
	key := canonicalLockModel(provider, model)
	until, locked, err := h.Repo.ProviderModelLockedUntil(provider, key)
	if err != nil {
		log.Warn("provider-model", "lock read failed", "provider", provider, "model", key, "error", err)
		return time.Time{}, false
	}
	if !locked {
		return time.Time{}, false
	}
	return until, true
}

// providerModelLockedError is the error body a parked pair answers with. It
// names the pair and the deadline, so the operator can see why the turn failed
// instead of a bare "all models failed".
type providerModelLockedError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// providerModelLockedBody renders the 429 the client receives when every combo
// member is parked.
func providerModelLockedBody(provider, model string, until time.Time) []byte {
	var payload providerModelLockedError
	payload.Error.Message = fmt.Sprintf(
		"provider %s model %s is in quota lockout until %s",
		provider, model, until.UTC().Format(time.RFC3339),
	)
	payload.Error.Type = "rate_limit_error"
	payload.Error.Code = http.StatusTooManyRequests

	body, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"error":{"message":"provider model in quota lockout","type":"rate_limit_error","code":429}}`)
	}
	return body
}
