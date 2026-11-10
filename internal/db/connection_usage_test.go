package db

import (
	"context"
	"testing"
	"time"
)

// The quota tracker draws this summary on every card whose provider exposes no
// metered quota. These tests pin the window, the attribution and the empty case.

func TestGetConnectionUsageSummary_WindowAndAttribution(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	rows := []struct {
		conn      string
		at        time.Time
		prompt    int
		compl     int
		expectUse bool
	}{
		{"conn-a", now.Add(-2 * time.Hour), 100, 50, true},
		{"conn-a", now.Add(-48 * time.Hour), 10, 5, true},
		{"conn-a", now.Add(-40 * 24 * time.Hour), 1000, 1000, false},
		{"conn-b", now.Add(-1 * time.Hour), 7, 3, false},
	}
	for i, row := range rows {
		if _, err := repo.RawDB().Exec(
			`INSERT INTO usageHistory (timestamp, provider, model, connectionId, apiKey, clientKey, endpoint, promptTokens, completionTokens, cost, status, tokens, meta)
			 VALUES (?, 'someprovider', 'some-model', ?, 'k', 'c', '/v1/chat/completions', ?, ?, 0, 'success', '{}', '')`,
			row.at.Format(time.RFC3339), row.conn, row.prompt, row.compl,
		); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	since := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	got, err := repo.GetConnectionUsageSummary(context.Background(), "conn-a", since)
	if err != nil {
		t.Fatalf("GetConnectionUsageSummary: %v", err)
	}
	if got.Requests != 2 {
		t.Errorf("requests = %d, want 2 (the 40-day-old row is outside the window)", got.Requests)
	}
	if got.Tokens != 165 {
		t.Errorf("tokens = %d, want 165", got.Tokens)
	}
	if want := now.Add(-2 * time.Hour).Format(time.RFC3339); got.LastUsed != want {
		t.Errorf("lastUsed = %q, want %q", got.LastUsed, want)
	}
}

func TestGetConnectionUsageSummary_EmptyAnswersZero(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	got, err := repo.GetConnectionUsageSummary(context.Background(), "conn-none", "2020-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetConnectionUsageSummary: %v", err)
	}
	if got.Requests != 0 || got.Tokens != 0 || got.LastUsed != "" {
		t.Errorf("summary = %+v, want the zero value", got)
	}
	if _, err := repo.GetConnectionUsageSummary(context.Background(), "", ""); err != nil {
		t.Errorf("empty connection id must not error: %v", err)
	}
}
