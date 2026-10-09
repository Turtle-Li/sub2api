package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func multiModelGroupTestAccount(platform string) *Account {
	return &Account{ID: 221, Platform: platform, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 10, GroupIDs: []int64{7},
		Credentials: map[string]any{"model_mapping": map[string]any{
			"ds": "deepseek/deepseek-v4-pro", "kimi": "kimi-k3",
			"wrong": "kimi-k3", "claude": "anthropic/claude-sonnet-4-6",
		}}}
}

func TestMultiModelGroupScheduler(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		for _, advanced := range []bool{false, true} {
			for _, loadBatch := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/advanced=%t/batch=%t", provider, advanced, loadBatch), func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
					account := multiModelGroupTestAccount(provider)
					otherGroup := *multiModelGroupTestAccount(provider)
					otherGroup.ID, otherGroup.GroupIDs, otherGroup.Priority = 222, []int64{8}, -100
					cfg := &config.Config{}
					cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
					svc := &OpenAIGatewayService{
						accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{*account, otherGroup}}},
						cfg:         cfg, cache: &schedulerTestGatewayCache{},
						concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
					}
					if advanced {
						svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
					}
					groupID := int64(7)
					for _, tc := range []struct {
						group, model string
						want         bool
					}{
						{PlatformDeepseek, "ds", true}, {PlatformKimi, "kimi", true},
						{PlatformDeepseek, "kimi", false}, {PlatformKimi, "ds", false},
					} {
						// Reuse the session across models to exercise sticky validation.
						selection, _, err := svc.SelectAccountWithSchedulerForCapability(context.Background(), &groupID, "", "shared", tc.model, nil, OpenAIUpstreamTransportAny, "", false, false, true, tc.group)
						if !tc.want {
							require.Error(t, err)
							continue
						}
						require.NoError(t, err)
						require.Equal(t, account.ID, selection.Account.ID)
						if selection.ReleaseFunc != nil {
							selection.ReleaseFunc()
						}
					}
				})
			}
		}
	}
}

func TestMultiModelGroupEligibility(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		t.Run(provider, func(t *testing.T) {
			account := multiModelGroupTestAccount(provider)
			for _, tc := range []struct {
				group, model string
				want         bool
			}{
				{PlatformDeepseek, "ds", true}, {PlatformKimi, "kimi", true},
				{PlatformDeepseek, "kimi", false}, {PlatformKimi, "ds", false},
				{PlatformDeepseek, "wrong", false}, {PlatformDeepseek, "unknown", false},
				{PlatformOpenAI, "claude", false}, {provider, "kimi", true},
			} {
				t.Run(tc.group+"/"+tc.model, func(t *testing.T) {
					got := isOpenAICompatibleAccountEligibleForRequestBeforeProfit(context.Background(), account, tc.group, tc.model, false, "")
					require.Equal(t, tc.want, got)
				})
			}
		})
	}
}

func TestMultiModelGroupModelListing(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		t.Run(provider, func(t *testing.T) {
			account := multiModelGroupTestAccount(provider)
			account.Credentials["model_mapping"] = map[string]any{
				"ds": "deepseek-v4-pro", "kimi": "kimi-k3", "deepseek-fake": "kimi-k3",
			}
			svc := &GatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}}
			gid := int64(7)
			require.Equal(t, []string{"ds"}, svc.GetAvailableModels(context.Background(), &gid, PlatformDeepseek))
			require.Equal(t, []string{"deepseek-fake", "kimi"}, svc.GetAvailableModels(context.Background(), &gid, PlatformKimi))
			for _, mapped := range []bool{true, false} {
				if !mapped {
					account.Credentials = map[string]any{}
				}
				body, err := projectAccountModelsBody([]byte(`{"object":"list","data":[{"id":"deepseek-v4-pro"},{"id":"kimi-k3"}]}`), account, &Group{Platform: PlatformDeepseek}, false)
				require.NoError(t, err)
				var catalog struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(body, &catalog))
				require.Len(t, catalog.Data, 1)
				if mapped {
					require.Equal(t, "ds", catalog.Data[0].ID)
				} else {
					require.Equal(t, "deepseek-v4-pro", catalog.Data[0].ID)
				}
			}
		})
	}
}

func TestMultiModelGroupClineWalletCooldown(t *testing.T) {
	account := multiModelGroupTestAccount(PlatformCline)
	account.Credentials["model_mapping"] = map[string]any{
		"ds": "cline-pass/deepseek-v4-pro", "kimi": "cline-pass/kimi-k3", "credits": "deepseek/deepseek-v4-pro",
	}
	now := time.Now()
	setAccountModelRateLimitSnapshot(account, clinePassRateLimitKey, now.Add(time.Hour), clinePassLimitReason, now)
	for _, tc := range []struct {
		group, model string
		want         bool
	}{
		{PlatformDeepseek, "ds", false}, {PlatformKimi, "kimi", false}, {PlatformDeepseek, "credits", true},
	} {
		require.Equal(t, tc.want, isOpenAICompatibleAccountEligibleForRequestBeforeProfit(context.Background(), account, tc.group, tc.model, false, ""))
	}
}

func TestMultiModelGroupScheduledCatalog(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		t.Run(provider, func(t *testing.T) {
			account := multiModelGroupTestAccount(provider)
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}}}
			results, err := svc.fetchScheduledOpenAIModels(context.Background(), &Group{ID: 7, Platform: PlatformKimi}, 1,
				func(_ context.Context, selected *Account) (*OpenAIModelsResponse, error) {
					require.Equal(t, account.ID, selected.ID)
					return &OpenAIModelsResponse{Body: []byte(`{"data":[{"id":"kimi-k3"}]}`)}, nil
				})
			require.NoError(t, err)
			require.Len(t, results, 1)
		})
	}
}
