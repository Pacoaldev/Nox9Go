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
	if list[0].Signal != "quota_exceeded" || list[0].LimitValue != 60 || list[0].LimitUnit != "rpd" {
		t.Errorf("observation = %+v, want quota_exceeded 60 req/day", list[0])
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

func TestAcquireProviderAttempt_EnforcesIntegratedDailyBudget(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	ctx := withProviderLimitState(context.Background())
	if err := repo.SetProviderCapacity(ctx, "bazaarlink", db.ProviderCapacity{DailyRequests: 2}); err != nil {
		t.Fatalf("SetProviderCapacity: %v", err)
	}

	for i := 0; i < 2; i++ {
		slot, err := handler.acquireProviderAttempt(ctx, "bazaarlink", "auto:free", "conn-1", []byte(`{"messages":[]}`))
		if slot == nil || err != nil {
			t.Fatalf("attempt %d = slot %v err %v, want admitted", i, slot, err)
		}
		slot.Release()
	}

	_, err := handler.acquireProviderAttempt(ctx, "bazaarlink", "auto:free", "conn-1", []byte(`{"messages":[]}`))
	if err == nil {
		t.Fatal("third attempt was admitted while the daily budget was exhausted")
	}
	refusal, ok := providerLimitRefusal(err)
	if !ok {
		t.Fatalf("err %v was not recognized as a providerLimitRefusal", err)
	}
	if refusal.Axis != "rpd" || refusal.Limit != 2 {
		t.Errorf("refusal = %+v, want axis rpd limit 2", refusal)
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

func TestRecordProviderObservation_CanceledContextAnd200Headers(t *testing.T) {
	handler, repo := newProviderLimitHandler(t)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	handler.recordProviderObservation(canceledCtx, "minimax-cn", "MiniMax-M2.7",
		http.StatusGatewayTimeout, []byte(`upstream timed out`), nil)

	obs, err := repo.ListProviderObservations(context.Background(), "minimax-cn")
	if err != nil || len(obs) != 1 {
		t.Fatalf("minimax-cn observations with canceled ctx = %+v, err %v; want 1", obs, err)
	}
	if obs[0].Signal != "timeout" {
		t.Errorf("signal = %q, want timeout", obs[0].Signal)
	}

	h := http.Header{}
	h.Set("x-ratelimit-limit-requests-minute", "60")
	h.Set("x-ratelimit-limit-tokens-minute", "150000")
	handler.recordProviderHeaderObservations(context.Background(), "groq", "llama-3.3-70b-versatile", http.StatusOK, h)

	groqObs, err := repo.ListProviderObservations(context.Background(), "groq")
	if err != nil || len(groqObs) != 2 {
		t.Fatalf("groq header observations = %+v, err %v; want 2", groqObs, err)
	}
}
