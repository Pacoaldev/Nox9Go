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
	{"quota exhausted", SignalQuotaExceeded},
	{"quota has been exhausted", SignalQuotaExceeded},
	{"free quota", SignalQuotaExceeded},
	{"token quota", SignalQuotaExceeded},
	{"resource_exhausted", SignalQuotaExceeded},
	{"resource has been exhausted", SignalQuotaExceeded},
	{"insufficient quota", SignalQuotaExceeded},
	{"insufficient credits", SignalQuotaExceeded},
	{"insufficient balance", SignalQuotaExceeded},
	{"insufficient funds", SignalQuotaExceeded},
	{"balance is insufficient", SignalQuotaExceeded},
	{"you have reached the daily limit", SignalQuotaExceeded},
	{"daily limit", SignalQuotaExceeded},
	{"free-models-per-day", SignalQuotaExceeded},
	{"usage limit reached", SignalQuotaExceeded},
	{"usage limit has been reached", SignalQuotaExceeded},
	{"free usage limit", SignalQuotaExceeded},
	{"free allowance", SignalQuotaExceeded},
	{"is used up", SignalQuotaExceeded},
	{"spending limit", SignalQuotaExceeded},
	{"positive credit balance", SignalQuotaExceeded},
	{"positive pay-as-you-go balance", SignalQuotaExceeded},
	{"active subscription", SignalQuotaExceeded},
	{"accounts that have topped up", SignalQuotaExceeded},
	{"account balance", SignalQuotaExceeded},
	{"free-tier", SignalQuotaExceeded},
	{"out of credits", SignalQuotaExceeded},
	{"credit balance", SignalQuotaExceeded},
	{"pricingurl", SignalQuotaExceeded},
	{"用量上限", SignalQuotaExceeded},
	{"余额不足", SignalQuotaExceeded},
	{"额度已用尽", SignalQuotaExceeded},
	{"额度不足", SignalQuotaExceeded},
	{"免费额度", SignalQuotaExceeded},
	{"model_capacity_exhausted", SignalCapacity},
	{"no capacity available", SignalCapacity},
	{"at capacity", SignalCapacity},
	{"concurrency capacity", SignalCapacity},
	{"overloaded", SignalCapacity},
	{"server is temporarily unavailable", SignalCapacity},
	{"temporarily unavailable", SignalCapacity},
	{"currently unavailable", SignalCapacity},
	{"model is unavailable", SignalCapacity},
	{"service is busy", SignalCapacity},
	{"resource depleted", SignalCapacity},
	{"capacity", SignalCapacity},
	{"rate limit", SignalRateLimit},
	{"rate-limit", SignalRateLimit},
	{"too many requests", SignalRateLimit},
	{"too many invalid requests", SignalRateLimit},
	{"tokens per minute", SignalRateLimit},
	{"requests per minute", SignalRateLimit},
	{"request(s) per minute", SignalRateLimit},
	{"(tpm)", SignalRateLimit},
	{"(itpm)", SignalRateLimit},
	{"(rpm)", SignalRateLimit},
	{"resource_limit", SignalRateLimit},
	{"速率限制", SignalRateLimit},
	{"请求过于频繁", SignalRateLimit},
	{"频率限制", SignalRateLimit},
	{"timed out", SignalTimeout},
	{"timeout", SignalTimeout},
	{"deadline exceeded", SignalTimeout},
	{"invalid api key", SignalAuth},
	{"api key is invalid", SignalAuth},
	{"invalid_api_key", SignalAuth},
	{"authentication expired", SignalAuth},
	{"invalidated oauth token", SignalAuth},
	{"bind your alibaba cloud account", SignalAuth},
	{"missing userid", SignalAuth},
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

var embeddedShapeErrorNeedles = []string{
	"maximum context length",
	"context_length_exceeded",
	"supports text input only",
	"does not support image",
	"doesn't support the image",
	"does not support multimodal",
	"does not support vision",
	"multimodal content type",
	"may not support the input you sent",
	"at most 12 image",
	"invalid tool use format",
	"unexpected message role",
	"profilearn is required",
	"inference request is invalid",
	"not supported on this endpoint",
	"the requested model is not supported",
	"provider test not supported",
}

func isEmbeddedShapeError(lowerText string) bool {
	for _, needle := range embeddedShapeErrorNeedles {
		if strings.Contains(lowerText, needle) {
			return true
		}
	}
	return false
}

func isExplicitRateOrQuotaText(lowerText string) bool {
	for _, needle := range []string{
		"tokens per minute",
		"requests per minute",
		"request(s) per minute",
		"(tpm)",
		"(itpm)",
		"(rpm)",
		"rate limit",
		"rate-limit",
		"quota",
		"insufficient balance",
		"accounts that have topped up",
		"account balance",
		"free models are no longer offered",
	} {
		if strings.Contains(lowerText, needle) {
			return true
		}
	}
	return false
}

// ClassifyObservation reduces an upstream failure to a capacity fact.
//
// A request-shape error (model fit, malformed request) is not a capacity fact, so
// it answers ok=false even though it came with a status: recording it would fill the
// knowledge base with rows that say nothing about how much the provider tolerates.
func ClassifyObservation(statusCode int, body []byte, headers http.Header) (Observation, bool) {
	text := strings.ToLower(string(body))
	if strings.HasPrefix(strings.TrimSpace(text), "provider ") && strings.Contains(text, "refused this request at its") {
		return Observation{}, false
	}
	if isEmbeddedShapeError(text) {
		return Observation{}, false
	}
	if !isExplicitRateOrQuotaText(text) && (IsModelFitError(statusCode, body) || IsRequestInvalidError(statusCode, body)) {
		return Observation{}, false
	}

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

var explicitRateLimitHeaders = []struct {
	key  string
	unit string
}{
	{"x-ratelimit-limit-requests-minute", "rpm"},
	{"x-ratelimit-limit-requests-per-minute", "rpm"},
	{"x-ratelimit-limit-rpm", "rpm"},
	{"anthropic-ratelimit-requests-limit", "rpm"},
	{"x-ratelimit-limit-tokens-minute", "tpm"},
	{"x-ratelimit-limit-tokens-per-minute", "tpm"},
	{"x-ratelimit-limit-tpm", "tpm"},
	{"anthropic-ratelimit-tokens-limit", "tpm"},
	{"x-ratelimit-limit-requests-day", "rpd"},
	{"x-ratelimit-limit-requests-per-day", "rpd"},
	{"x-ratelimit-limit-rpd", "rpd"},
	{"x-ratelimit-limit-concurrent", "concurrent"},
	{"x-ratelimit-limit-concurrency", "concurrent"},
}

// ClassifyHeaderObservations extracts rate-limit observations from HTTP response
// headers (on both 200 OK and error responses) when explicit limit headers are present.
func ClassifyHeaderObservations(statusCode int, headers http.Header) []Observation {
	if headers == nil {
		return nil
	}
	var out []Observation
	seenUnits := map[string]bool{}
	for _, h := range explicitRateLimitHeaders {
		if seenUnits[h.unit] {
			continue
		}
		if v, ok := firstHeaderInt(headers, h.key); ok {
			seenUnits[h.unit] = true
			out = append(out, Observation{
				Signal:     SignalRateLimit,
				StatusCode: statusCode,
				LimitValue: v,
				LimitUnit:  h.unit,
				Evidence:   h.key + ": " + strconv.Itoa(v),
			})
		}
	}
	return out
}

// Structured and natural-language patterns that carry a numeric limit. Ordered so
// the most specific unit wins: "tokens per minute" before "requests", and any
// "per minute" phrase yields an rpm/tpm unit rather than a bare count.
var (
	obsTPMRe         = regexp.MustCompile(`(?i)(?:(\d+)\s*(?:input\s+)?tokens?\s*(?:per|/)\s*(?:min|minute)|(?:tokens?\s*(?:per|/)\s*(?:min|minute)|\b(?:i?tpm)\b)[^0-9]{0,24}?limit[:\s(]+(\d+)|\btpm\s+limit\s*\(?(\d+)\)?|(\d+)\s*(?:i?tpm)\b)`)
	obsRPMRe         = regexp.MustCompile(`(?i)(?:(\d+)\s*(?:requests?(?:\(s\))?|calls?)\s*(?:per|/)\s*(?:min|minute)|(?:requests?\s*(?:per|/)\s*(?:min|minute)|\brpm\b)[^0-9]{0,24}?limit[:\s(]+(\d+)|\brpm\s+limit\s*\(?(\d+)\)?|(\d+)\s*rpm\b)`)
	obsRPDRe         = regexp.MustCompile(`(?i)(?:(\d+)\s*(?:requests?(?:\(s\))?|calls?)\s*(?:per|/)\s*(?:day|24\s*hours?)|(\d+)\s*rpd\b)`)
	obsConcurRe      = regexp.MustCompile(`(?i)(\d+)\s+(?:concurrent|simultaneous)\s+(?:requests?|connections?)`)
	obsRequestsRe    = regexp.MustCompile(`(?i)(\d+)\s+(?:free[- ]model\s+)?requests?`)
	obsTokensRe      = regexp.MustCompile(`(?i)(\d+)\s+tokens?`)
	obsUpsellRe      = regexp.MustCompile(`(?i)\b(?:to\s+unlock|unlock)\s+\d+\s+(?:free[- ]model\s+)?requests?(?:\s+(?:per|/)\s*day)?`)
	obsPriceTokensRe = regexp.MustCompile(`(?i)\$\d+(?:\.\d+)?\s*(?:per|/)\s*\d+[km]?\s+tokens?`)
)

// parseObservedLimit returns the first parseable numeric limit and its unit,
// preferring the structured x-ratelimit headers over the free-text body.
func parseObservedLimit(lowerText string, headers http.Header) (int, string) {
	if headers != nil {
		for _, h := range explicitRateLimitHeaders {
			if v, ok := firstHeaderInt(headers, h.key); ok {
				return v, h.unit
			}
		}
		if v, ok := firstHeaderInt(headers, "x-ratelimit-limit-requests"); ok {
			if strings.Contains(lowerText, "requests per minute") || strings.Contains(lowerText, "request(s) per minute") || strings.Contains(lowerText, "(rpm)") {
				return v, "rpm"
			}
			return v, dailyUnitIfNamed(lowerText, "requests")
		}
		if v, ok := firstHeaderInt(headers, "x-ratelimit-limit-tokens"); ok {
			if strings.Contains(lowerText, "tokens per minute") || strings.Contains(lowerText, "(tpm)") || strings.Contains(lowerText, "(itpm)") {
				return v, "tpm"
			}
			return v, "tokens"
		}
	}
	cleanedText := obsUpsellRe.ReplaceAllString(lowerText, "")
	cleanedText = obsPriceTokensRe.ReplaceAllString(cleanedText, "")
	for _, m := range []struct {
		re   *regexp.Regexp
		unit string
	}{
		{obsTPMRe, "tpm"},
		{obsRPMRe, "rpm"},
		{obsRPDRe, "rpd"},
		{obsConcurRe, "concurrent"},
		{obsRequestsRe, "requests"},
		{obsTokensRe, "tokens"},
	} {
		if v, ok := firstGroupInt(m.re, cleanedText); ok {
			return v, dailyUnitIfNamed(cleanedText, m.unit)
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
	for _, g := range m[1:] {
		if g == "" {
			continue
		}
		if v, err := strconv.Atoi(g); err == nil && v > 0 {
			return v, true
		}
	}
	return 0, false
}

// observationEvidence turns a body into a short readable snippet: the JSON
// `error.message` when the body carries one, else the trimmed raw text.
func observationEvidence(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || strings.EqualFold(trimmed, "request failed") {
		return ""
	}
	if s := jsonErrorMessage(trimmed); s != "" {
		if strings.EqualFold(s, "request failed") {
			return ""
		}
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
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err == nil {
		for _, s := range []string{probe.Error.Message, probe.Message, probe.Detail} {
			if t := strings.TrimSpace(s); t != "" {
				return t
			}
		}
		for _, e := range probe.Errors {
			if t := strings.TrimSpace(e.Message); t != "" {
				return t
			}
		}
	}
	var strErr struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &strErr); err == nil {
		if t := strings.TrimSpace(strErr.Error); t != "" {
			return t
		}
	}
	var arrProbe []struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &arrProbe); err == nil {
		for _, item := range arrProbe {
			for _, s := range []string{item.Error.Message, item.Message} {
				if t := strings.TrimSpace(s); t != "" {
					return t
				}
			}
		}
	}
	return ""
}
