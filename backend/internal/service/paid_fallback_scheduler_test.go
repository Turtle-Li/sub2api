package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type paidFallbackAccountRepo struct {
	schedulerTestOpenAIAccountRepo
	listErr error
}

func (r paidFallbackAccountRepo) ListByGroup(_ context.Context, _ int64) ([]Account, error) {
	return r.accounts, r.listErr
}

func (r paidFallbackAccountRepo) ListModelAvailabilityCandidates(_ context.Context, _ *int64, _ []string, _ bool) ([]Account, error) {
	return r.accounts, r.listErr
}

type paidFallbackSchedulerSettings struct {
	SettingRepository
	mu     sync.RWMutex
	values map[string]string
}

func (r *paidFallbackSchedulerSettings) GetValue(_ context.Context, key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *paidFallbackSchedulerSettings) Get(ctx context.Context, key string) (*Setting, error) {
	value, err := r.GetValue(ctx, key)
	return &Setting{Key: key, Value: value}, err
}

func (r *paidFallbackSchedulerSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]string)
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *paidFallbackSchedulerSettings) setPaidPolicy(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[settingKeyPaidFallback] = value
}

func newPaidFallbackScheduler(t *testing.T, platform string, advanced bool) (*OpenAIGatewayService, context.Context, *paidFallbackSchedulerSettings) {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	mode := "false"
	if advanced {
		mode = "true"
	}
	policy, err := json.Marshal(PaidFallbackSettings{Enabled: true, GroupIDs: []int64{7}, APIKeyIDs: []int64{11}})
	require.NoError(t, err)
	repo := &paidFallbackSchedulerSettings{values: map[string]string{settingKeyPaidFallback: string(policy), openAIAdvancedSchedulerSettingKey: mode}}
	settings := NewSettingService(repo, &config.Config{})
	accounts := []Account{
		{ID: 1, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, GroupIDs: []int64{7}},
		{ID: 2, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{7}, Extra: map[string]any{PaidFallbackExtraKey: true}},
	}
	svc := &OpenAIGatewayService{accountRepo: paidFallbackAccountRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}}, settingService: settings, rateLimitService: &RateLimitService{settingService: settings}, cfg: &config.Config{}, cache: &schedulerTestGatewayCache{}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
	ctx := WithPaidFallbackAPIKey(context.Background(), 11)
	ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: 7, Platform: platform, Hydrated: true})
	return svc, ctx, repo
}

func selectPaidFallbackTestAccount(svc *OpenAIGatewayService, ctx context.Context, platform string, excluded map[int64]struct{}) (*AccountSelectionResult, error) {
	groupID := int64(7)
	model := "gpt-5.4"
	if platform == PlatformDeepseek {
		model = "deepseek-v4-flash"
	}
	result, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "test-session", model, excluded, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityChatCompletions, false, false, false, platform)
	return result, err
}

func paidFallbackTestAccountRepo(t *testing.T, svc *OpenAIGatewayService) paidFallbackAccountRepo {
	t.Helper()
	repo, ok := svc.accountRepo.(paidFallbackAccountRepo)
	require.True(t, ok)
	return repo
}

func TestPaidFallbackSchedulerFailureRecovery(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		for _, advanced := range []bool{false, true} {
			name := platform + "/legacy"
			if advanced {
				name = platform + "/advanced"
			}
			t.Run(name, func(t *testing.T) {
				svc, ctx, settings := newPaidFallbackScheduler(t, platform, advanced)
				selection, err := selectPaidFallbackTestAccount(svc, ctx, platform, nil)
				require.NoError(t, err)
				require.EqualValues(t, 1, selection.Account.ID, "paid account has higher priority but remains isolated")
				selection.ReleaseFunc()
				repo := paidFallbackTestAccountRepo(t, svc)
				until := time.Now().Add(time.Hour)
				repo.accounts[0].RateLimitResetAt = &until
				selection, err = selectPaidFallbackTestAccount(svc, ctx, platform, nil)
				require.NoError(t, err)
				require.EqualValues(t, 2, selection.Account.ID)
				admissionCtx := ContextWithSelectionProfitGate(ctx, selection)
				_, vetoed, reason := svc.ProfitControlVetoLatest(admissionCtx, selection.Account)
				require.False(t, vetoed, reason)
				settings.setPaidPolicy(`{"enabled":false}`)
				_, vetoed, reason = svc.ProfitControlVetoLatest(admissionCtx, selection.Account)
				require.True(t, vetoed)
				require.Equal(t, "paid_fallback_disabled", reason)
				selection.ReleaseFunc()
				repo.accounts[0].RateLimitResetAt = nil
				selection, err = selectPaidFallbackTestAccount(svc, ctx, platform, nil)
				require.NoError(t, err)
				require.EqualValues(t, 1, selection.Account.ID)
				selection.ReleaseFunc()
			})
		}
	}
}

func TestPaidFallbackSchedulerNeedsFailureEvidence(t *testing.T) {
	svc, ctx, _ := newPaidFallbackScheduler(t, PlatformOpenAI, false)
	_, err := selectPaidFallbackTestAccount(svc, ctx, PlatformOpenAI, map[int64]struct{}{1: {}})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	ctx = WithPaidFallbackFailure(ctx, &Account{ID: 1}, 503)
	selection, err := selectPaidFallbackTestAccount(svc, ctx, PlatformOpenAI, map[int64]struct{}{1: {}})
	require.NoError(t, err)
	require.EqualValues(t, 2, selection.Account.ID)
	selection.ReleaseFunc()
}

func TestPaidFallbackContinuationKeepsProviderState(t *testing.T) {
	svc, ctx, settings := newPaidFallbackScheduler(t, PlatformOpenAI, true)
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, 7, "resp_paid", 2, time.Minute))
	groupID := int64(7)
	selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "resp_paid", "stateful", "gpt-5.4", nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityResponses, false, false, false)
	require.NoError(t, err)
	require.EqualValues(t, 2, selection.Account.ID, "provider state stays on its original credential even after normal capacity recovers")
	selection.ReleaseFunc()
	settings.setPaidPolicy(`{"enabled":false}`)
	_, _, err = svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "resp_paid", "stateful", "gpt-5.4", nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityResponses, false, false, false)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	id, err := svc.getOpenAIWSStateStore().GetResponseAccount(ctx, 7, "resp_paid")
	require.NoError(t, err)
	require.EqualValues(t, 2, id)
}

func TestPaidFallbackContinuationMissingAccountPreservesOrdinaryRouting(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "advanced"}[advanced], func(t *testing.T) {
			svc, ctx, settings := newPaidFallbackScheduler(t, PlatformOpenAI, advanced)
			settings.setPaidPolicy(`{"enabled":false}`)
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, 7, "resp_deleted", 999, time.Minute))
			groupID := int64(7)
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "resp_deleted", "stateful", "gpt-5.4", nil, OpenAIUpstreamTransportHTTPSSE, "", false, false, false)
			require.NoError(t, err)
			require.EqualValues(t, 1, selection.Account.ID)
			selection.ReleaseFunc()
		})
	}
}

func TestPaidFallbackExcludesNonTextCapabilities(t *testing.T) {
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityEmbeddings, OpenAIEndpointCapabilityAlphaSearch} {
		t.Run(string(capability), func(t *testing.T) {
			svc, ctx, _ := newPaidFallbackScheduler(t, PlatformOpenAI, false)
			repo := paidFallbackTestAccountRepo(t, svc)
			until := time.Now().Add(time.Hour)
			repo.accounts[0].RateLimitResetAt = &until
			groupID := int64(7)
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportHTTPSSE, capability, false, false, false)
			if selection != nil && selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selection)
		})
	}
}

func TestPaidFallbackExcludesResponsesImageGeneration(t *testing.T) {
	for _, previousID := range []string{"", "resp_paid"} {
		for _, nativeTool := range []bool{false, true} {
			t.Run(previousID+map[bool]string{false: "/image_intent", true: "/native_image_tool"}[nativeTool], func(t *testing.T) {
				svc, ctx, _ := newPaidFallbackScheduler(t, PlatformOpenAI, false)
				repo := paidFallbackTestAccountRepo(t, svc)
				until := time.Now().Add(time.Hour)
				repo.accounts[0].RateLimitResetAt = &until
				for i := range repo.accounts {
					if repo.accounts[i].Extra == nil {
						repo.accounts[i].Extra = make(map[string]any)
					}
					repo.accounts[i].Extra["openai_responses_supported"] = true
				}
				require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, 7, "resp_paid", 2, time.Minute))
				if nativeTool {
					ctx = WithOpenAIResponsesImageModelRequirements(ctx, []string{"gpt-image-2"})
				} else {
					ctx = WithOpenAIImageGenerationIntent(ctx)
				}
				groupID := int64(7)
				selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, previousID, "", "gpt-5.4", nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityResponses, false, false, false)
				if selection != nil && selection.ReleaseFunc != nil {
					defer selection.ReleaseFunc()
				}
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				require.Nil(t, selection)
			})
		}
	}
}
