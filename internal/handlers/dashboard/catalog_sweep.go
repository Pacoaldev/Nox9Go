// The periodic catalogue sweep: it re-reads the upstream catalogue of every
// provider that publishes one and records what it found, so the dashboard can
// show which models a provider no longer lists.
//
// Two rules hold everywhere in this file:
//
//   - A model is only marked deprecated from explicit upstream evidence (an
//     HTTP 410 naming it). A model the catalogue omits is reported as "no
//     longer listed" and nothing else: absence is weak evidence.
//   - Only providers with a queryable catalogue are swept; the rest are
//     skipped, which is a property of the provider rather than a failure.
package dashboard

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"sort"
	"strings"
	"time"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/models"
	"nox9go/proxy/internal/providers"
)

// Settings key, KV scope and bounds of the catalogue sweep.
const (
	// CatalogSweepSettingsKey is the settings key holding the sweep configuration.
	CatalogSweepSettingsKey = "modelSweep"
	// catalogSweepScope is the KV scope the sweep results are written to, one key per provider.
	catalogSweepScope = "modelSweep"
	// DefaultCatalogSweepIntervalMin is the pause between passes: twelve hours.
	DefaultCatalogSweepIntervalMin = 720
	// MinCatalogSweepIntervalMin and MaxCatalogSweepIntervalMin bound the
	// interval a stored configuration may ask for: a quarter of an hour to a week.
	MinCatalogSweepIntervalMin = 15
	MaxCatalogSweepIntervalMin = 10080
	// catalogSweepIdleWait is how long the loop sleeps while the sweep is off.
	catalogSweepIdleWait = 60 * time.Second
)

// CatalogSweepEntry is what one provider's catalogue looked like at the time
// of the last sweep. CheckedAt is the RFC3339 UTC timestamp, Listed the number
// of models the catalogue published, and Revived the number of recorded
// deprecations the catalogue proved wrong.
//
// Unlisted holds the provider's configured models the catalogue no longer
// lists: it is informational, and nothing in it is ever marked deprecated.
// Skipped marks a provider that exposes no queryable catalogue, and Error
// holds why a catalogue could not be read.
type CatalogSweepEntry struct {
	Provider  string   `json:"provider"`
	CheckedAt string   `json:"checkedAt"`
	Listed    int      `json:"listed"`
	Revived   int      `json:"revived"`
	Unlisted  []string `json:"unlisted,omitempty"`
	Skipped   bool     `json:"skipped,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// CatalogSweepConfig is the parsed modelSweep settings value.
type CatalogSweepConfig struct {
	Enabled     bool `json:"enabled"`
	IntervalMin int  `json:"intervalMin"`
}

// ParseCatalogSweepConfig reads the settings value leniently.
//
// Anything unreadable yields the default, which is on: the sweep never writes
// a deprecation on its own, so running it is the safe side of the failure.
func ParseCatalogSweepConfig(value any) CatalogSweepConfig {
	cfg := CatalogSweepConfig{Enabled: true, IntervalMin: DefaultCatalogSweepIntervalMin}
	obj, ok := value.(map[string]any)
	if !ok {
		return cfg
	}
	if enabled, ok := obj["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if minutes, ok := sweepIntervalMinutes(obj["intervalMin"]); ok {
		cfg.IntervalMin = minutes
	}
	return cfg
}

// sweepIntervalMinutes reads an interval a stored JSON number or a plain
// integer may carry, clamped to the bounds the loop accepts.
func sweepIntervalMinutes(value any) (int, bool) {
	var minutes int
	switch v := value.(type) {
	case float64:
		minutes = int(v)
	case int:
		minutes = v
	case int64:
		minutes = int(v)
	default:
		return 0, false
	}
	return min(max(minutes, MinCatalogSweepIntervalMin), MaxCatalogSweepIntervalMin), true
}

// catalogSweepConfig reads the sweep configuration from the settings row.
func (h *DashboardHandler) catalogSweepConfig() CatalogSweepConfig {
	raw, err := h.Repo.GetSettingsRaw()
	if err != nil {
		log.Warn("model-sweep", "read settings", "error", err)
		return ParseCatalogSweepConfig(nil)
	}
	return ParseCatalogSweepConfig(raw[CatalogSweepSettingsKey])
}

// RunCatalogSweep sweeps every active connection and records the result.
// providerFilter narrows the sweep to one provider; "" sweeps them all. The
// entries are returned sorted by provider id.
func (h *DashboardHandler) RunCatalogSweep(ctx context.Context, providerFilter string) []CatalogSweepEntry {
	conns, err := h.Repo.GetProviderConnections(providerFilter, true)
	if err != nil {
		log.Warn("model-sweep", "read connections", "error", err)
		return nil
	}

	accumulators := map[string]*sweepAccumulator{}
	for _, conn := range conns {
		if conn == nil || ctx.Err() != nil {
			continue
		}
		key := strings.ToLower(conn.Provider)
		acc := accumulators[key]
		if acc == nil {
			acc = &sweepAccumulator{provider: conn.Provider}
			accumulators[key] = acc
		}
		acc.add(h, ctx, conn)
	}

	entries := make([]CatalogSweepEntry, 0, len(accumulators))
	for _, acc := range accumulators {
		entry := acc.entry(h)
		h.persistSweepEntry(entry)
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Provider < entries[j].Provider })
	return entries
}

// persistSweepEntry writes one provider's entry to the KV scope.
func (h *DashboardHandler) persistSweepEntry(entry CatalogSweepEntry) {
	raw, err := json.Marshal(entry)
	if err != nil {
		log.Warn("model-sweep", "encode entry", "provider", entry.Provider, "error", err)
		return
	}
	if err := h.Repo.SetKV(catalogSweepScope, strings.ToLower(entry.Provider), string(raw)); err != nil {
		log.Warn("model-sweep", "write entry", "provider", entry.Provider, "error", err)
	}
}

// LoadCatalogSweep returns the last recorded sweep, keyed by provider id.
func (h *DashboardHandler) LoadCatalogSweep() map[string]CatalogSweepEntry {
	out := map[string]CatalogSweepEntry{}
	all, err := h.Repo.GetKVScope(catalogSweepScope)
	if err != nil {
		log.Warn("model-sweep", "read entries", "error", err)
		return out
	}
	for key, raw := range all {
		var entry CatalogSweepEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			continue
		}
		out[key] = entry
	}
	return out
}

// HandleGetCatalogSweep handles GET /api/models/sweep. It answers with the
// recorded sweep and the configuration behind it, without touching upstream.
func (h *DashboardHandler) HandleGetCatalogSweep(w http.ResponseWriter, _ *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"config":    h.catalogSweepConfig(),
		"providers": h.LoadCatalogSweep(),
	})
}

// HandleRunCatalogSweep handles POST /api/models/sweep. It runs the sweep now
// and answers with the same shape as the GET; the provider query parameter
// narrows the sweep to one provider. A sweep that failed for every provider it
// touched answers 502: the operator asked for a reading and got none.
func (h *DashboardHandler) HandleRunCatalogSweep(w http.ResponseWriter, r *http.Request) {
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	entries := h.RunCatalogSweep(r.Context(), provider)
	if len(entries) == 0 {
		// A sweep that produced no entry either had nothing to sweep or could
		// not read the connections at all. The second is a gateway fault, not
		// an empty catalogue.
		if _, err := h.Repo.GetProviderConnections(provider, true); err != nil {
			handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	synced, failed, skipped := 0, 0, 0
	lastError := ""
	for _, entry := range entries {
		switch {
		case entry.Error != "":
			failed++
			lastError = entry.Error
		case entry.Skipped:
			skipped++
		default:
			synced++
		}
	}
	if synced == 0 && skipped == 0 && failed > 0 {
		handlerutil.WriteJSONError(w, http.StatusBadGateway, "model sweep failed: "+lastError)
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"config":    h.catalogSweepConfig(),
		"providers": h.LoadCatalogSweep(),
	})
}

// StartCatalogSweep runs the periodic sweep until ctx ends. The configuration
// is read on every cycle, so switching it on or off takes effect without a restart.
func StartCatalogSweep(ctx context.Context, repo *db.Repo) {
	if repo == nil {
		return
	}
	h := NewDashboardHandler(repo)
	for {
		cfg := h.catalogSweepConfig()
		wait := catalogSweepIdleWait
		if cfg.Enabled {
			entries := h.RunCatalogSweep(ctx, "")
			log.Info("model-sweep", "sweep finished", "providers", len(entries))
			wait = time.Duration(cfg.IntervalMin) * time.Minute
		} else {
			log.Info("model-sweep", "disabled, waiting", "wait", catalogSweepIdleWait)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// sweepAccumulator collects the result of every connection of one provider.
type sweepAccumulator struct {
	provider string
	live     map[string]bool
	revived  int
	skipped  int
	errors   []string
}

// add sweeps one connection and folds its result into the accumulator.
func (a *sweepAccumulator) add(h *DashboardHandler, ctx context.Context, conn *models.ProviderConnection) {
	live, fetchErr, listable := h.fetchLiveModelIDs(ctx, conn)
	if !listable {
		// The provider has no catalogue endpoint at all. That is a property of
		// the provider, not a failed sweep.
		a.skipped++
		return
	}
	if fetchErr != nil {
		a.errors = append(a.errors, fetchErr.Error())
		log.Warn("model-sweep", "catalogue unreachable", "provider", conn.Provider, "conn", conn.ID, "error", fetchErr)
		return
	}
	cleared, clearErr := h.Repo.RetainLiveModels(conn.Provider, live)
	if clearErr != nil {
		a.errors = append(a.errors, clearErr.Error())
		log.Warn("model-sweep", "clear deprecations", "provider", conn.Provider, "error", clearErr)
		return
	}
	a.revived += cleared
	if a.live == nil {
		a.live = make(map[string]bool, len(live))
	}
	for key := range live {
		a.live[key] = true
	}
	log.Info("model-sweep", "catalogue refreshed", "provider", conn.Provider, "models", len(live), "revived", cleared)
}

// entry turns the accumulated result into the recorded entry. A failure wins
// over a skip: a catalogue that could not be read is not an empty catalogue.
func (a *sweepAccumulator) entry(h *DashboardHandler) CatalogSweepEntry {
	entry := CatalogSweepEntry{
		Provider:  a.provider,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
	switch {
	case len(a.errors) > 0:
		entry.Error = a.errors[0]
		return entry
	case a.skipped > 0 && len(a.live) == 0:
		entry.Skipped = true
		return entry
	}
	entry.Listed = len(a.live)
	entry.Revived = a.revived
	entry.Unlisted = h.unlistedModels(a.provider, a.live)
	return entry
}

// unlistedModels returns the provider's configured models the catalogue does
// not list. The list is informational and never writes a deprecation.
func (h *DashboardHandler) unlistedModels(provider string, live map[string]bool) []string {
	configured, err := h.configuredModels(provider)
	if err != nil {
		log.Warn("model-sweep", "read configured models", "provider", provider, "error", err)
		return nil
	}
	if len(configured) == 0 {
		return nil
	}
	out := make([]string, 0, len(configured))
	for _, id := range configured {
		if live[providers.DeprecationKey(provider, id)] {
			continue
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// configuredModels returns the models the operator configured for one
// provider: the custom models filed under it and the combo members that name
// it, keyed by deprecation key and carrying the id as the operator spelled it.
func (h *DashboardHandler) configuredModels(provider string) (map[string]string, error) {
	out := map[string]string{}

	customs, err := h.Repo.GetCustomModels()
	if err != nil {
		return nil, err
	}
	for _, cm := range customs {
		if cm == nil || cm.ID == "" || !sweepProviderMatches(cm.ProviderAlias, provider) {
			continue
		}
		id := strings.TrimSpace(cm.ID)
		out[providers.DeprecationKey(provider, id)] = id
	}

	combos, err := h.Repo.GetCombos()
	if err != nil {
		return nil, err
	}
	for _, combo := range combos {
		if combo == nil {
			continue
		}
		members, err := comboModels(combo.Models)
		if err != nil {
			log.Warn("model-sweep", "read combo members", "combo", combo.Name, "error", err)
			continue
		}
		for _, member := range members {
			entryProvider, model, ok := splitSweepEntry(member)
			if !ok || !sweepProviderMatches(entryProvider, provider) {
				continue
			}
			out[providers.DeprecationKey(provider, model)] = model
		}
	}
	return out, nil
}

// splitSweepEntry splits a "provider/model" combo member on its first slash.
func splitSweepEntry(entry string) (string, string, bool) {
	slash := strings.Index(entry, "/")
	if slash <= 0 || slash == len(entry)-1 {
		return "", "", false
	}
	return entry[:slash], entry[slash+1:], true
}

// sweepProviderMatches reports whether a configured provider names the swept
// one: combos and custom models publish the provider under its registry alias,
// while the connection carries the provider id, so both are accepted.
func sweepProviderMatches(candidate, provider string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	if strings.EqualFold(candidate, provider) {
		return true
	}
	return strings.EqualFold(candidate, providers.GetProviderAlias(provider))
}
