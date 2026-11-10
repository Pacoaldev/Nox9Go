package db

import (
	"context"
	"fmt"
)

// Per-connection usage summary for the quota tracker.
//
// Most catalog providers have no metered quota endpoint, so their tracker card
// had nothing to draw. The gateway does own one honest number about every
// connection: what it routed through it. This reader is that number, and it is
// deliberately a summary (requests, tokens, last request) rather than a quota —
// nothing here is a provider allowance, so nothing here may be rendered as a
// percentage of one.

// ConnectionUsageSummary is the gateway-recorded traffic of one connection over
// the requested window.
type ConnectionUsageSummary struct {
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
	LastUsed string `json:"lastUsed,omitempty"`
}

// connectionUsageSummaryQuery is a named constant so the shape stays visible to
// the query-plan review: it is one indexed seek on idx_uh_conn, it never scans
// the ledger by provider, and it sums in the database rather than shipping rows.
const connectionUsageSummaryQuery = `
	SELECT COUNT(*),
	       COALESCE(SUM(COALESCE(promptTokens, 0) + COALESCE(completionTokens, 0)), 0),
	       COALESCE(MAX(timestamp), '')
	  FROM usageHistory
	 WHERE connectionId = ? AND timestamp >= ?`

// GetConnectionUsageSummary returns what this gateway routed through one
// connection since `since` (RFC3339 UTC, the format usageHistory stores).
//
// An empty connection id, a nil repo, or a window that matches no row all answer
// the zero summary rather than an error: this read decorates a card, and a
// decorate-only read must never turn into a failure the operator has to read.
func (r *Repo) GetConnectionUsageSummary(ctx context.Context, connectionID, since string) (ConnectionUsageSummary, error) {
	var out ConnectionUsageSummary
	if r == nil || r.db == nil || connectionID == "" {
		return out, nil
	}
	err := r.db.QueryRowContext(ctx, connectionUsageSummaryQuery, connectionID, since).
		Scan(&out.Requests, &out.Tokens, &out.LastUsed)
	if err != nil {
		return ConnectionUsageSummary{}, fmt.Errorf("get connection usage summary %s: %w", connectionID, err)
	}
	return out, nil
}
