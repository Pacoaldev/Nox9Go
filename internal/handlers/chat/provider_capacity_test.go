package chat

import (
	"context"
	"net/http"
	"testing"

	"nox9go/proxy/internal/db"
)

func TestRecordProviderObservation_RecordsIntegratedProvider(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	ctx := context.Background()

	body := []byte(`{"error":{"message":"You have reached the daily limit of 60 free-model requests"}}`)
	handler.recordProviderObservation(ctx, "bazaarlink", "auto:free", http.StatusForbidden, body, nil)

	list, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("recorded %d observations, want 1", len(list))
	}
	if list[0].Signal != "quota_exceeded" || list[0].LimitValue != 60 || list[0].LimitUnit != "requests" {
		t.Errorf("observation = %+v, want quota_exceeded 60 requests", list[0])
	}
}

func TestRecordProviderObservation_IgnoresCustomNode(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	ctx := context.Background()
	// A custom node's provider string is its providerNodes id, not a catalog id.
	handler.recordProviderObservation(ctx, "openai-compatible-chat-nara", "glm-5.3",
		http.StatusTooManyRequests, []byte(`rate limit`), nil)

	list, err := repo.ListProviderObservations(ctx, "openai-compatible-chat-nara")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("a custom node was recorded: %+v", list)
	}
}

func TestRecordProviderObservation_IgnoresUnclassifiableFailure(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	ctx := context.Background()
	// A 404 (unknown model) says nothing about capacity.
	handler.recordProviderObservation(ctx, "bazaarlink", "auto:free", http.StatusNotFound, []byte(`not found`), nil)

	list, err := repo.ListProviderObservations(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("ListProviderObservations: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("an unclassifiable failure was recorded: %+v", list)
	}
}

func TestReadProviderPolicy_IntegratedReadsCapacity(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	ctx := context.Background()
	if err := repo.SetProviderCapacity(ctx, "bazaarlink", db.ProviderCapacity{RPM: 7, TPM: 900}); err != nil {
		t.Fatalf("SetProviderCapacity: %v", err)
	}
	got, err := handler.readProviderPolicy(ctx, "bazaarlink")
	if err != nil {
		t.Fatalf("readProviderPolicy: %v", err)
	}
	if got.RPM != 7 || got.TPM != 900 {
		t.Errorf("policy = %+v, want RPM 7 TPM 900", got)
	}
}

func TestReadProviderPolicy_CustomNodeReadsNodeRow(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	seedLimitedProviderNode(t, repo, "openai-compatible-chat-nara", db.ProviderLimits{RPM: 42})
	got, err := handler.readProviderPolicy(context.Background(), "openai-compatible-chat-nara")
	if err != nil {
		t.Fatalf("readProviderPolicy: %v", err)
	}
	if got.RPM != 42 {
		t.Errorf("policy = %+v, want RPM 42 from the node row", got)
	}
}

func TestAcquireProviderAttempt_EnforcesIntegratedCapacity(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	ctx := withProviderLimitState(context.Background())
	if err := repo.SetProviderCapacity(ctx, "bazaarlink", db.ProviderCapacity{RPM: 1}); err != nil {
		t.Fatalf("SetProviderCapacity: %v", err)
	}

	slot, err := handler.acquireProviderAttempt(ctx, "bazaarlink", "auto:free", "conn-1", []byte(`{"messages":[]}`))
	if slot == nil || err != nil {
		t.Fatalf("first attempt = slot %v err %v, want an admitted slot", slot, err)
	}
	slot.Release()

	_, err = handler.acquireProviderAttempt(ctx, "bazaarlink", "auto:free", "conn-1", []byte(`{"messages":[]}`))
	if err == nil {
		t.Fatal("second attempt was admitted while the integrated provider's window was full")
	}
}

func TestAcquireProviderAttempt_UnlimitedIntegratedProviderReturnsNoSlot(t *testing.T) {
	handler, _ := newProviderLimitHandler(t)
	ctx := withProviderLimitState(context.Background())
	slot, err := handler.acquireProviderAttempt(ctx, "bazaarlink", "auto:free", "conn-1", []byte(`{"messages":[]}`))
	if slot != nil || err != nil {
		t.Fatalf("unconfigured integrated provider = slot %v err %v, want nil/nil", slot, err)
	}
}
