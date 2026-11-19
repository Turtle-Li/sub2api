//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBatchImageSubmitRetryAndSettlement(t *testing.T) {
	const (
		model = "gpt-image-2.6-flare"
		rate  = `{"error":{"type":"requests","code":"rate_limit_exceeded"}}`
		busy  = `{"error":{"type":"service_unavailable_error","code":"server_is_overloaded"}}`
	)
	response := func(status int, body, retryAfter string) *http.Response {
		headers := http.Header{"Content-Type": {"application/json"}}
		if retryAfter != "" {
			headers.Set("Retry-After", retryAfter)
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
	}
	ok := func() *http.Response {
		return response(http.StatusOK, `{"data":[{"b64_json":"`+museTestPNG(t)+`","output_format":"png"}]}`, "")
	}
	interrupted := response(429, rate, "1")
	interrupted.Body = io.NopCloser(io.MultiReader(interrupted.Body, passthroughErrReadCloser{}))
	for _, tc := range []struct {
		name      string
		responses []*http.Response
		wantCalls int
		wantCost  float64
	}{
		{"recover on second retry", []*http.Response{response(429, rate, "1"), response(503, busy, ""), ok()}, 3, 0.25},
		{"retries exhausted", []*http.Response{response(429, rate, ""), response(429, rate, ""), response(429, rate, ""), ok()}, 3, 0},
		{"ambiguous relay failure", []*http.Response{response(500, busy, "1"), ok()}, 1, 0},
		{"interrupted error body", []*http.Response{interrupted, ok()}, 1, 0},
		{"spent quota", []*http.Response{response(429, `{"error":{"code":"insufficient_quota"}}`, "1"), ok()}, 1, 0},
		{"long provider delay", []*http.Response{response(429, rate, "120"), ok()}, 1, 0},
		{"not a rejection when output is present", []*http.Response{response(429, `{"data":[{}],"error":{"code":"rate_limit_exceeded"}}`, ""), ok()}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			upstream := &httpUpstreamRecorder{responses: tc.responses}
			cfg := &config.Config{BatchImage: config.BatchImageConfig{Enabled: true, MaxItemsPerJobDefault: 10, MaxPromptCharsPerItem: 100, OutputRetentionAfterTerminalHours: 360}}
			gateway := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			provider := NewOpenAIImagesBatchProvider(newFakeOpenAIImageBatchStore(), &GatewayOpenAIImageBatchItemExecutor{Gateway: gateway}, testOpenAIImageBatchObjectPrefix)
			registry := NewBatchImageProviderRegistry(provider)
			account := testBatchImageMappedAccount(101, AccountTypeAPIKey, map[string]any{model: model})
			account.Platform = PlatformOpenAI
			account.Credentials["base_url"] = "https://api.example.test/v1"
			repo := newFakeBatchImageRepository()
			billing := &fakeBatchImageBillingRepo{}
			pricing := &fakeBatchImagePricingResolver{unitPrice: 0.25}
			public := &BatchImagePublicService{Repo: repo, AccountRepo: &publicBatchImageAccountRepo{accounts: []Account{account}}, Queue: &publicBatchImageQueue{}, ProviderRegistry: registry, Pricing: pricing, BillingRepo: billing, Config: cfg}
			job, err := public.Submit(ctx, testBatchImageOwner(), BatchImageSubmitRequest{Model: model, Provider: BatchImageProviderOpenAI, ImageSize: "1K", ResponseMimeType: "image/png", AspectRatio: "1:1", Items: []BatchImageSubmitItem{{CustomID: "one", Prompt: "one blue square"}}}, "")
			require.NoError(t, err)
			pipeline := &BatchImagePipelineProcessor{
				ProviderProcessor: &BatchImageProviderProcessor{Repo: repo, ProviderRegistry: registry, AccountResolver: &fakeBatchImageAccountResolver{account: &account}, BillingRepo: billing},
				SettlementService: &BatchImageSettlementService{Repo: repo, Pricing: pricing, BillingRepo: billing, Config: cfg},
			}
			for range 4 {
				_, err := pipeline.Process(ctx, job.ID)
				require.NoError(t, err)
			}
			require.Len(t, upstream.requests, tc.wantCalls)
			for _, body := range upstream.bodies {
				require.Equal(t, upstream.bodies[0], body)
			}
			final := repo.jobs[job.ID]
			require.Equal(t, BatchImageJobStatusCompleted, final.Status)
			require.NotNil(t, final.SettledAt)
			require.NotNil(t, final.ActualCost)
			require.InDelta(t, tc.wantCost, *final.ActualCost, 1e-12)
			require.Len(t, billing.reserves, 1)
			require.Len(t, billing.captures, 1)
			require.InDelta(t, tc.wantCost, billing.captures[0].ActualAmount, 1e-12)
			if tc.wantCost > 0 {
				require.Equal(t, 1, final.SuccessCount)
			} else {
				require.Equal(t, 1, final.FailCount)
			}
		})
	}
}

type batchImageTestExecute func(context.Context, *Account, OpenAIImageBatchItemRequest, func() error) (*OpenAIImageBatchItemResult, error)

func (f batchImageTestExecute) Execute(ctx context.Context, account *Account, request OpenAIImageBatchItemRequest, before func() error) (*OpenAIImageBatchItemResult, error) {
	return f(ctx, account, request, before)
}

func TestBatchImageSubmitRetryChecksCancelAfterAdmission(t *testing.T) {
	ctx := t.Context()
	var calls, sends int
	var persistCancel func() error
	executor := batchImageTestExecute(func(_ context.Context, _ *Account, _ OpenAIImageBatchItemRequest, before func() error) (*OpenAIImageBatchItemResult, error) {
		calls++
		if calls == 2 {
			require.NoError(t, persistCancel()) // Cancel after re-admission, before a second POST.
		}
		if err := before(); err != nil {
			return nil, err
		}
		sends++
		return nil, &batchImageTemporarySubmitRejection{}
	})
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 102, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	job := &BatchImageJob{BatchID: "imgbatch_retry_cancel", ItemCount: 1}
	input := BatchImageInput{Model: "gpt-image-2.6-flare", Items: []BatchImageInputItem{{CustomID: "one", Prompt: "one blue square"}}}
	accepted, err := provider.Submit(ctx, job, account, input)
	require.NoError(t, err)
	job.ProviderJobName, job.ProviderInputRef = &accepted.ProviderJobName, &accepted.ProviderInputRef
	persistCancel = func() error { return provider.PersistCancelIntent(ctx, job) }

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateCancelled, status.InternalState)
	require.Equal(t, 2, calls)
	require.Equal(t, 1, sends)
	claimed, err := store.Exists(ctx, provider.itemAttemptKey(job.BatchID, 0))
	require.NoError(t, err)
	require.True(t, claimed)
}

func TestBatchImageSubmitRetryPreservesClaimAfterLeaseLoss(t *testing.T) {
	ctx, loseLease := context.WithCancel(t.Context())
	defer loseLease()
	var calls int
	executor := batchImageTestExecute(func(_ context.Context, _ *Account, _ OpenAIImageBatchItemRequest, before func() error) (*OpenAIImageBatchItemResult, error) {
		calls++
		require.NoError(t, before())
		loseLease()
		return nil, &batchImageTemporarySubmitRejection{}
	})
	provider := NewOpenAIImagesBatchProvider(newFakeOpenAIImageBatchStore(), executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 103, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	job := &BatchImageJob{BatchID: "imgbatch_retry_lease", ItemCount: 1}
	input := BatchImageInput{Model: "gpt-image-2.6-flare", Items: []BatchImageInputItem{{CustomID: "one", Prompt: "one blue square"}}}
	accepted, err := provider.Submit(ctx, job, account, input)
	require.NoError(t, err)
	job.ProviderJobName, job.ProviderInputRef = &accepted.ProviderJobName, &accepted.ProviderInputRef
	_, err = provider.Advance(ctx, job, account)
	require.ErrorIs(t, err, context.Canceled)
	resultExists, err := provider.store.Exists(t.Context(), provider.itemResultKey(job.BatchID, 0))
	require.NoError(t, err)
	require.False(t, resultExists)

	replacement := NewOpenAIImagesBatchProvider(provider.store, executor, testOpenAIImageBatchObjectPrefix)
	_, err = replacement.Advance(t.Context(), job, account)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestBatchImageSubmitRetryAfterAndDeadline(t *testing.T) {
	now := time.Date(2026, time.November, 19, 10, 20, 0, 0, time.UTC)
	headers := http.Header{"Retry-After": {"2", now.Add(4 * time.Second).Format(http.TimeFormat)}}
	require.Equal(t, 4*time.Second, batchImageRetryAfter(headers, now))
	delay, ok := batchImageSubmitRetryDelay(t.Context(), 0, &batchImageTemporarySubmitRejection{retryAfter: 4 * time.Second})
	require.True(t, ok)
	require.GreaterOrEqual(t, delay, 4*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, ok = batchImageSubmitRetryDelay(ctx, 0, &batchImageTemporarySubmitRejection{})
	require.False(t, ok)
	require.Greater(t, batchImageRetryAfter(http.Header{"Retry-After": {"9999999999999999999"}}, now), batchImageMaxSubmitRetryDelay)
}
