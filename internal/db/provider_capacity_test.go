package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProviderCapacity_RoundTrip(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := context.Background()

	want := ProviderCapacity{
		RPM:               60,
		TPM:               90_000,
		Concurrency:       4,
		DailyRequests:     60,
		QueueTimeoutMs:    15_000,
		TokenQuota:        5_000_000,
		ContractExpiresAt: "2027-01-31T00:00:00Z",
	}
	if err := repo.SetProviderCapacity(ctx, "bazaarlink", want); err != nil {
		t.Fatalf("SetProviderCapacity: %v", err)
	}
	got, err := repo.GetProviderCapacity(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("GetProviderCapacity: %v", err)
	}
	if got.RPM != want.RPM || got.TPM != want.TPM || got.Concurrency != want.Concurrency ||
		got.DailyRequests != want.DailyRequests || got.QueueTimeoutMs != want.QueueTimeoutMs ||
		got.TokenQuota != want.TokenQuota || got.ContractExpiresAt != want.ContractExpiresAt {
		t.Errorf("GetProviderCapacity = %+v, want %+v", got, want)
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt was not stamped on a save")
	}
}

func TestGetProviderCapacity_UnknownIsUnlimited(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	got, err := repo.GetProviderCapacity(context.Background(), "never-configured")
	if err != nil {
		t.Fatalf("GetProviderCapacity: %v", err)
	}
	if got != (ProviderCapacity{}) {
		t.Errorf("capacity of an unknown provider = %+v, want the zero value", got)
	}
}

func TestSetProviderCapacity_RejectsNegativeCounts(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		cap  ProviderCapacity
	}{
		{"rpm", ProviderCapacity{RPM: -1}},
		{"tpm", ProviderCapacity{TPM: -1}},
		{"concurrency", ProviderCapacity{Concurrency: -1}},
		{"daily requests", ProviderCapacity{DailyRequests: -1}},
		{"token quota", ProviderCapacity{TokenQuota: -1}},
		{"queue timeout", ProviderCapacity{QueueTimeoutMs: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := openEphemeralSchemaDB(t)
			if err := repo.SetProviderCapacity(ctx, "bazaarlink", tt.cap); err == nil {
				t.Fatalf("SetProviderCapacity(%+v) accepted a negative count", tt.cap)
			}
			got, err := repo.GetProviderCapacity(ctx, "bazaarlink")
			if err != nil {
				t.Fatalf("GetProviderCapacity: %v", err)
			}
			if got != (ProviderCapacity{}) {
				t.Errorf("capacity after a refused write = %+v, want the zero value", got)
			}
		})
	}
}

func TestSetProviderCapacity_NormalisesContractExpiry(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "utc passes through", input: "2027-01-31T00:00:00Z", want: "2027-01-31T00:00:00Z"},
		{name: "offset is converted to utc", input: "2027-01-31T07:00:00+07:00", want: "2027-01-31T00:00:00Z"},
		{name: "empty means no expiry", input: "", want: ""},
		{name: "unparseable is refused", input: "31-01-2027", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := openEphemeralSchemaDB(t)
			ctx := context.Background()
			err := repo.SetProviderCapacity(ctx, "bazaarlink", ProviderCapacity{ContractExpiresAt: tt.input})
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetProviderCapacity(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got, err := repo.GetProviderCapacity(ctx, "bazaarlink")
			if err != nil {
				t.Fatalf("GetProviderCapacity: %v", err)
			}
			if got.ContractExpiresAt != tt.want {
				t.Errorf("stored contractExpiresAt = %q, want %q", got.ContractExpiresAt, tt.want)
			}
		})
	}
}

func TestProviderCapacity_Expired(t *testing.T) {
	now := time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		expiresAt string
		want      bool
	}{
		{name: "no expiry", expiresAt: "", want: false},
		{name: "future contract", expiresAt: "2027-02-01T00:00:00Z", want: false},
		{name: "past contract", expiresAt: "2027-01-01T00:00:00Z", want: true},
		{name: "unparseable reads as no expiry", expiresAt: "soon", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ProviderCapacity{ContractExpiresAt: tt.expiresAt}
			if got := c.Expired(now); got != tt.want {
				t.Errorf("Expired(%q) = %v, want %v", tt.expiresAt, got, tt.want)
			}
		})
	}
}

func TestRecordProviderObservation_UpsertsOnRepeat(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := context.Background()

	obs := ProviderObservation{
		Provider:   "bazaarlink",
		Model:      "auto:free",
		Signal:     "rate_limit",
		StatusCode: 429,
		LimitValue: 60,
		LimitUnit:  "requests",
		Evidence:   "You have reached the daily limit of 60 free-model requests",
	}
	for i := 0; i < 3; i++ {
		if _, err := repo.RecordProviderObservation(ctx, obs); err != nil {
			t.Fatalf("RecordProviderObservation #%d: %v", i, err)
		}
	}

	list, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d observations, want 1 (the same fact upserts)", len(list))
	}
	if list[0].SampleCount != 3 {
		t.Errorf("SampleCount = %d, want 3", list[0].SampleCount)
	}
	if list[0].LimitValue != 60 || list[0].LimitUnit != "requests" {
		t.Errorf("fact = %+v, want limit 60 requests", list[0])
	}
}

func TestRecordProviderObservation_DistinctLimitsAreDistinctRows(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := context.Background()
	for _, n := range []int{60, 200} {
		if _, err := repo.RecordProviderObservation(ctx, ProviderObservation{
			Provider: "bazaarlink", Signal: "rate_limit", LimitValue: n, LimitUnit: "requests",
		}); err != nil {
			t.Fatalf("RecordProviderObservation(%d): %v", n, err)
		}
	}
	list, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("listed %d observations, want 2 distinct limits", len(list))
	}
}

func TestRecordProviderObservation_KeepsEvidenceOnEmptyFollowup(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := context.Background()
	base := ProviderObservation{Provider: "bazaarlink", Signal: "rate_limit", LimitValue: 60, LimitUnit: "requests"}
	withEvidence := base
	withEvidence.Evidence = "daily limit of 60 requests"
	if _, err := repo.RecordProviderObservation(ctx, withEvidence); err != nil {
		t.Fatalf("RecordProviderObservation: %v", err)
	}
	if _, err := repo.RecordProviderObservation(ctx, base); err != nil {
		t.Fatalf("RecordProviderObservation (empty evidence): %v", err)
	}
	list, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(list) != 1 || list[0].Evidence == "" {
		t.Fatalf("evidence was erased by a later empty sample: %+v", list)
	}
}

func TestRecordProviderObservation_IgnoresRowWithoutProviderOrSignal(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	ctx := context.Background()
	for _, obs := range []ProviderObservation{
		{Signal: "rate_limit"},
		{Provider: "bazaarlink"},
	} {
		if _, err := repo.RecordProviderObservation(ctx, obs); err != nil {
			t.Fatalf("RecordProviderObservation(%+v) errored; it must fail quiet", obs)
		}
	}
	list, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("an incomplete observation was stored: %+v", list)
	}
}

func TestBoundEvidence_TrimsAndCollapsesWhitespace(t *testing.T) {
	long := strings.Repeat("x", 400)
	got := boundEvidence("  a\n\n  b  " + long)
	if strings.Contains(got, "\n") {
		t.Errorf("evidence kept a newline: %q", got)
	}
	if len(got) > maxEvidenceLen {
		t.Errorf("evidence length = %d, want <= %d", len(got), maxEvidenceLen)
	}
}
