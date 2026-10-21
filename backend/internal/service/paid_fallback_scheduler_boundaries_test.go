package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPaidFallbackBusyNormalPoolWaits(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		svc, ctx, _ := newPaidFallbackScheduler(t, PlatformOpenAI, advanced)
		svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true}})
		selection, err := selectPaidFallbackTestAccount(svc, ctx, PlatformOpenAI, nil)
		require.NoError(t, err)
		require.EqualValues(t, 1, selection.Account.ID)
		require.False(t, selection.Acquired)
		require.NotNil(t, selection.WaitPlan)
	}
}

func TestPaidFallbackScopeModelAndDependencyDoNotTriggerSpending(t *testing.T) {
	for _, scenario := range []string{"scope", "model", "dependency", "profit", "retry_cap", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			svc, ctx, _ := newPaidFallbackScheduler(t, PlatformOpenAI, false)
			repo := paidFallbackTestAccountRepo(t, svc)
			until := time.Now().Add(time.Hour)
			repo.accounts[0].RateLimitResetAt = &until
			switch scenario {
			case "scope":
				ctx = WithPaidFallbackAPIKey(ctx, 12)
			case "disabled":
				repo.accounts[0].Schedulable = false
			case "model":
				repo.accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{"different-model": "different-model"}}
			case "dependency":
				repo.listErr = errors.New("database unavailable")
				svc.accountRepo = repo
			case "profit":
				ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: 7, platform: PlatformOpenAI, threshold: .5})
			case "retry_cap":
				repo.accounts[0].RateLimitResetAt = nil
				ctx = WithPaidFallbackOnly(ctx)
			}
			_, err := selectPaidFallbackTestAccount(svc, ctx, PlatformOpenAI, nil)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
		})
	}
}

func TestPaidFallbackWebsocketUsesSamePolicy(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		svc, ctx, settings := newPaidFallbackScheduler(t, PlatformOpenAI, advanced)
		svc.cfg = newSchedulerTestOpenAIWSV2Config()
		repo := paidFallbackTestAccountRepo(t, svc)
		for i := range repo.accounts {
			if repo.accounts[i].Extra == nil {
				repo.accounts[i].Extra = map[string]any{}
			}
			repo.accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = true
		}
		until := time.Now().Add(time.Hour)
		repo.accounts[0].RateLimitResetAt = &until
		groupID := int64(7)
		selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "ws-test", "gpt-5.4", nil, OpenAIUpstreamTransportResponsesWebsocketV2, OpenAIEndpointCapabilityResponses, false, false, false)
		require.NoError(t, err)
		require.EqualValues(t, 2, selection.Account.ID)
		selection.ReleaseFunc()
		turnGate := &openAIProfitControlGate{groupID: 7, threshold: .25}
		turnCtx := ContextWithPaidFallbackSelection(context.WithValue(ctx, openAIProfitControlGateCtxKey{}, turnGate), selection)
		require.Same(t, turnGate, turnCtx.Value(openAIProfitControlGateCtxKey{}))
		_, vetoed, reason := svc.ProfitControlVetoLatest(turnCtx, selection.Account)
		require.False(t, vetoed, reason)
		settings.setPaidPolicy(`{"enabled":false}`)
		_, vetoed, _ = svc.ProfitControlVetoLatest(turnCtx, selection.Account)
		require.True(t, vetoed)
	}
}

func TestPaidFallbackAdmissionRechecksAccountAfterWaiting(t *testing.T) {
	svc, ctx, _ := newPaidFallbackScheduler(t, PlatformOpenAI, false)
	ctx = WithPaidFallbackFailure(ctx, &Account{ID: 1}, 503)
	selection, err := selectPaidFallbackTestAccount(svc, ctx, PlatformOpenAI, map[int64]struct{}{1: {}})
	require.NoError(t, err)
	defer selection.ReleaseFunc()
	admissionCtx := ContextWithSelectionProfitGate(ctx, selection)
	repo := paidFallbackTestAccountRepo(t, svc)
	repo.accounts[1].Schedulable = false
	_, vetoed, reason := svc.ProfitControlVetoLatest(admissionCtx, selection.Account)
	require.True(t, vetoed)
	require.Equal(t, "paid_fallback_account_unavailable", reason)
	repo.accounts[1].Schedulable = true
	invalidRate := math.Inf(1)
	repo.accounts[1].RateMultiplier = &invalidRate
	_, vetoed, _ = svc.ProfitControlVetoLatest(admissionCtx, selection.Account)
	require.True(t, vetoed)
}

func TestPaidFallbackDeepSeekWBToCline(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		svc, ctx, _ := newPaidFallbackScheduler(t, PlatformDeepseek, advanced)
		repo := paidFallbackTestAccountRepo(t, svc)
		repo.accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "global:deepseek-v4.1-flash"}}
		repo.accounts[1].Platform = PlatformCline
		repo.accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "cline-pass/deepseek-v4.1-flash-1017"}}
		groupID := int64(7)
		selectAccount := func(ctx context.Context, excluded map[int64]struct{}) (*AccountSelectionResult, error) {
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "wb-cline", "deepseek-v4.1-flash", excluded, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityChatCompletions, false, false, false, PlatformDeepseek)
			return selection, err
		}
		selection, err := selectAccount(ctx, nil)
		require.NoError(t, err)
		require.EqualValues(t, 1, selection.Account.ID)
		selection.ReleaseFunc()
		failedCtx := WithPaidFallbackFailure(ctx, selection.Account, 503)
		selection, err = selectAccount(failedCtx, map[int64]struct{}{1: {}})
		require.NoError(t, err)
		require.EqualValues(t, 2, selection.Account.ID)
		require.Equal(t, PlatformCline, selection.Account.Platform)
		_, vetoed, reason := svc.ProfitControlVetoLatest(ContextWithSelectionProfitGate(failedCtx, selection), selection.Account)
		require.False(t, vetoed, reason)
		selection.ReleaseFunc()
		selection, err = selectAccount(ctx, nil)
		require.NoError(t, err)
		require.EqualValues(t, 1, selection.Account.ID)
		selection.ReleaseFunc()
	}
}
