package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func openAIResponsesImageRoutingAccount(id int64, priority int, supportedImageModels ...string) Account {
	mapping := map[string]any{"gpt-5.5": "gpt-5.5"}
	for _, model := range supportedImageModels {
		mapping[model] = model
	}
	return Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    priority,
		Credentials: map[string]any{"model_mapping": mapping},
	}
}

func newOpenAIResponsesImageRoutingService(advanced bool, accounts []Account, cache *schedulerTestGatewayCache) *OpenAIGatewayService {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	if cache == nil {
		cache = &schedulerTestGatewayCache{}
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	if advanced {
		svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
	}
	return svc
}

func selectOpenAIResponsesImageRoutingAccount(
	ctx context.Context,
	svc *OpenAIGatewayService,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	return svc.SelectAccountWithSchedulerForCapability(
		ctx,
		groupID,
		previousResponseID,
		sessionHash,
		"gpt-5.5",
		nil,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityResponses,
		false,
		false,
		true,
		PlatformOpenAI,
	)
}

func TestOpenAIResponsesNativeImageGenerationToolModels(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "omitted model uses billing default",
			body: `{"model":"gpt-5.5","tools":[{"type":"image_generation"}]}`,
			want: []string{"gpt-image-2"},
		},
		{
			name: "explicit image one",
			body: `{"model":"gpt-5.5","tools":[{"type":"image_generation","model":"gpt-image-1"}]}`,
			want: []string{"gpt-image-1"},
		},
		{
			name: "multiple distinct native tools",
			body: `{"tools":[{"type":"image_generation","model":"gpt-image-1"},{"type":"image_generation"},{"type":"image_generation","model":"gpt-image-1"}]}`,
			want: []string{"gpt-image-1", "gpt-image-2"},
		},
		{
			name: "passive namespace and functions do not require an image mapping",
			body: `{"tools":[{"type":"namespace","name":"image_gen"},{"type":"function","name":"image_generation","model":"gpt-image-1"}]}`,
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, OpenAIResponsesNativeImageGenerationToolModels([]byte(tt.body)))
		})
	}
}

func TestOpenAIResponsesImageModelRequirement_ContextClearsAndPreservesMappingSemantics(t *testing.T) {
	textOnly := openAIResponsesImageRoutingAccount(71010, 0)
	withImage := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{"gpt-image-2"})
	require.Equal(t, "image_model_not_supported", OpenAIResponsesImageModelRequirementFailureReason(withImage, &textOnly, PlatformOpenAI))

	textTurn := WithOpenAIResponsesImageModelRequirements(withImage, nil)
	require.Empty(t, openAIResponsesImageModelRequirementsFromContext(textTurn))
	require.Empty(t, OpenAIResponsesImageModelRequirementFailureReason(textTurn, &textOnly, PlatformOpenAI))

	for _, tt := range []struct {
		name     string
		account  Account
		platform string
	}{
		{
			name: "wildcard mapping",
			account: Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-image-*": "image"},
			}},
			platform: PlatformOpenAI,
		},
		{
			name:     "empty mapping permits models",
			account:  Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			platform: PlatformOpenAI,
		},
		{
			name: "passthrough permits models despite a stale mapping",
			account: Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}},
				Extra:       map[string]any{"openai_passthrough": true},
			},
			platform: PlatformOpenAI,
		},
		{
			name:     "other platform is outside this gate",
			account:  Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}},
			platform: PlatformGrok,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Empty(t, OpenAIResponsesImageModelRequirementFailureReason(withImage, &tt.account, tt.platform))
		})
	}
}

func TestOpenAIResponsesImageModelRequirement_SelectsMappedImageAccount(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "advanced"}[advanced], func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(71001)
			textOnly := Account{
				ID:          71001,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    0,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}},
			}
			imageCapable := Account{
				ID:          71002,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    10,
				Credentials: map[string]any{"model_mapping": map[string]any{
					"gpt-5.5":     "gpt-5.5",
					"gpt-image-2": "gpt-image-2",
				}},
			}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = false
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{textOnly, imageCapable}},
				cache:              &schedulerTestGatewayCache{},
				cfg:                cfg,
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			if advanced {
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}

			ctx := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{"gpt-image-2"})
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(
				ctx,
				&groupID,
				"",
				"",
				"gpt-5.5",
				nil,
				OpenAIUpstreamTransportAny,
				OpenAIEndpointCapabilityResponses,
				false,
				false,
				true,
				PlatformOpenAI,
			)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, imageCapable.ID, selection.Account.ID)
		})
	}
}

func TestOpenAIResponsesImageModelRequirement_RequestShapesSelectEveryNativeToolModel(t *testing.T) {
	groupID := int64(71020)
	tests := []struct {
		name       string
		body       string
		accounts   []Account
		selectedID int64
	}{
		{
			name:       "omitted tool model requires gpt image two",
			body:       `{"model":"gpt-5.5","tools":[{"type":"image_generation"}]}`,
			accounts:   []Account{openAIResponsesImageRoutingAccount(71021, 0), openAIResponsesImageRoutingAccount(71022, 10, "gpt-image-2")},
			selectedID: 71022,
		},
		{
			name:       "explicit image one requires its own mapping",
			body:       `{"model":"gpt-5.5","tools":[{"type":"image_generation","model":"gpt-image-1"}]}`,
			accounts:   []Account{openAIResponsesImageRoutingAccount(71023, 0), openAIResponsesImageRoutingAccount(71024, 10, "gpt-image-1")},
			selectedID: 71024,
		},
		{
			name: "multiple native tools require all mappings",
			body: `{"model":"gpt-5.5","tools":[{"type":"image_generation","model":"gpt-image-1"},{"type":"image_generation"}]}`,
			accounts: []Account{
				openAIResponsesImageRoutingAccount(71025, 0, "gpt-image-1"),
				openAIResponsesImageRoutingAccount(71026, 10, "gpt-image-1", "gpt-image-2"),
			},
			selectedID: 71026,
		},
		{
			name:       "text request stays on preferred text account",
			body:       `{"model":"gpt-5.5","input":"hello"}`,
			accounts:   []Account{openAIResponsesImageRoutingAccount(71027, 0), openAIResponsesImageRoutingAccount(71028, 10, "gpt-image-2")},
			selectedID: 71027,
		},
		{
			name:       "passive image namespace stays on preferred text account",
			body:       `{"model":"gpt-5.5","tools":[{"type":"namespace","name":"image_gen"}]}`,
			accounts:   []Account{openAIResponsesImageRoutingAccount(71029, 0), openAIResponsesImageRoutingAccount(71030, 10, "gpt-image-2")},
			selectedID: 71029,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newOpenAIResponsesImageRoutingService(false, tt.accounts, nil)
			ctx := WithOpenAIResponsesImageModelRequirements(context.Background(), OpenAIResponsesNativeImageGenerationToolModels([]byte(tt.body)))
			selection, _, err := selectOpenAIResponsesImageRoutingAccount(ctx, svc, &groupID, "", "")
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, tt.selectedID, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIResponsesImageModelRequirement_SkipsImageRateLimitedAccounts(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	tests := []struct {
		name            string
		imageModel      string
		rateLimitKey    string
		rateLimitReset  string
		wantImageID     int64
		wantImageReason string
		withBackup      bool
	}{
		{
			name:            "active per image model cooldown",
			rateLimitKey:    "gpt-image-2",
			rateLimitReset:  future,
			wantImageID:     71072,
			wantImageReason: "image_model_rate_limited",
			withBackup:      true,
		},
		{
			name:            "active image family cooldown",
			rateLimitKey:    openAIImageGenerationRateLimitKey,
			rateLimitReset:  future,
			wantImageID:     71072,
			wantImageReason: "image_model_rate_limited",
			withBackup:      true,
		},
		{
			name:            "active image family cooldown with relay alias",
			imageModel:      "image-alias",
			rateLimitKey:    openAIImageGenerationRateLimitKey,
			rateLimitReset:  future,
			wantImageID:     71072,
			wantImageReason: "image_model_rate_limited",
			withBackup:      true,
		},
		{
			name:           "expired image family cooldown with relay alias",
			imageModel:     "image-alias",
			rateLimitKey:   openAIImageGenerationRateLimitKey,
			rateLimitReset: past,
			wantImageID:    71071,
		},
		{
			name:           "expired image cooldown",
			rateLimitKey:   openAIImageGenerationRateLimitKey,
			rateLimitReset: past,
			wantImageID:    71071,
		},
	}

	for _, advanced := range []bool{false, true} {
		mode := map[bool]string{false: "legacy", true: "advanced"}[advanced]
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				groupID := int64(71070)
				imageModel := tt.imageModel
				if imageModel == "" {
					imageModel = "gpt-image-2"
				}
				cooled := openAIResponsesImageRoutingAccount(71071, 0, imageModel)
				cooled.Extra = map[string]any{
					modelRateLimitsKey: map[string]any{
						tt.rateLimitKey: map[string]any{"rate_limit_reset_at": tt.rateLimitReset},
					},
				}
				accounts := []Account{cooled}
				if tt.withBackup {
					accounts = append(accounts, openAIResponsesImageRoutingAccount(71072, 10, imageModel))
				}
				svc := newOpenAIResponsesImageRoutingService(advanced, accounts, nil)
				svc.cfg.Gateway.OpenAIWS.LBTopK = 1
				imageCtx := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{imageModel})

				require.Equal(t, tt.wantImageReason, OpenAIResponsesImageModelRequirementFailureReason(imageCtx, &cooled, PlatformOpenAI))
				imageSelection, _, err := selectOpenAIResponsesImageRoutingAccount(imageCtx, svc, &groupID, "", "image-cooldown-selection")
				require.NoError(t, err)
				require.NotNil(t, imageSelection)
				require.NotNil(t, imageSelection.Account)
				require.Equal(t, tt.wantImageID, imageSelection.Account.ID)
				if imageSelection.ReleaseFunc != nil {
					imageSelection.ReleaseFunc()
				}

				textCtx := WithOpenAIResponsesImageModelRequirements(imageCtx, nil)
				textSelection, _, err := selectOpenAIResponsesImageRoutingAccount(textCtx, svc, &groupID, "", "image-cooldown-selection")
				require.NoError(t, err)
				require.NotNil(t, textSelection)
				require.NotNil(t, textSelection.Account)
				require.Equal(t, cooled.ID, textSelection.Account.ID, "an image-only cooldown must not exclude the same account for text")
				if textSelection.ReleaseFunc != nil {
					textSelection.ReleaseFunc()
				}
			})
		}
	}
}

func TestOpenAIResponsesImageModelRequirement_SkipsStickyAndPreviousBindings(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "advanced"}[advanced], func(t *testing.T) {
			groupID := int64(71031)
			textOnly := openAIResponsesImageRoutingAccount(71031, 0)
			imageCapable := openAIResponsesImageRoutingAccount(71032, 10, "gpt-image-2")
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:image-routing-session": textOnly.ID}}
			svc := newOpenAIResponsesImageRoutingService(advanced, []Account{textOnly, imageCapable}, cache)
			ctx := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{"gpt-image-2"})

			selection, decision, err := selectOpenAIResponsesImageRoutingAccount(ctx, svc, &groupID, "", "image-routing-session")
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, imageCapable.ID, selection.Account.ID)
			require.False(t, decision.StickySessionHit)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}

	t.Run("previous response binding falls back without deleting its owner", func(t *testing.T) {
		groupID := int64(71033)
		textOnly := openAIResponsesImageRoutingAccount(71033, 0)
		imageCapable := openAIResponsesImageRoutingAccount(71034, 10, "gpt-image-2")
		textOnly.Extra = map[string]any{"openai_apikey_responses_websockets_v2_enabled": true}
		imageCapable.Extra = map[string]any{"openai_apikey_responses_websockets_v2_enabled": true}
		svc := newOpenAIResponsesImageRoutingService(true, []Account{textOnly, imageCapable}, nil)
		svc.cfg.Gateway.OpenAIWS.Enabled = true
		svc.cfg.Gateway.OpenAIWS.OAuthEnabled = true
		svc.cfg.Gateway.OpenAIWS.APIKeyEnabled = true
		svc.cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
		svc.cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = 3600
		store := svc.getOpenAIWSStateStore()
		require.NoError(t, store.BindResponseAccount(context.Background(), groupID, "resp_image_mapping", textOnly.ID, time.Hour))

		ctx := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{"gpt-image-2"})
		selection, decision, err := selectOpenAIResponsesImageRoutingAccount(ctx, svc, &groupID, "resp_image_mapping", "")
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Account)
		require.Equal(t, imageCapable.ID, selection.Account.ID)
		require.False(t, decision.StickyPreviousHit)
		boundID, bindErr := store.GetResponseAccount(context.Background(), groupID, "resp_image_mapping")
		require.NoError(t, bindErr)
		require.Equal(t, textOnly.ID, boundID)
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	})
}

func TestOpenAIResponsesImageModelRequirement_DBRefreshSkipsStaleImageMapping(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "advanced"}[advanced], func(t *testing.T) {
			groupID := int64(71040)
			stalePrimary := openAIResponsesImageRoutingAccount(71041, 0, "gpt-image-2")
			staleBackup := openAIResponsesImageRoutingAccount(71042, 10, "gpt-image-2")
			dbPrimary := openAIResponsesImageRoutingAccount(71041, 0)
			dbBackup := openAIResponsesImageRoutingAccount(71042, 10, "gpt-image-2")
			stalePrimary.GroupIDs = []int64{groupID}
			staleBackup.GroupIDs = []int64{groupID}
			dbPrimary.GroupIDs = []int64{groupID}
			dbBackup.GroupIDs = []int64{groupID}
			snapshot := &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{
				snapshotAccounts: []*Account{&stalePrimary, &staleBackup},
				accountsByID: map[int64]*Account{
					stalePrimary.ID: &stalePrimary,
					staleBackup.ID:  &staleBackup,
				},
			}}
			svc := newOpenAIResponsesImageRoutingService(advanced, []Account{dbPrimary, dbBackup}, nil)
			svc.schedulerSnapshot = snapshot
			ctx := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{"gpt-image-2"})

			selection, _, err := selectOpenAIResponsesImageRoutingAccount(ctx, svc, &groupID, "", "")
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, dbBackup.ID, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIResponsesImageModelRequirement_NoEligibleAccountReportsImageReason(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "advanced"}[advanced], func(t *testing.T) {
			groupID := int64(71050)
			svc := newOpenAIResponsesImageRoutingService(advanced, []Account{openAIResponsesImageRoutingAccount(71051, 0)}, nil)
			ctx := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{"gpt-image-2"})

			selection, _, err := selectOpenAIResponsesImageRoutingAccount(ctx, svc, &groupID, "", "")
			require.Nil(t, selection)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.True(t, strings.Contains(err.Error(), "image_model_not_supported=1"), err)
		})
	}
}

func TestOpenAIResponsesImageModelRequirement_DoesNotGateOtherPlatformScheduling(t *testing.T) {
	groupID := int64(71060)
	account := Account{
		ID:          71061,
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}},
	}
	svc := newOpenAIResponsesImageRoutingService(false, []Account{account}, nil)
	ctx := WithOpenAIResponsesImageModelRequirements(context.Background(), []string{"gpt-image-2"})
	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.5",
		nil,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		true,
		PlatformGrok,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, account.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}
