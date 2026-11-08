package dashboard

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"testing"

	"nox9go/proxy/internal/db"
)

func TestProviderCapacity_UnknownProviderIs404(t *testing.T) {
	repo, cleanup := setupNodeTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	rec := nodeLimitsRequest(t, router, http.MethodGet, "/api/providers/not-a-provider/capacity", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown provider = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestProviderCapacity_PutThenGet(t *testing.T) {
	repo, cleanup := setupNodeTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	rec := nodeLimitsRequest(t, router, http.MethodPut, "/api/providers/bazaarlink/capacity",
		`{"rpm":60,"tpm":10000,"concurrency":2,"dailyRequests":60,"tokenQuota":5000,"queueTimeoutMs":1500,"contractExpiresAt":"2027-01-31"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	get := nodeLimitsRequest(t, router, http.MethodGet, "/api/providers/bazaarlink/capacity", "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (body: %s)", get.Code, get.Body.String())
	}
	var res ProviderCapacityResponse
	if err := json.Unmarshal(get.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Limits.RPM != 60 || res.Limits.TPM != 10000 || res.Limits.Concurrency != 2 {
		t.Errorf("limits = %+v, want rpm 60 tpm 10000 concurrency 2", res.Limits)
	}
	if res.Limits.DailyRequests != 60 {
		t.Errorf("dailyRequests = %d, want 60", res.Limits.DailyRequests)
	}
	if res.Limits.ContractExpiresAt != "2027-01-31T00:00:00Z" {
		t.Errorf("contractExpiresAt = %q, want the normalised UTC instant", res.Limits.ContractExpiresAt)
	}
}

func TestProviderCapacity_PutMergesPartialEdit(t *testing.T) {
	repo, cleanup := setupNodeTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	nodeLimitsRequest(t, router, http.MethodPut, "/api/providers/bazaarlink/capacity", `{"rpm":60,"tpm":9000}`)
	rec := nodeLimitsRequest(t, router, http.MethodPut, "/api/providers/bazaarlink/capacity", `{"concurrency":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res ProviderCapacityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Limits.RPM != 60 || res.Limits.TPM != 9000 || res.Limits.Concurrency != 4 {
		t.Errorf("merged limits = %+v, want the omitted fields to keep their values", res.Limits)
	}
}

func TestProviderCapacity_PutRefusesNegative(t *testing.T) {
	repo, cleanup := setupNodeTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	rec := nodeLimitsRequest(t, router, http.MethodPut, "/api/providers/bazaarlink/capacity", `{"rpm":-1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT negative rpm = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestProviderCapacity_GetReportsSuggestionsFromObservations(t *testing.T) {
	repo, cleanup := setupNodeTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	ctx := context.Background()
	for _, obs := range []db.ProviderObservation{
		{Provider: "bazaarlink", Signal: "rate_limit", LimitUnit: "rpm", LimitValue: 60},
		{Provider: "bazaarlink", Signal: "rate_limit", LimitUnit: "rpm", LimitValue: 120},
		{Provider: "bazaarlink", Signal: "rate_limit", LimitUnit: "tpm", LimitValue: 90000},
		// A daily allowance maps onto the daily budget field.
		{Provider: "bazaarlink", Signal: "quota_exceeded", LimitUnit: "rpd", LimitValue: 60},
		// A bare window count must not prefill a per-minute field.
		{Provider: "bazaarlink", Signal: "quota_exceeded", LimitUnit: "requests", LimitValue: 500},
	} {
		if _, err := repo.RecordProviderObservation(ctx, obs); err != nil {
			t.Fatalf("RecordProviderObservation: %v", err)
		}
	}

	rec := nodeLimitsRequest(t, router, http.MethodGet, "/api/providers/bazaarlink/capacity", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res ProviderCapacityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Suggestions.RPM != 120 {
		t.Errorf("suggested rpm = %d, want 120 (the largest observed)", res.Suggestions.RPM)
	}
	if res.Suggestions.TPM != 90000 {
		t.Errorf("suggested tpm = %d, want 90000", res.Suggestions.TPM)
	}
	if res.Suggestions.DailyRequests != 60 {
		t.Errorf("suggested dailyRequests = %d, want 60", res.Suggestions.DailyRequests)
	}
	if len(res.Observations) != 5 {
		t.Errorf("observations = %d rows, want 5", len(res.Observations))
	}
}
