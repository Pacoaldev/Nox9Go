package dashboard

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/middleware"
	"nox9go/proxy/internal/providers"
)

// Integrated-provider capacity (docs/provider-capacity-learning).
//
// This is the operator's view of the feature the chat side enforces
// (internal/handlers/chat/provider_capacity.go): a catalog provider — which has no
// providerNodes row — gets the same capacity editor a custom node has, and the
// editor can be prefilled from the capacity facts that provider has actually
// reported at runtime.
//
// It mirrors provider_node_limits.go on purpose: the same fields, the same
// tri-state PUT merge, the same "0 means unlimited / empty means no expiry"
// convention, so an operator who learned one editor has learned both.

// ProviderCapacityResponse is an integrated provider's policy plus what has been
// learned about it, so the dashboard renders both from one read.
type ProviderCapacityResponse struct {
	Provider string `json:"provider"`
	// Limits is the operator's configured policy. Zero on every axis means unlimited.
	Limits ProviderCapacityLimits `json:"limits"`
	// Observations is every capacity fact the provider reported, newest first.
	Observations []db.ProviderObservation `json:"observations"`
	// Suggestions are values derived from the observations, for prefilling the
	// editor. They are never applied automatically — the operator saves to apply.
	Suggestions ProviderCapacitySuggestions `json:"suggestions"`
	// DailyUsedWindow is how many requests the provider spent today against its
	// daily budget, read from the limiter. It is 0 when no daily budget is
	// configured or the limiter is not wired.
	DailyUsedWindow int `json:"dailyUsedWindow"`
}

// ProviderCapacityLimits is the editable policy shape.
type ProviderCapacityLimits struct {
	RPM               int `json:"rpm"`
	TPM               int `json:"tpm"`
	Concurrency       int `json:"concurrency"`
	DailyRequests     int `json:"dailyRequests"`
	QueueTimeoutMs    int `json:"queueTimeoutMs"`
	TokenQuota        int `json:"tokenQuota"`
	ContractExpiresAt string `json:"contractExpiresAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

// ProviderCapacitySuggestions carries the observed-value prefill.
type ProviderCapacitySuggestions struct {
	RPM           int `json:"rpm"`
	TPM           int `json:"tpm"`
	Concurrency   int `json:"concurrency"`
	DailyRequests int `json:"dailyRequests"`
	TokenQuota    int `json:"tokenQuota"`
}

// capacityLimitsFrom maps the stored policy onto the response shape.
func capacityLimitsFrom(c db.ProviderCapacity) ProviderCapacityLimits {
	return ProviderCapacityLimits{
		RPM:               c.RPM,
		TPM:               c.TPM,
		Concurrency:       c.Concurrency,
		DailyRequests:     c.DailyRequests,
		QueueTimeoutMs:    c.QueueTimeoutMs,
		TokenQuota:        c.TokenQuota,
		ContractExpiresAt: c.ContractExpiresAt,
		UpdatedAt:         c.UpdatedAt,
	}
}

// suggestCapacity derives prefill values from the observations, each axis taking
// the largest value the provider named in that unit.
//
// Only units that map onto a field are used: an rpm/tpm/concurrent observation maps
// directly, a daily request count maps onto the daily budget, and a bare
// "requests"/"tokens" count (which is a window or lifetime figure, not a per-minute
// one) is surfaced in the list but never prefill a per-minute field it does not mean.
func suggestCapacity(observations []db.ProviderObservation) ProviderCapacitySuggestions {
	var s ProviderCapacitySuggestions
	for _, o := range observations {
		if o.LimitValue <= 0 {
			continue
		}
		switch o.LimitUnit {
		case "rpm":
			s.RPM = max(s.RPM, o.LimitValue)
		case "tpm":
			s.TPM = max(s.TPM, o.LimitValue)
		case "concurrent":
			s.Concurrency = max(s.Concurrency, o.LimitValue)
		case "rpd":
			s.DailyRequests = max(s.DailyRequests, o.LimitValue)
		}
	}
	return s
}

// requireIntegratedProvider answers 404 unless the id names a catalog provider.
// The two id spaces cannot collide (a custom node id always carries a
// "<literal>-<suffix>" shape), so this is exact rather than heuristic.
func requireIntegratedProvider(w http.ResponseWriter, providerID string) (string, bool) {
	canon := providers.ResolveAlias(providerID)
	if _, known := providers.KnownProviders[canon]; !known {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "unknown integrated provider")
		return "", false
	}
	return canon, true
}

// HandleGetProviderCapacity handles GET /api/providers/{id}/capacity.
func (h *DashboardHandler) HandleGetProviderCapacity(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing provider id")
		return
	}
	canon, ok := requireIntegratedProvider(w, id)
	if !ok {
		return
	}
	res, err := h.providerCapacityResponse(r.Context(), canon)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, res)
}

// providerCapacityResponse assembles the policy, the observations and the
// suggestions from one read each.
func (h *DashboardHandler) providerCapacityResponse(ctx context.Context, providerID string) (ProviderCapacityResponse, error) {
	limits, err := h.Repo.GetProviderCapacity(ctx, providerID)
	if err != nil {
		return ProviderCapacityResponse{}, err
	}
	observations, err := h.Repo.ListProviderObservations(ctx, providerID)
	if err != nil {
		return ProviderCapacityResponse{}, err
	}
	return ProviderCapacityResponse{
		Provider:     providerID,
		Limits:       capacityLimitsFrom(limits),
		Observations: observations,
		Suggestions:  suggestCapacity(observations),
		DailyUsedWindow: func() int {
			if h.Limiter == nil {
				return 0
			}
			return h.Limiter.SnapshotDaily(middleware.ProviderScopeKey(providerID))
		}(),
	}, nil
}

// HandleUpdateProviderCapacity handles PUT /api/providers/{id}/capacity.
//
// The PUT merges instead of replacing, exactly like the node editor: an omitted
// number keeps its stored value, an explicit 0 clears it, an explicit number sets
// it. A provider that is not in the catalog is a 404, not a silent write to a table
// no reader consults.
func (h *DashboardHandler) HandleUpdateProviderCapacity(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing provider id")
		return
	}
	canon, ok := requireIntegratedProvider(w, id)
	if !ok {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer func() { _ = r.Body.Close() }()

	var patch struct {
		RPM               *int    `json:"rpm"`
		TPM               *int    `json:"tpm"`
		Concurrency       *int    `json:"concurrency"`
		DailyRequests     *int    `json:"dailyRequests"`
		TokenQuota        *int    `json:"tokenQuota"`
		QueueTimeoutMs    *int    `json:"queueTimeoutMs"`
		ContractExpiresAt *string `json:"contractExpiresAt"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &patch); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}

	ctx := r.Context()
	merged, err := h.Repo.GetProviderCapacity(ctx, canon)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pairs := []struct {
		field  string
		patch  *int
		target *int
	}{
		{"rpm", patch.RPM, &merged.RPM},
		{"tpm", patch.TPM, &merged.TPM},
		{"concurrency", patch.Concurrency, &merged.Concurrency},
		{"dailyRequests", patch.DailyRequests, &merged.DailyRequests},
		{"tokenQuota", patch.TokenQuota, &merged.TokenQuota},
		{"queueTimeoutMs", patch.QueueTimeoutMs, &merged.QueueTimeoutMs},
	}
	for _, p := range pairs {
		v, err := mergeLimit(p.patch, p.target)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, p.field+": "+err.Error())
			return
		}
		*p.target = v
	}
	if patch.ContractExpiresAt != nil {
		expires, err := normalizeContractExpiry(*patch.ContractExpiresAt)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "contractExpiresAt: "+err.Error())
			return
		}
		merged.ContractExpiresAt = expires
	}

	if err := h.Repo.SetProviderCapacity(ctx, canon, merged); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := h.providerCapacityResponse(ctx, canon)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, res)
}
