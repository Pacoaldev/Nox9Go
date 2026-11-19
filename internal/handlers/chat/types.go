package chat

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlers/shared"
	"nox9go/proxy/internal/middleware"
	"nox9go/proxy/internal/models"
	"nox9go/proxy/internal/proxy"
	"nox9go/proxy/internal/quotalearn"
	"nox9go/proxy/internal/resilience"
	"nox9go/proxy/internal/semanticcache"
)

type comboStickyState struct {
	Index               int
	ConsecutiveUseCount int
	// ServingIndex is the model index currently owning the turn. Mid-turn
	// requests reuse it so a tool-use sequence stays on the same provider,
	// even after Index has advanced for the next turn.
	ServingIndex int
	// LastModel is the model that served the previous turn of this combo,
	// so a mid-session switch at a turn boundary can inject a compact
	// context handoff note (S3).
	LastModel string
}

// ChatHandler handles /v1/chat/completions (OpenAI) and /v1/messages (Claude) endpoints.
type ChatHandler struct {
	Repo       *db.Repo
	Client     *http.Client
	TokenSaver *shared.TokenSaverConfig
	// RateLimiter is the process-wide limiter the request path charges against.
	// The handler holds it so the metering path can reconcile a TPM
	// reservation once the real usage is known: the estimate is taken
	// pre-dispatch, but only the response knows what the turn actually cost.
	RateLimiter   *middleware.RateLimiter
	SemanticCache *semanticcache.Cache
	// Breakers holds the per-provider circuit breakers. Nil on a handler built
	// without NewChatHandler, which disables the gate.
	Breakers *resilience.Registry
	// Quota learns per-(provider, model, connection) rate-limit ceilings and
	// applies proactive cooldowns so combo fallback skips a key the provider
	// has already throttled. Nil on a handler built without NewChatHandler.
	Quota       *quotalearn.Learner
	stickyMu    sync.Mutex
	stickyState map[string]*comboStickyState
	// sessionPins keeps a conversation on the account that served its first
	// turn, so the provider's per-account prompt cache stays warm
	// (session_affinity.go).
	sessionPins sessionPinState
	// oauthRefreshFlight collapses concurrent OAuth token refreshes for the
	// same connection, so an expired token triggers one upstream round-trip
	// instead of one per in-flight request.
	oauthRefreshFlight singleflight.Group
	// deprecationCache throttles kv writes when a provider retires a model:
	// every request at a dead combo entry arrives as a fresh 410, and each
	// one would otherwise upsert the same row.
	deprecationMu    sync.Mutex
	deprecationCache map[string]time.Time
}

var comboMemberTimeout = 15 * time.Second

// Type aliases for shared types
type ModelInfo = shared.ModelInfo

// ProviderConnection is the stored connection row.
type ProviderConnection = models.ProviderConnection
type ConnectionData = shared.ConnectionData
type UsageLogInfo = shared.UsageLogInfo
type streamMetrics = shared.StreamMetrics
type upstreamError = proxy.UpstreamError
