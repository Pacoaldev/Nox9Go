package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Capacity for INTEGRATED providers (docs/provider-capacity-learning).
//
// A custom provider's capacity lives on its providerNodes row (provider_limits.go).
// An integrated provider has no such row: it is a catalog entry, and every
// connection of it shares the same upstream. This file gives an integrated
// provider the same two things a custom node has:
//
//   - providerCapacity, the operator's configured policy, so the existing provider
//     limiter can bound an integrated provider exactly like a node; and
//   - providerObservations, a small per-provider knowledge base of the capacity
//     facts the upstream itself reports at runtime (rate limits, quota refusals,
//     capacity errors), which the dashboard uses to prefill the editor.
//
// Both are Go-only, additive tables: upstream's Next.js application declares
// neither and ignores them, exactly like providerModelLocks and the ledger tables.
//
// 0 means unlimited and "" means no expiry, the same convention apiKeys and
// providerNodes already use.

// ProviderCapacity is the capacity policy of one integrated provider.
type ProviderCapacity struct {
	RPM               int
	TPM               int
	Concurrency       int
	// DailyRequests caps the requests the provider may spend per UTC day. It is a
	// fixed-window budget, not a sliding one: it refills at midnight UTC, the
	// boundary a provider that publishes a daily allowance names. 0 = unlimited.
	DailyRequests     int
	QueueTimeoutMs    int
	TokenQuota        int
	ContractExpiresAt string
	// UpdatedAt is RFC3339 UTC, or "" when the row has never been written.
	UpdatedAt string
}

// Expired reports whether the provider's contract had run out by now. It mirrors
// ProviderLimits.Expired, including the "unparseable reads as not expired" rule.
func (c ProviderCapacity) Expired(now time.Time) bool {
	if c.ContractExpiresAt == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, c.ContractExpiresAt)
	if err != nil {
		return false
	}
	return now.After(until)
}

// ProviderObservation is one learned capacity fact about an integrated provider.
//
// It is a fact the upstream stated, not a policy: Signal says what kind, LimitValue
// and LimitUnit carry a number only when one was parseable, and Evidence is a
// bounded snippet kept for the operator to read. SampleCount and the two timestamps
// let the dashboard show how strongly the fact is supported.
type ProviderObservation struct {
	Provider    string `json:"provider"`
	Model       string `json:"model,omitempty"`
	Signal      string `json:"signal"`
	StatusCode  int    `json:"statusCode,omitempty"`
	LimitValue  int    `json:"limitValue,omitempty"`
	LimitUnit   string `json:"limitUnit,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	SampleCount int    `json:"sampleCount"`
	FirstSeen   string `json:"firstSeen"`
	LastSeen    string `json:"lastSeen"`
}

// providerCapacityColumns is the shared column list for a capacity read.
const providerCapacityColumns = `rpm, tpm, concurrency, dailyRequests, queueTimeoutMs, tokenQuota, COALESCE(contractExpiresAt, ''), COALESCE(updatedAt, '')`

// GetProviderCapacity reads an integrated provider's policy. An unknown provider
// answers the zero value, which the caller reads as "unlimited" — the same shape
// GetProviderLimits gives a node with no row.
func (r *Repo) GetProviderCapacity(ctx context.Context, providerID string) (ProviderCapacity, error) {
	var c ProviderCapacity
	if r == nil || r.db == nil || providerID == "" {
		return c, nil
	}
	err := r.db.QueryRowContext(ctx,
		`SELECT `+providerCapacityColumns+` FROM providerCapacity WHERE provider = ? LIMIT 1`, providerID,
	).Scan(&c.RPM, &c.TPM, &c.Concurrency, &c.DailyRequests, &c.QueueTimeoutMs, &c.TokenQuota, &c.ContractExpiresAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderCapacity{}, nil
	}
	if err != nil {
		return ProviderCapacity{}, fmt.Errorf("get provider capacity %s: %w", providerID, err)
	}
	return c, nil
}

// SetProviderCapacity writes the whole policy of one integrated provider.
//
// It is a total write with the same validation as SetProviderLimits: numbers must
// not be negative (the column has no CHECK constraint), and a contract timestamp is
// normalised to RFC3339 UTC or refused. Unlike a node, there is no row to UPDATE,
// so this is an upsert keyed by the provider id.
func (r *Repo) SetProviderCapacity(ctx context.Context, providerID string, c ProviderCapacity) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("set provider capacity: nil repo")
	}
	if strings.TrimSpace(providerID) == "" {
		return fmt.Errorf("set provider capacity: empty provider id")
	}
	if c.RPM < 0 || c.TPM < 0 || c.Concurrency < 0 || c.DailyRequests < 0 || c.TokenQuota < 0 || c.QueueTimeoutMs < 0 {
		return fmt.Errorf("set provider capacity %s: limits are counts and cannot be negative", providerID)
	}
	expiresAt := strings.TrimSpace(c.ContractExpiresAt)
	if expiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return fmt.Errorf("set provider capacity %s: contractExpiresAt must be RFC3339: %w", providerID, err)
		}
		expiresAt = parsed.UTC().Format(time.RFC3339)
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO providerCapacity
			(provider, rpm, tpm, concurrency, dailyRequests, queueTimeoutMs, tokenQuota, contractExpiresAt, updatedAt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET
			rpm = excluded.rpm,
			tpm = excluded.tpm,
			concurrency = excluded.concurrency,
			dailyRequests = excluded.dailyRequests,
			queueTimeoutMs = excluded.queueTimeoutMs,
			tokenQuota = excluded.tokenQuota,
			contractExpiresAt = excluded.contractExpiresAt,
			updatedAt = excluded.updatedAt`,
		providerID, c.RPM, c.TPM, c.Concurrency, c.DailyRequests, c.QueueTimeoutMs, c.TokenQuota, expiresAt, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("set provider capacity %s: %w", providerID, err)
	}
	return nil
}

// RecordProviderObservation upserts one learned capacity fact and returns it with
// its refreshed counters.
//
// The row is keyed by (provider, model, signal, limitUnit, limitValue): a repeated
// fact is a sample of the same fact, so it bumps SampleCount and LastSeen instead of
// growing the table without bound. A fact the upstream states with a new number (a
// different limit) lands as its own row, which is the point — the dashboard shows
// every distinct limit the upstream has named.
//
// Empty evidence is not written over a stored one, so a later sample that carried no
// readable body does not erase the snippet that explains the row.
func (r *Repo) RecordProviderObservation(ctx context.Context, obs ProviderObservation) (ProviderObservation, error) {
	if r == nil || r.db == nil {
		return obs, nil
	}
	provider := strings.TrimSpace(obs.Provider)
	if provider == "" || strings.TrimSpace(obs.Signal) == "" {
		return obs, nil
	}
	model := strings.TrimSpace(obs.Model)
	unit := strings.TrimSpace(obs.LimitUnit)
	evidence := boundEvidence(obs.Evidence)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO providerObservations
			(provider, model, signal, statusCode, limitValue, limitUnit, evidence, sampleCount, firstSeen, lastSeen)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(provider, model, signal, limitUnit, limitValue) DO UPDATE SET
			sampleCount = sampleCount + 1,
			lastSeen = excluded.lastSeen,
			statusCode = excluded.statusCode,
			evidence = CASE WHEN excluded.evidence != '' THEN excluded.evidence ELSE evidence END`,
		provider, model, obs.Signal, obs.StatusCode, obs.LimitValue, unit, evidence, now, now,
	)
	if err != nil {
		return obs, fmt.Errorf("record provider observation %s: %w", provider, err)
	}
	obs.Provider = provider
	obs.Model = model
	obs.LimitUnit = unit
	obs.Evidence = evidence
	obs.SampleCount = 1
	obs.FirstSeen = now
	obs.LastSeen = now
	return obs, nil
}

// ListProviderObservations returns every learned fact for one provider, most
// recently seen first, so the dashboard can render a per-provider view from one
// read.
func (r *Repo) ListProviderObservations(ctx context.Context, providerID string) ([]ProviderObservation, error) {
	if r == nil || r.db == nil || strings.TrimSpace(providerID) == "" {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider, model, signal, statusCode, limitValue, limitUnit,
		       evidence, sampleCount, firstSeen, lastSeen
		  FROM providerObservations
		 WHERE provider = ?
		 ORDER BY lastSeen DESC, signal ASC`, providerID)
	if err != nil {
		return nil, fmt.Errorf("list provider observations %s: %w", providerID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []ProviderObservation{}
	for rows.Next() {
		var o ProviderObservation
		if err := rows.Scan(&o.Provider, &o.Model, &o.Signal, &o.StatusCode, &o.LimitValue,
			&o.LimitUnit, &o.Evidence, &o.SampleCount, &o.FirstSeen, &o.LastSeen); err != nil {
			return nil, fmt.Errorf("scan provider observation: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider observations: %w", err)
	}
	return out, nil
}

// maxEvidenceLen bounds a stored snippet so one verbose upstream body cannot bloat
// the table or the dashboard payload.
const maxEvidenceLen = 240

// boundEvidence trims a snippet to maxEvidenceLen bytes, cutting on a character
// boundary and collapsing whitespace so the stored text is one readable line.
func boundEvidence(raw string) string {
	s := strings.Join(strings.Fields(raw), " ")
	if len(s) <= maxEvidenceLen {
		return s
	}
	cut := s[:maxEvidenceLen]
	// Do not split a UTF-8 rune.
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut)
}
