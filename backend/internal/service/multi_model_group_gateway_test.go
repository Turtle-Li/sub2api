//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestMultiModelCompositeProtocolRefreshPreservesForwardTarget(t *testing.T) {
	account := commandCodeTestAccount(9221)
	account.Credentials["model_mapping"] = map[string]any{"gpt-5.4-high": "claude-sonnet-4-6", "gpt-5.4": "gemini-3-pro"}
	upstream := &httpUpstreamRecorder{err: fmt.Errorf("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	key := modelProtocolCatalogKey(account, buildOpenAIModelsURL(account.GetCNProtocolBaseURL(APIProtocolChatCompletions)))
	upstreamModelProtocols.store(key, map[string][]string{"claude-sonnet-4-6": {APIProtocolAnthropic}}, nil, time.Now())
	t.Cleanup(func() {
		upstreamModelProtocols.mu.Lock()
		delete(upstreamModelProtocols.entries, key)
		upstreamModelProtocols.mu.Unlock()
	})
	requestCtx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 7, Platform: PlatformComposite, Hydrated: true})
	requestCtx = WithCompositeRouteDecision(requestCtx, CompositeRouteDecision{Matched: true, Source: CompositeRouteSourceExplicit, GroupID: 7, PublicModel: "budget-alias", TargetPlatform: PlatformAnthropic, UpstreamModel: "gpt-5.4-high"})
	requestCtx = svc.WithMultiModelGroupProtocol(requestCtx, APIProtocolAnthropic)
	require.True(t, supportsGroupPlatformModelWithContext(requestCtx, account, PlatformAnthropic, "gpt-5.4-high", false))
	selection := attachSelectionProfitGate(withAccountGroupModelConstraint(requestCtx, PlatformAnthropic, "gpt-5.4-high"), &AccountSelectionResult{Account: account, Acquired: true})
	admissionCtx := ContextWithSelectionProfitGate(requestCtx, selection)
	_, vetoed, _ := profitControlVetoLatest(admissionCtx, account, nil)
	require.False(t, vetoed)
	// A catalog refresh after admission changes Messages normalization and the
	// resulting mapped model. Both direct and selected forwards must fail closed.
	upstreamModelProtocols.store(key, map[string][]string{"claude-sonnet-4-6": {APIProtocolChatCompletions}}, nil, time.Now())
	body := []byte(`{"model":"gpt-5.4-high","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	for _, ctx := range []context.Context{requestCtx, admissionCtx} {
		c := adaptiveProtocolTestContext("/v1/messages", body)
		c.Request = c.Request.WithContext(ctx)
		_, err := svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
		require.Error(t, err)
		require.Empty(t, upstream.requests, "the final model must remain in the resolved composite target family")
	}
}

type multiModelHydrationCache struct{ *snapshotHydrationCache }

type multiModelChangingHydrationCache struct {
	*multiModelHydrationCache
	latest *Account
	reads  int
}

func TestMultiModelGroupCatalogColdCandidateDoesNotMaskHealthyPeer(t *testing.T) {
	healthy := multiModelGroupTestAccount(PlatformOpenCodeGo)
	healthy.Credentials = map[string]any{"model_mapping": map[string]any{"credits": "kimi-k3"}}
	cooling := multiModelGroupTestAccount(PlatformCline)
	cooling.ID = 222
	cooling.Credentials = map[string]any{"model_mapping": map[string]any{"pass": "cline-pass/kimi-k3"}}
	now := time.Now()
	setAccountModelRateLimitSnapshot(cooling, clinePassRateLimitKey, now.Add(time.Hour), clinePassLimitReason, now)
	svc := &OpenAIGatewayService{cfg: testConfig(), accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{*healthy, *cooling}}}}
	gid := int64(7)
	selected, err := svc.selectAccountForModelWithExclusions(context.Background(), &gid, PlatformKimi, "", "credits", nil, false, 0, "", false)
	require.NoError(t, err)
	require.Equal(t, healthy.ID, selected.ID)
	fetched := int64(0)
	_, err = svc.fetchScheduledOpenAIModels(context.Background(), &Group{ID: 7, Platform: PlatformKimi}, 1, func(_ context.Context, a *Account) (*OpenAIModelsResponse, error) {
		fetched = a.ID
		return &OpenAIModelsResponse{Body: []byte(`{"data":[{"id":"kimi-k3"}]}`)}, nil
	})
	t.Logf("healthy direct selection=%d; catalog fetched=%d error=%v", selected.ID, fetched, err)
	require.NoError(t, err, "a model-local ClinePass cooldown must not hide healthy OpenCode Kimi catalog")
}
func TestMultiModelGroupCompositeMetadataUsesResolvedModel(t *testing.T) {
	a := multiModelGroupTestAccount(PlatformCline)
	a.Credentials = map[string]any{}
	a.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"gpt-6-astra": {ID: "gpt-6-astra", ContextWindow: 32000, MaxContextWindow: 64000, InputModalities: []string{"text"}}}})
	routes := []CompositeModelRoute{{GroupID: 7, PublicModel: "budget-alias", TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-6-astra", MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointAny, Enabled: true}}
	meta, ok := groupCodexModelMetadata(PlatformComposite, "budget-alias", []Account{*a}, nil, routes, true)
	canonical, canonicalOK := groupCodexModelMetadata(PlatformOpenAI, "gpt-6-astra", []Account{*a}, nil, nil, true)
	require.True(t, canonicalOK)
	require.Equal(t, int64(32000), canonical.ContextWindow)
	body, err := buildCodexModelsManifestForAccounts(PlatformComposite, []string{"budget-alias"}, []Account{*a}, nil, routes, true)
	require.NoError(t, err)
	model := decodeCodexManifestModels(t, body)[0]
	t.Logf("alias metadata found=%t metadata context=%d; published context=%v max=%v", ok, meta.ContextWindow, model["context_window"], model["max_context_window"])
	require.True(t, ok, "explicit group-owned alias must retain selected account metadata for its resolved upstream model")
	require.Equal(t, int64(64000), meta.MaxContextWindow)
	require.Equal(t, float64(32000), model["context_window"])
	require.Equal(t, float64(64000), model["max_context_window"])
}

func (c *multiModelChangingHydrationCache) GetAccount(ctx context.Context, id int64) (*Account, error) {
	c.reads++
	if c.reads > 1 {
		return c.latest, nil
	}
	return c.multiModelHydrationCache.GetAccount(ctx, id)
}

func (c *multiModelHydrationCache) GetSnapshot(_ context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	if len(c.snapshot) > 0 && bucket.Platform == c.snapshot[0].Platform {
		return c.snapshot, true, nil
	}
	return nil, true, nil
}

type multiModelHydrationConcurrencyCache struct {
	*mockConcurrencyCache
	released int
}

func (c *multiModelHydrationConcurrencyCache) ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error {
	c.released++
	return c.mockConcurrencyCache.ReleaseAccountSlot(ctx, accountID, requestID)
}

func TestMultiModelGroupHydrationRechecksMappedFamily(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		for _, groupPlatform := range []string{PlatformAnthropic, PlatformGemini} {
			for _, loadAware := range []bool{false, true} {
				for _, mappingChanged := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/load=%t/changed=%t", provider, groupPlatform, loadAware, mappingChanged), func(t *testing.T) {
						model, otherFamily := "claude-sonnet-4-6", "gemini-3-pro"
						if groupPlatform == PlatformGemini {
							model, otherFamily = otherFamily, model
						}
						old := multiModelGroupTestAccount(provider)
						old.AccountGroups = []AccountGroup{{AccountID: old.ID, GroupID: 7}}
						old.Credentials = map[string]any{"model_mapping": map[string]any{"alias": model}}
						latest := multiModelGroupTestAccount(provider)
						latest.AccountGroups = old.AccountGroups
						if mappingChanged {
							model = otherFamily
						}
						latest.Credentials = map[string]any{"model_mapping": map[string]any{"alias": model}}
						cache := &multiModelHydrationCache{&snapshotHydrationCache{snapshot: []*Account{old}, accounts: map[int64]*Account{old.ID: latest}}}
						svc := &GatewayService{schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, nil, nil, nil), cache: &mockGatewayCacheForPlatform{}, cfg: testConfig(), groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: {ID: 7, Platform: groupPlatform, Status: StatusActive}}}}
						svc.cfg.Gateway.Scheduling.LoadBatchEnabled = loadAware
						concurrencyCache := &multiModelHydrationConcurrencyCache{mockConcurrencyCache: &mockConcurrencyCache{}}
						if loadAware {
							svc.concurrencyService = NewConcurrencyService(concurrencyCache)
						}
						gid := int64(7)
						result, err := svc.SelectAccountWithLoadAwareness(context.Background(), &gid, "", "alias", nil, "", 0)
						if mappingChanged {
							require.ErrorIs(t, err, ErrNoAvailableAccounts, "fresh mapping must stay in the requested group's model family")
							require.Nil(t, result)
							if loadAware {
								require.Equal(t, 1, concurrencyCache.released, "rejected hydrated account must release its acquired slot")
							}
						} else {
							require.NoError(t, err)
							require.Same(t, latest, result.Account)
							if result.ReleaseFunc != nil {
								result.ReleaseFunc()
							}
						}
					})
				}
			}
		}
	}
}

func TestMultiModelGroupOpenAILegacyHydrationRechecksMappedFamily(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		for _, groupPlatform := range []string{PlatformKimi, PlatformDeepseek} {
			for _, mode := range []string{"catalog", "legacy", "batch"} {
				t.Run(provider+"/"+groupPlatform+"/"+mode, func(t *testing.T) {
					model, otherFamily := "kimi-k3", "deepseek-v4-pro"
					if groupPlatform == PlatformDeepseek {
						model, otherFamily = otherFamily, model
					}
					old := multiModelGroupTestAccount(provider)
					old.Credentials = map[string]any{"model_mapping": map[string]any{"alias": model}}
					latest := multiModelGroupTestAccount(provider)
					latest.Credentials = map[string]any{"model_mapping": map[string]any{"alias": otherFamily}}
					cache := &multiModelChangingHydrationCache{multiModelHydrationCache: &multiModelHydrationCache{&snapshotHydrationCache{snapshot: []*Account{old}, accounts: map[int64]*Account{old.ID: old}}}, latest: latest}
					concurrencyCache := &multiModelHydrationConcurrencyCache{mockConcurrencyCache: &mockConcurrencyCache{}}
					svc := &OpenAIGatewayService{schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, nil, nil, nil), cfg: testConfig(), cache: &stubGatewayCache{}, concurrencyService: NewConcurrencyService(concurrencyCache)}
					svc.cfg.Gateway.Scheduling.LoadBatchEnabled = mode == "batch"
					gid := int64(7)
					if mode == "catalog" {
						account, err := svc.selectAccountForModelWithExclusions(context.Background(), &gid, groupPlatform, "", "alias", nil, false, 0, "", false)
						require.ErrorIs(t, err, ErrNoAvailableAccounts)
						require.Nil(t, account)
					} else {
						result, err := svc.selectAccountWithLoadAwareness(context.Background(), &gid, groupPlatform, "", "alias", nil, false, "", false)
						require.ErrorIs(t, err, ErrNoAvailableAccounts)
						require.Nil(t, result)
						if mode == "batch" {
							require.Equal(t, 1, concurrencyCache.released, "rejected hydrated account must release its acquired slot once")
						}
					}
					require.GreaterOrEqual(t, cache.reads, 2, "the mapping changes between fresh recheck and hydration")
				})
			}
		}
	}
}

func TestMultiModelGroupCompositeAliasRetainsResolvedModelDuringHydration(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		t.Run(provider, func(t *testing.T) {
			account := multiModelGroupTestAccount(provider)
			account.AccountGroups = []AccountGroup{{AccountID: account.ID, GroupID: 7}}
			account.Credentials = map[string]any{}
			cache := &multiModelHydrationCache{&snapshotHydrationCache{snapshot: []*Account{account}, accounts: map[int64]*Account{account.ID: account}}}
			svc := &GatewayService{schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, nil, nil, nil), cache: &mockGatewayCacheForPlatform{}, cfg: testConfig(), groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: {ID: 7, Platform: PlatformComposite, Status: StatusActive, Hydrated: true}}}}
			ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{Matched: true, Source: CompositeRouteSourceExplicit, GroupID: 7, PublicModel: "budget-alias", TargetPlatform: PlatformAnthropic, UpstreamModel: "claude-sonnet-4-6", Endpoint: CompositeRouteEndpointAny})
			gid := int64(7)
			selected, err := svc.SelectAccountForModelWithExclusions(ctx, &gid, "", "budget-alias", nil)
			require.NoError(t, err)
			require.Same(t, account, selected)
			result, err := svc.SelectAccountWithLoadAwareness(ctx, &gid, "", "budget-alias", nil, "", 0)
			require.NoError(t, err, "outer hydration must check the resolved upstream model, not the group-owned public alias")
			require.Same(t, account, result.Account)
			if result.ReleaseFunc != nil {
				result.ReleaseFunc()
			}
		})
	}
}

func TestMultiModelGroupNativeGatewaySelection(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		for _, groupPlatform := range []string{PlatformAnthropic, PlatformGemini} {
			t.Run(provider+"/"+groupPlatform, func(t *testing.T) {
				account := multiModelGroupTestAccount(provider)
				account.AccountGroups = []AccountGroup{{GroupID: 7}}
				account.Credentials["model_mapping"] = map[string]any{"claude": "claude-sonnet-4-6", "gemini": "gemini-3-pro"}
				svc := &GatewayService{accountRepo: newGroupAwareMockRepo([]Account{*account}), cache: &mockGatewayCacheForPlatform{}, cfg: testConfig(), groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: {ID: 7, Platform: groupPlatform, Status: StatusActive}}}}
				gid := int64(7)
				model, wrong := "claude", "gemini"
				if groupPlatform == PlatformGemini {
					model, wrong = wrong, model
				}
				selected, err := svc.selectAccountWithMixedScheduling(context.Background(), &gid, "sticky", model, nil, groupPlatform)
				require.NoError(t, err)
				require.Equal(t, account.ID, selected.ID)
				_, err = svc.selectAccountWithMixedScheduling(context.Background(), &gid, "sticky", wrong, nil, groupPlatform)
				require.Error(t, err, "sticky binding cannot bypass the group's model family")
				if groupPlatform == PlatformGemini {
					_, err = svc.selectAccountWithMixedScheduling(WithNativeGeminiAccountSelection(context.Background()), &gid, "sticky", model, nil, groupPlatform)
					require.Error(t, err, "Google native ingress must keep its native account pool")
				}
			})
		}
	}
}

func TestMultiModelGroupChannelMappingAdmission(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		for _, wrongFamily := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrong=%t", provider, wrongFamily), func(t *testing.T) {
				model := "claude-sonnet-4-6"
				if wrongFamily {
					model = "gemini-3-pro"
				}
				a := multiModelGroupTestAccount(provider)
				a.AccountGroups = []AccountGroup{{AccountID: a.ID, GroupID: 7}}
				a.Credentials = map[string]any{"model_mapping": map[string]any{"routed-alias": model}}
				ch := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{7}, ModelMapping: map[string]map[string]string{PlatformAnthropic: {"claude-sonnet-4-6": "routed-alias"}}}
				svc := &GatewayService{accountRepo: newGroupAwareMockRepo([]Account{*a}), cache: &mockGatewayCacheForPlatform{}, cfg: testConfig(), groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: {ID: 7, Platform: PlatformAnthropic, Status: StatusActive}}}, channelService: newTestChannelService(makeStandardRepo(ch, map[int64]string{7: PlatformAnthropic}))}
				gid := int64(7)
				selected, err := svc.SelectAccountForModelWithExclusions(context.Background(), &gid, "", "claude-sonnet-4-6", nil)
				if wrongFamily {
					require.ErrorIs(t, err, ErrNoAvailableAccounts)
					require.Nil(t, selected)
				} else {
					require.NoError(t, err, "only the post-channel alias is configured on this account")
					require.Equal(t, a.ID, selected.ID)
				}
			})
		}
	}
}

func TestMultiModelGroupTerminalRefreshRetainsSelectionConstraint(t *testing.T) {
	for _, provider := range []string{PlatformOpenCodeGo, PlatformCommandCode, PlatformCline} {
		for _, acquired := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/acquired=%t", provider, acquired), func(t *testing.T) {
				selected := multiModelGroupTestAccount(provider)
				selected.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "claude-sonnet-4-6"}}
				latest := multiModelGroupTestAccount(provider)
				latest.Credentials = map[string]any{"model_mapping": map[string]any{"alias": "gemini-3-pro"}}
				rate := 0.2
				selected.RateMultiplier, latest.RateMultiplier = &rate, &rate
				latest.UpdatedAt = selected.UpdatedAt.Add(time.Second)
				ctx := withAccountGroupModelConstraint(context.Background(), PlatformAnthropic, "alias")
				ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: 7, platform: PlatformAnthropic, threshold: 2})
				selection := attachSelectionProfitGate(ctx, &AccountSelectionResult{Account: selected, Acquired: acquired})
				admissionCtx := ContextWithSelectionProfitGate(context.Background(), selection)
				snapshot := NewSchedulerSnapshotService(&gatewayProfitSnapshotCache{account: latest}, nil, nil, nil, nil)
				_, vetoed, reason := profitControlVetoLatest(admissionCtx, selected, snapshot)
				require.True(t, vetoed)
				require.Equal(t, "model_family_mismatch", reason)
			})
		}
	}
}

func TestMultiModelGroupSerializedModelCheckedBeforeUpstreamIO(t *testing.T) {
	account := multiModelGroupTestAccount(PlatformCline)
	ctx := withAccountGroupModelConstraint(context.Background(), PlatformAnthropic, "claude")
	body := []byte(`{"model":"gemini-3-pro"}`)
	svc := &OpenAIGatewayService{}
	request, err := svc.buildUpstreamRequest(ctx, nil, account, body, "test-key", false, "", false)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, request)
	request, err = svc.buildUpstreamRequestOpenAIPassthrough(ctx, nil, account, body, "test-key")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, request)
	request, _, err = svc.buildNativeAnthropicUpstreamRequest(ctx, nil, account, body, "test-key", "https://example.test/v1/messages")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, request)
	response, err := svc.sendCCUpstreamRequest(ctx, nil, account, "https://example.test/v1/chat/completions", body, false, "test-key", "", "")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, response, "a nil HTTP upstream is safe because the serialized model is rejected before I/O")
}

func TestMultiModelGroupMessagesProtocolControlsNormalization(t *testing.T) {
	for _, protocol := range []string{APIProtocolAnthropic, APIProtocolChatCompletions, APIProtocolResponses} {
		t.Run(protocol, func(t *testing.T) {
			account := multiModelGroupTestAccount(PlatformCommandCode)
			account.Credentials = map[string]any{"api_protocol": protocol, "model_mapping": map[string]any{"gpt-5.4-high": "claude-sonnet-4-6", "gpt-5.4": "deepseek-v4-pro"}}
			svc := &OpenAIGatewayService{}
			ctx := svc.WithMultiModelGroupProtocol(context.Background(), APIProtocolAnthropic)
			require.Equal(t, protocol == APIProtocolAnthropic, supportsGroupPlatformModelWithContext(ctx, account, PlatformAnthropic, "gpt-5.4-high", false))
			ctx = svc.WithMultiModelGroupProtocol(context.Background(), APIProtocolChatCompletions)
			require.True(t, supportsGroupPlatformModelWithContext(ctx, account, PlatformAnthropic, "gpt-5.4-high", false), "Chat ingress preserves the raw model before account mapping")
		})
	}
}
