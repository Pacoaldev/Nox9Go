package db

import (
	"context"
	"testing"
	"unicode/utf8"
)

// Provider observations carry upstream text, and an upstream that answers in a
// multibyte script can hand back a body that was cut mid-rune by an earlier
// writer. SQLite stores bytes, so such a row reads back fine from the database
// and only fails later: encoding/json refuses invalid UTF-8, and the capacity
// endpoint answered 500 (then, worse, a 200 with a body truncated at the bad byte
// and the error object concatenated after it). These tests pin that a row with
// invalid evidence still reads, with the text repaired instead of the whole list
// lost.

func TestListProviderObservations_RepairsInvalidUTF8Evidence(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := context.Background()

	// "免费模型" is three-byte UTF-8; cutting after the first byte of "免" leaves a
	// lone lead byte, which is exactly the shape a mid-rune truncation produces.
	valid := "You have reached the daily limit. 免费模型每日 60 次"
	broken := valid[:len(valid)-len("免费模型每日 60 次")] + "\xe5"

	// Store the broken value the way a legacy writer did: raw bytes, bypassing
	// RecordProviderObservation so its own bounding is not what is under test.
	if _, err := repo.db.ExecContext(ctx, `
		INSERT INTO providerObservations
			(provider, model, signal, statusCode, limitValue, limitUnit, evidence, sampleCount, firstSeen, lastSeen)
		VALUES ('bazaarlink', '', 'quota_exceeded', 429, 60, 'rpd', ?, 1,
		        '2026-11-06T14:22:39Z', '2026-11-06T14:22:39Z')`, broken); err != nil {
		t.Fatalf("seed invalid row: %v", err)
	}

	got, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations returned an error for a row with invalid UTF-8: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("observations = %d, want 1", len(got))
	}
	if !utf8.ValidString(got[0].Evidence) {
		t.Errorf("evidence is still not valid UTF-8: %q", got[0].Evidence)
	}
	if got[0].LimitValue != 60 || got[0].LimitUnit != "rpd" {
		t.Errorf("the fact was lost with the text: value=%d unit=%q", got[0].LimitValue, got[0].LimitUnit)
	}
	// The readable prefix is kept, not discarded: the operator still learns why.
	if want := "You have reached the daily limit. "; len(got[0].Evidence) < len(want) {
		t.Errorf("evidence = %q, want at least the readable prefix %q", got[0].Evidence, want)
	}
}

// A clean row must come back byte-identical: the repair must be a no-op for the
// overwhelming majority of observations.
func TestListProviderObservations_LeavesValidEvidenceUntouched(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := context.Background()

	const clean = "Rate limit exceeded: free-models-per-day. 免费模型每日 60 次"
	if _, err := repo.db.ExecContext(ctx, `
		INSERT INTO providerObservations
			(provider, model, signal, statusCode, limitValue, limitUnit, evidence, sampleCount, firstSeen, lastSeen)
		VALUES ('bazaarlink', '', 'quota_exceeded', 429, 60, 'rpd', ?, 1,
		        '2026-11-06T14:22:39Z', '2026-11-06T14:22:39Z')`, clean); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	got, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("observations = %d, want 1", len(got))
	}
	if got[0].Evidence != clean {
		t.Errorf("valid evidence was altered:\n got %q\nwant %q", got[0].Evidence, clean)
	}
}

// repairUTF8 is the helper the read path uses. It is tested directly because the
// database round-trip above cannot express every shape.
func TestRepairUTF8(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"ascii untouched", "limit reached", "limit reached"},
		{"valid multibyte untouched", "免费模型", "免费模型"},
		{"lone lead byte at end is dropped", "abc\xe5", "abc"},
		{"truncated 2-byte rune is dropped", "abc\xd6", "abc"},
		{"truncated 3-byte rune keeps the prefix", "abc\xe5\x85", "abc"},
		{"invalid byte in the middle cuts the tail", "abc\xe5def", "abc"},
		{"already valid, mixed scripts", "ok 免费 60", "ok 免费 60"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repairUTF8(tt.input); got != tt.want {
				t.Errorf("repairUTF8(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
