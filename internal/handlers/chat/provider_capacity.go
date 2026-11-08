package chat

import (
	"context"
	"net/http"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/providers"
)

// Integrated-provider capacity, chat side (docs/provider-capacity-learning).
//
// A custom provider's capacity lives on its providerNodes row and is read by
// providerLimitsFor through providerNodes. An integrated provider is a catalog
// entry with no such row, so its configured capacity lives in the providerCapacity
// table, and this file bridges the two: readProviderPolicy picks the right store,
// and recordProviderObservation grows the per-provider knowledge base from the
// failures the upstream returns.

// readProviderPolicy reads the capacity policy for whichever kind of provider this
// is: an integrated provider reads providerCapacity; everything else (a custom
// node) reads providerNodes, unchanged.
//
// The two id spaces cannot collide: a custom node id is always
// "<literal>-<suffix>" (provider_node_id.go), so it can never equal a bare catalog
// id like "bazaarlink". The alias is resolved first so an alias form still finds the
// catalog entry.
func (h *ChatHandler) readProviderPolicy(ctx context.Context, provider string) (db.ProviderLimits, error) {
	canon := providers.ResolveAlias(provider)
	if _, known := providers.KnownProviders[canon]; known {
		c, err := h.Repo.GetProviderCapacity(ctx, canon)
		if err != nil {
			return db.ProviderLimits{}, err
		}
		return db.ProviderLimits{
			RPM:               c.RPM,
			TPM:               c.TPM,
			Concurrency:       c.Concurrency,
			QueueTimeoutMs:    c.QueueTimeoutMs,
			TokenQuota:        c.TokenQuota,
			ContractExpiresAt: c.ContractExpiresAt,
		}, nil
	}
	return h.Repo.GetProviderLimits(ctx, provider)
}

// recordProviderObservation stores one capacity fact an integrated provider
// reported, so the dashboard can prefill that provider's capacity editor — the
// "pequeña DB" of what each provider actually said.
//
// Only integrated providers are recorded: a custom node's provider string is its
// providerNodes id, which is not in the catalog, and its capacity is already the
// operator's own configuration rather than something to learn.
//
// It is fail-open (S7): an unknown provider, an unclassifiable failure or a storage
// error is dropped, never surfaced. Recording must not change the request's
// outcome, the same rule the provider-limit policy read follows.
func (h *ChatHandler) recordProviderObservation(ctx context.Context, provider, model string, status int, body []byte, headers http.Header) {
	if h == nil || h.Repo == nil || provider == "" || status == 0 {
		return
	}
	canon := providers.ResolveAlias(provider)
	if _, known := providers.KnownProviders[canon]; !known {
		return
	}
	obs, ok := providers.ClassifyObservation(status, body, headers)
	if !ok {
		return
	}
	if _, err := h.Repo.RecordProviderObservation(ctx, db.ProviderObservation{
		Provider:   canon,
		Model:      model,
		Signal:     obs.Signal,
		StatusCode: obs.StatusCode,
		LimitValue: obs.LimitValue,
		LimitUnit:  obs.LimitUnit,
		Evidence:   obs.Evidence,
	}); err != nil {
		log.Debug("providerobs", "record failed", "provider", canon, "error", err)
	}
}
