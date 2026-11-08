package providers

import (
	json "encoding/json/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Observation is one capacity fact an upstream reported, reduced to a shape the
// gateway can store per (provider, model).
//
// It is deliberately NOT the raw body: Signal is a small closed vocabulary,
// LimitValue/LimitUnit carry a number only when one was parseable, and Evidence is
// a short human-readable snippet kept for the operator. This is the "pequeña DB"
// the dashboard reads to prefill an integrated provider's capacity editor — a fact
// the provider stated, never a policy the gateway decided.
type Observation struct {
	// Signal is one of the Signal* constants below.
	Signal string
	// StatusCode is the upstream status the fact arrived with.
	StatusCode int
	// LimitValue is the parsed limit, or 0 when the fact carried no number.
	LimitValue int
	// LimitUnit is "rpm", "tpm", "requests", "tokens", "concurrent" or "".
	LimitUnit string
	// Evidence is a short snippet explaining the fact. Never contains a credential.
	Evidence string
}

// Signals. Only the ones that say something about capacity are emitted; a plain
// request-shape error (bad tool call, unsupported modality) is not a capacity fact
// and ClassifyObservation reports ok=false for it.
const (
	SignalRateLimit     = "rate_limit"
	SignalQuotaExceeded = "quota_exceeded"
	SignalCapacity      = "capacity"
	SignalTimeout       = "timeout"
	SignalAuth          = "auth"
	SignalServerError   = "server_error"
)

// Signal vocabulary used by both the recorder and the dashboard.

// observationTextRules maps a phrase to the signal it implies. Order matters:
// the first match wins, and the more specific quota/capacity phrases are checked
// before the generic rate-limit ones so "rate limit quota exhausted" reads as a
// quota, not a transient rate limit.
var observationTextRules = []struct {
	needle string
	signal string
}{
	{"quota exceeded", SignalQuotaExceeded},
	{"resource_exhausted", SignalQuotaExceeded},
	{"resource has been exhausted", SignalQuotaExceeded},
	{"insufficient quota", SignalQuotaExceeded},
	{"insufficient credits", SignalQuotaExceeded},
	{"you have reached the daily limit", SignalQuotaExceeded},
	{"daily limit", SignalQuotaExceeded},
	{"free-tier", SignalQuotaExceeded},
	{"out of credits", SignalQuotaExceeded},
	{"credit balance", SignalQuotaExceeded},
	{"model_capacity_exhausted", SignalCapacity},
	{"no capacity available", SignalCapacity},
	{"at capacity", SignalCapacity},
	{"overloaded", SignalCapacity},
	{"server is temporarily unavailable", SignalCapacity},
	{"resource depleted", SignalCapacity},
	{"capacity", SignalCapacity},
	{"rate limit", SignalRateLimit},
	{"rate-limit", SignalRateLimit},
	{"too many requests", SignalRateLimit},
	{"timed out", SignalTimeout},
	{"timeout", SignalTimeout},
	{"deadline exceeded", SignalTimeout},
}

// observationStatusSignals maps a status to a signal when the body said nothing
// recognizable. Kept minimal on purpose: a status must not invent a capacity fact
// it does not carry.
func observationStatusSignal(status int) (string, bool) {
	switch status {
	case http.StatusTooManyRequests:
		return SignalRateLimit, true
	case http.StatusPaymentRequired:
		return SignalQuotaExceeded, true
	case http.StatusUnauthorized, http.StatusForbidden:
		return SignalAuth, true
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return SignalTimeout, true
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return SignalCapacity, true
	case http.StatusInternalServerError:
		return SignalServerError, true
	default:
		return "", false
	}
}

// ClassifyObservation reduces an upstream failure to a capacity fact.
//
// A request-shape error (model fit, malformed request) is not a capacity fact, so
// it answers ok=false even though it came with a status: recording it would fill the
// knowledge base with rows that say nothing about how much the provider tolerates.
func ClassifyObservation(statusCode int, body []byte, headers http.Header) (Observation, bool) {
	if IsModelFitError(statusCode, body) || IsRequestInvalidError(statusCode, body) {
		return Observation{}, false
	}
	text := strings.ToLower(string(body))

	signal := ""
	for _, rule := range observationTextRules {
		if strings.Contains(text, rule.needle) {
			signal = rule.signal
			break
		}
	}
	if signal == "" {
		s, ok := observationStatusSignal(statusCode)
		if !ok {
			return Observation{}, false
		}
		signal = s
	}

	value, unit := parseObservedLimit(text, headers)
	return Observation{
		Signal:     signal,
		StatusCode: statusCode,
		LimitValue: value,
		LimitUnit:  unit,
		Evidence:   observationEvidence(body),
	}, true
}

// Structured and natural-language patterns that carry a numeric limit. Ordered so
// the most specific unit wins: "tokens per minute" before "requests", and any
// "per minute" phrase yields an rpm/tpm unit rather than a bare count.
var (
	obsTPMRe      = regexp.MustCompile(`(?i)(\d+)\s*tokens?\s*(?:per|/)\s*(?:min|minute)`)
	obsRPMRe      = regexp.MustCompile(`(?i)(\d+)\s*(?:requests?|calls?)\s*(?:per|/)\s*(?:min|minute)`)
	obsConcurRe   = regexp.MustCompile(`(?i)(\d+)\s+(?:concurrent|simultaneous)\s+(?:requests?|connections?)`)
	obsRequestsRe = regexp.MustCompile(`(?i)(\d+)\s+(?:free[- ]model\s+)?requests?`)
	obsTokensRe   = regexp.MustCompile(`(?i)(\d+)\s+tokens?`)
)

// parseObservedLimit returns the first parseable numeric limit and its unit,
// preferring the structured x-ratelimit headers over the free-text body.
func parseObservedLimit(lowerText string, headers http.Header) (int, string) {
	if headers != nil {
		if v, ok := firstHeaderInt(headers, "x-ratelimit-limit-requests"); ok {
			return v, "requests"
		}
		if v, ok := firstHeaderInt(headers, "x-ratelimit-limit-tokens"); ok {
			return v, "tokens"
		}
	}
	for _, m := range []struct {
		re   *regexp.Regexp
		unit string
	}{
		{obsTPMRe, "tpm"},
		{obsRPMRe, "rpm"},
		{obsConcurRe, "concurrent"},
		{obsRequestsRe, "requests"},
		{obsTokensRe, "tokens"},
	} {
		if v, ok := firstGroupInt(m.re, lowerText); ok {
			return v, dailyUnitIfNamed(lowerText, m.unit)
		}
	}
	return 0, ""
}

// dailyUnitIfNamed upgrades a request or token count to the daily unit when the
// text says the allowance is per day.
//
// A provider that publishes "the daily limit of 60 free-model requests" is stating
// a fixed daily budget, not a per-minute rate, and the two must not be confused:
// filling a per-minute field with a daily figure would throttle the provider by
// three orders of magnitude.
func dailyUnitIfNamed(lowerText, unit string) string {
	if unit != "requests" && unit != "tokens" {
		return unit
	}
	if strings.Contains(lowerText, "per day") || strings.Contains(lowerText, "daily") ||
		strings.Contains(lowerText, "per 24 hours") || strings.Contains(lowerText, "each day") {
		return "rpd"
	}
	return unit
}

func firstHeaderInt(headers http.Header, key string) (int, bool) {
	for _, raw := range headers.Values(key) {
		if v, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && v > 0 {
			return v, true
		}
	}
	return 0, false
}

func firstGroupInt(re *regexp.Regexp, text string) (int, bool) {
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0, false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// observationEvidence turns a body into a short readable snippet: the JSON
// `error.message` when the body carries one, else the trimmed raw text.
func observationEvidence(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	if s := jsonErrorMessage(trimmed); s != "" {
		return s
	}
	if len(trimmed) > 512 {
		trimmed = trimmed[:512]
	}
	return trimmed
}

// jsonErrorMessage digs `error.message`, `message` or `detail` out of a JSON error
// body, returning "" for a non-JSON body or a JSON body without one.
func jsonErrorMessage(raw string) string {
	var probe struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return ""
	}
	for _, s := range []string{probe.Error.Message, probe.Message, probe.Detail} {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}
