//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestValidatePaidFallbackExtra(t *testing.T) {
	for _, platform := range []string{"openai", "deepseek", "cline"} {
		require.NoError(t, validatePaidFallbackExtra(platform, map[string]any{PaidFallbackExtraKey: true}, nil))
	}
	require.Error(t, validatePaidFallbackExtra("anthropic", map[string]any{PaidFallbackExtraKey: true}, nil))
	require.NoError(t, validatePaidFallbackExtra("anthropic", map[string]any{PaidFallbackExtraKey: false}, nil))
	require.NoError(t, validatePaidFallbackExtra("anthropic", map[string]any{"other": true}, map[string]any{PaidFallbackExtraKey: nil}))
	for _, value := range []any{nil, "false", float64(0), []any{}, map[string]any{}} {
		require.Error(t, validatePaidFallbackExtra("openai", map[string]any{PaidFallbackExtraKey: value}, nil))
		require.NoError(t, validatePaidFallbackExtra("anthropic", map[string]any{PaidFallbackExtraKey: value}, map[string]any{PaidFallbackExtraKey: value}))
	}
}

func TestPaidFallbackBulkAccountIntent(t *testing.T) {
	for _, intent := range []string{"keep", "paid", "ordinary", "invalid_platform", "missing_target", "malformed"} {
		t.Run(intent, func(t *testing.T) {
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
				1: {ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Extra: map[string]any{PaidFallbackExtraKey: true, "custom": "preserved"}},
				2: {ID: 2, Platform: PlatformCline, Type: AccountTypeAPIKey},
			}}
			extra := map[string]any{"custom": "updated"}
			switch intent {
			case "paid", "invalid_platform", "missing_target":
				extra[PaidFallbackExtraKey] = true
			case "ordinary":
				extra[PaidFallbackExtraKey] = false
			case "malformed":
				extra[PaidFallbackExtraKey] = "false"
			}
			if intent == "invalid_platform" {
				repo.accounts[2].Platform = PlatformAnthropic
			}
			if intent == "missing_target" {
				delete(repo.accounts, 2)
			}
			result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1, 2}, Extra: extra})
			if intent == "invalid_platform" || intent == "missing_target" || intent == "malformed" {
				require.Error(t, err)
				require.Empty(t, repo.bulkUpdates, "all targets must validate before any write")
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, result.Success)
			require.Len(t, repo.bulkUpdates, 1)
			if intent == "keep" {
				require.NotContains(t, repo.bulkUpdates[0].Extra, PaidFallbackExtraKey)
			} else {
				require.Equal(t, intent == "paid", repo.bulkUpdates[0].Extra[PaidFallbackExtraKey])
			}
			require.Equal(t, true, repo.accounts[1].Extra[PaidFallbackExtraKey])
		})
	}
}

func TestPaidFallbackAccountEditsPreserveIsolation(t *testing.T) {
	for _, marker := range []any{true, false, nil, "false"} {
		repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
			1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Extra: map[string]any{PaidFallbackExtraKey: marker}},
		}}
		svc := &adminServiceImpl{accountRepo: repo}
		updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{"custom": "changed"}})
		require.NoError(t, err)
		require.Contains(t, updated.Extra, PaidFallbackExtraKey)
		require.Equal(t, marker, updated.Extra[PaidFallbackExtraKey])
		updated, err = svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{PaidFallbackExtraKey: false}})
		require.NoError(t, err)
		require.False(t, updated.IsPaidFallback())
	}
}

func TestPaidFallbackExtraMergeValidatesAndPreservesSettings(t *testing.T) {
	ctx := context.Background()
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		1: {ID: 1, Platform: PlatformDeepseek, Extra: map[string]any{"quota_limit": 3.0, "privacy_mode": true}},
		2: {ID: 2, Platform: PlatformGemini, Extra: map[string]any{PaidFallbackExtraKey: nil}},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	for _, enabled := range []bool{true, false} {
		require.NoError(t, svc.UpdateAccountExtra(ctx, 1, map[string]any{PaidFallbackExtraKey: enabled}))
		require.Equal(t, map[string]any{"quota_limit": 3.0, "privacy_mode": true, PaidFallbackExtraKey: enabled}, repo.accounts[1].Extra)
	}
	require.Error(t, svc.UpdateAccountExtra(ctx, 1, map[string]any{PaidFallbackExtraKey: "false"}))
	require.Error(t, svc.UpdateAccountExtra(ctx, 2, map[string]any{PaidFallbackExtraKey: true}))
	require.Empty(t, repo.updates[2])
	require.NoError(t, svc.UpdateAccountExtra(ctx, 2, map[string]any{"custom": "changed"}))
	require.True(t, repo.accounts[2].IsPaidFallback())
	require.NoError(t, svc.UpdateAccountExtra(ctx, 2, map[string]any{PaidFallbackExtraKey: false}))
	require.False(t, repo.accounts[2].IsPaidFallback())
}

type paidAttemptTestStore struct {
	PaidFallbackOpsStore
	start  func(context.Context, PaidFallbackAttempt) error
	finish func(context.Context, string, string, int) error
}

func (s *paidAttemptTestStore) StartAttempt(ctx context.Context, a PaidFallbackAttempt) error {
	return s.start(ctx, a)
}
func (s *paidAttemptTestStore) FinishAttempt(ctx context.Context, id, outcome string, status int) error {
	return s.finish(ctx, id, outcome, status)
}

func paidAttemptContext(t *testing.T, store PaidFallbackOpsStore) (context.Context, *paidFallbackSettingRepo) {
	t.Helper()
	s, repo := paidFallbackTestSettings(t)
	s.paidFallbackOpsStore = store
	group := int64(7)
	gateway := &OpenAIGatewayService{settingService: s, cfg: &config.Config{}, accountRepo: paidFallbackAccountRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, GroupIDs: []int64{7}, Status: StatusError, Schedulable: false}}}}}
	permit := &paidFallbackPermit{settings: s, gateway: gateway, groupID: group, apiKeyID: 11, accountID: 23, request: OpenAIAccountScheduleRequest{GroupID: &group, Platform: PlatformOpenAI, RequestedModel: "forward-ds", RequiredTransport: OpenAIUpstreamTransportHTTPSSE, RequiredCapability: OpenAIEndpointCapabilityChatCompletions}}
	ctx := context.WithValue(context.Background(), paidFallbackPermitKey{}, permit)
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "server-generated-request-id")
	return WithPaidFallbackModelSend(WithPaidFallbackPublicModel(ctx, "public-ds")), repo
}

func TestPaidFallbackDurableSendAdmission(t *testing.T) {
	for _, tc := range []string{"off", "account_mismatch", "canceled", "insert_failure", "missing_store", "missing_model", "off_after_insert", "cancel_after_insert"} {
		t.Run(tc, func(t *testing.T) {
			writes, finishes := 0, 0
			var repo *paidFallbackSettingRepo
			var cancel context.CancelFunc
			store := &paidAttemptTestStore{start: func(ctx context.Context, a PaidFallbackAttempt) error {
				writes++
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 750*time.Millisecond)
				if tc == "insert_failure" {
					return errors.New("database unavailable")
				}
				if tc == "off_after_insert" {
					require.NoError(t, repo.Set(ctx, settingKeyPaidFallback, `{"enabled":false}`))
				}
				if tc == "cancel_after_insert" {
					cancel()
				}
				return nil
			}, finish: func(_ context.Context, _ string, outcome string, _ int) error {
				finishes++
				require.Equal(t, "not_sent", outcome)
				return nil
			}}
			ctx, r := paidAttemptContext(t, store)
			repo = r
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()
			account := int64(23)
			switch tc {
			case "off":
				require.NoError(t, repo.Set(ctx, settingKeyPaidFallback, `{"enabled":false}`))
			case "account_mismatch":
				account = 24
			case "canceled":
				cancel()
			case "missing_store":
				ctx.Value(paidFallbackPermitKey{}).(*paidFallbackPermit).settings.paidFallbackOpsStore = nil
			case "missing_model":
				ctx = WithPaidFallbackPublicModel(ctx, "")
			}
			finish, err := BeginPaidFallbackHTTPAttempt(ctx, account)
			require.Error(t, err)
			require.Nil(t, finish)
			if tc == "insert_failure" || tc == "off_after_insert" || tc == "cancel_after_insert" {
				require.Equal(t, 1, writes)
			} else {
				require.Zero(t, writes)
			}
			if tc == "off_after_insert" || tc == "cancel_after_insert" || tc == "insert_failure" {
				require.Equal(t, 1, finishes)
			} else {
				require.Zero(t, finishes)
			}
		})
	}
}

func TestPaidFallbackDurableSendResultsAndRetryIdentity(t *testing.T) {
	type result struct {
		id, outcome string
		status      int
	}
	results := make(chan result, 4)
	attempts := []PaidFallbackAttempt{}
	store := &paidAttemptTestStore{start: func(_ context.Context, a PaidFallbackAttempt) error { attempts = append(attempts, a); return nil }, finish: func(_ context.Context, id, outcome string, status int) error {
		results <- result{id, outcome, status}
		return errors.New("result write failed")
	}}
	ctx, _ := paidAttemptContext(t, store)
	first, err := BeginPaidFallbackHTTPAttempt(ctx, 23)
	require.NoError(t, err)
	first(0, errors.New("connection lost"))
	first(200, nil)
	second, err := BeginPaidFallbackHTTPAttempt(ctx, 23)
	require.NoError(t, err)
	second(200, nil)
	require.Len(t, attempts, 2)
	require.NotEqual(t, attempts[0].ID, attempts[1].ID)
	require.Equal(t, attempts[0].InboundRequestID, attempts[1].InboundRequestID)
	require.Equal(t, "server-generated-request-id", attempts[0].InboundRequestID)
	require.Equal(t, "public-ds", attempts[0].PublicModel)
	require.Equal(t, "forward-ds", attempts[0].ForwardModel)
	outcomes := map[string]string{}
	for range 2 {
		select {
		case r := <-results:
			outcomes[r.id] = r.outcome
		case <-time.After(time.Second):
			t.Fatal("async audit result missing")
		}
	}
	require.Equal(t, "unknown", outcomes[attempts[0].ID])
	require.Equal(t, "http_headers", outcomes[attempts[1].ID])
	require.Empty(t, results)
	require.Len(t, attempts, 2, "result persistence failure cannot replay a supplier call")
}

func TestPaidFallbackModelOnlyAndBridgeAudit(t *testing.T) {
	attempts := []PaidFallbackAttempt{}
	store := &paidAttemptTestStore{start: func(_ context.Context, a PaidFallbackAttempt) error { attempts = append(attempts, a); return nil }}
	ctx, policy := paidAttemptContext(t, store)
	aux := context.WithValue(ctx, paidFallbackModelSendKey{}, false)
	_, err := BeginPaidFallbackHTTPAttempt(aux, 24)
	require.Error(t, err)
	finish, err := BeginPaidFallbackHTTPAttempt(aux, 23)
	require.NoError(t, err)
	finish(200, nil)
	finish, err = BeginPaidFallbackHTTPAttempt(context.Background(), 23)
	require.NoError(t, err)
	finish(200, nil)
	require.Empty(t, attempts, "auxiliary and ordinary traffic must not depend on the audit store")
	_, err = BeginPaidFallbackHTTPAttempt(WithPaidFallbackBridgeTurn(ctx, "next-public-ds", 3), 23)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, "ws_http_bridge", attempts[0].Transport)
	require.Equal(t, 3, attempts[0].Turn)
	require.Equal(t, "next-public-ds", attempts[0].PublicModel)
	_, err = BeginPaidFallbackWSSend(ctx, 23, 4, "turn-public-ds", "turn-upstream-ds", "ws_ctx_pool")
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.Equal(t, "turn-public-ds", attempts[1].PublicModel)
	require.Equal(t, "turn-upstream-ds", attempts[1].UpstreamModel)
	require.NoError(t, policy.Set(ctx, settingKeyPaidFallback, `{"enabled":false}`))
	_, err = BeginPaidFallbackHTTPAttempt(aux, 23)
	require.Error(t, err)
	require.Len(t, attempts, 2, "auxiliary revocation must not add a model attempt")
}

func TestPaidFallbackPassthroughEveryModelFrame(t *testing.T) {
	for _, messageType := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
		t.Run(messageType.String(), func(t *testing.T) {
			attempts := []PaidFallbackAttempt{}
			results := make(chan string, 3)
			store := &paidAttemptTestStore{start: func(_ context.Context, a PaidFallbackAttempt) error { attempts = append(attempts, a); return nil }, finish: func(_ context.Context, _ string, outcome string, _ int) error { results <- outcome; return nil }}
			ctx, repo := paidAttemptContext(t, store)
			inner := newStagedPassthroughConn()
			defer func() { _ = inner.Close() }()
			conn := &openAIWSPassthroughFirstOutputFrameConn{inner: inner, beforeModelWrite: func(_ context.Context, payload []byte) (func(int, error), error) {
				return BeginPaidFallbackWSSend(ctx, 23, len(attempts)+1, gjson.GetBytes(payload, "model").String(), "mapped-ds", "ws_passthrough")
			}, resolveDeadline: func([]byte) openAIWSPassthroughFirstOutputDeadline {
				return openAIWSPassthroughFirstOutputDeadline{startedAt: time.Now(), timeout: time.Second}
			}}
			require.NoError(t, conn.WriteFrame(ctx, messageType, []byte(`{"type":"response.create","model":"first-ds"}`)))
			require.NoError(t, conn.WriteFrame(ctx, messageType, []byte(`{"type":"response.create","model":"next-ds"}`)))
			require.NoError(t, conn.WriteFrame(ctx, messageType, []byte(`{"type":"input_audio_buffer.append","audio":"test"}`)))
			require.Len(t, attempts, 2)
			require.Len(t, inner.writes, 3)
			require.True(t, conn.state.armed)
			require.Equal(t, uint64(2), conn.state.generation, "every model frame arms a deadline")
			require.Equal(t, "first-ds", attempts[0].PublicModel)
			require.Equal(t, "next-ds", attempts[1].PublicModel)
			require.NoError(t, repo.Set(ctx, settingKeyPaidFallback, `{"enabled":false}`))
			require.Error(t, conn.WriteFrame(ctx, messageType, []byte(`{"type":"response.create","model":"third-ds"}`)))
			require.Len(t, inner.writes, 3)
			require.Len(t, attempts, 2)
			for range 2 {
				select {
				case outcome := <-results:
					require.Equal(t, "ws_frame_written", outcome)
				case <-time.After(time.Second):
					t.Fatal("frame result missing")
				}
			}
		})
	}
}

type paidOpsWorkerTestStore struct {
	PaidFallbackOpsStore
	delivery                                         *PaidFallbackDelivery
	afterAdvance                                     func()
	canSend                                          func() bool
	advances, claims, completes, retries, suppressed int
}

func (s *paidOpsWorkerTestStore) Advance(_ context.Context, _ PaidFallbackNotificationSettings, _ []string) error {
	s.advances++
	if s.afterAdvance != nil {
		s.afterAdvance()
	}
	return nil
}
func (s *paidOpsWorkerTestStore) Claim(_ context.Context, limit int) (*PaidFallbackDelivery, error) {
	s.claims++
	return s.delivery, nil
}
func (s *paidOpsWorkerTestStore) CanSend(context.Context, string, string) (bool, error) {
	if s.canSend != nil {
		return s.canSend(), nil
	}
	return true, nil
}
func (s *paidOpsWorkerTestStore) Complete(context.Context, string, string) error {
	s.completes++
	s.delivery = nil
	return nil
}
func (s *paidOpsWorkerTestStore) Retry(context.Context, string, string) error {
	s.retries++
	return nil
}

func (s *paidOpsWorkerTestStore) Suppress(context.Context, string, string) error {
	s.suppressed++
	s.delivery = nil
	return nil
}

type paidOpsEmailSettingRepo struct {
	*notificationEmailMemorySettingRepo
	failKey string
}

func (r *paidOpsEmailSettingRepo) Set(ctx context.Context, key, value string) error {
	if key == r.failKey {
		return errors.New("post-SMTP bookkeeping unavailable")
	}
	return r.notificationEmailMemorySettingRepo.Set(ctx, key, value)
}

func TestPaidFallbackNotificationWorkerSMTPFences(t *testing.T) {
	for _, scenario := range []string{"success", "disabled", "limit_zero", "resolved_suppressed", "lease_lost", "off_at_fence", "severity_at_fence", "handoff_at_fence", "smtp_failure", "sent_bookkeeping_failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			runtimegate.SetProcessActive(true)
			t.Cleanup(func() { runtimegate.SetProcessActive(true) })
			smtp := startNotificationEmailTestSMTPServer(t)
			repo := &paidOpsEmailSettingRepo{notificationEmailMemorySettingRepo: newNotificationEmailMemorySettingRepo()}
			require.NoError(t, repo.SetMultiple(ctx, smtp.settings()))
			settings := NewSettingService(repo, nil)
			notifications := PaidFallbackNotificationSettings{Enabled: scenario != "disabled", ReminderMinutes: 30, RecoveryQuietSeconds: 60}
			require.NoError(t, settings.UpdatePaidFallbackNotifications(ctx, notifications))
			ops := &OpsService{settingRepo: repo}
			config := defaultOpsEmailNotificationConfig()
			config.Alert.Recipients = []string{"operator@test.invalid"}
			config.Alert.RateLimitPerHour = 10
			if scenario == "limit_zero" {
				config.Alert.RateLimitPerHour = 0
			}
			setOps := func() {
				raw, err := json.Marshal(config)
				require.NoError(t, err)
				require.NoError(t, repo.Set(ctx, SettingKeyOpsEmailNotificationConfig, string(raw)))
			}
			setOps()
			store := &paidOpsWorkerTestStore{delivery: &PaidFallbackDelivery{ID: "delivery-id", ClaimToken: "claim-token", Recipient: "operator@test.invalid", Kind: "open", Summary: "No prompts or keys"}}
			switch scenario {
			case "resolved_suppressed":
				store.delivery.Kind = "recovered"
			case "lease_lost":
				store.canSend = func() bool { return false }
			case "handoff_at_fence":
				store.canSend = func() bool { runtimegate.SetProcessActive(false); return true }
			case "off_at_fence":
				store.afterAdvance = func() {
					notifications.Enabled = false
					require.NoError(t, settings.UpdatePaidFallbackNotifications(ctx, notifications))
				}
			case "severity_at_fence":
				store.afterAdvance = func() { config.Alert.MinSeverity = "critical"; setOps() }
			case "smtp_failure":
				smtp.close()
			case "sent_bookkeeping_failure":
				repo.failKey = notificationEmailDeliveryKey(NotificationEmailEventOpsAlert, "paid_fallback_incident", "delivery-id", "operator@test.invalid", "")
			}
			email := NewNotificationEmailService(repo, NewEmailService(repo, nil))
			worker := NewPaidFallbackOpsService(store, settings, ops, email)
			err := worker.tick(ctx)
			require.Equal(t, 1, store.advances)
			switch scenario {
			case "success", "sent_bookkeeping_failure", "limit_zero":
				require.NoError(t, err)
				require.Equal(t, int64(1), smtp.messageCount())
				require.Equal(t, 1, store.completes)
				require.Zero(t, store.retries)
			case "disabled":
				require.NoError(t, err)
				require.Zero(t, smtp.messageCount())
				require.Zero(t, store.retries)
			case "resolved_suppressed", "off_at_fence", "severity_at_fence":
				require.NoError(t, err)
				require.Zero(t, smtp.messageCount())
				require.Equal(t, 1, store.suppressed)
				require.Zero(t, store.retries)
			default:
				require.Error(t, err)
				require.Zero(t, smtp.messageCount())
				require.Equal(t, 1, store.retries)
				require.Zero(t, store.completes)
			}
		})
	}
}

func TestPaidFallbackUnfinishedDeadlineHonorsTransportConfig(t *testing.T) {
	require.Equal(t, 900, paidFallbackUnfinishedAfterSeconds(nil))
	cfg := &config.Config{}
	cfg.Gateway.OpenAIResponseHeaderTimeout = 1800
	require.Equal(t, 1920, paidFallbackUnfinishedAfterSeconds(cfg))
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 2400
	require.Equal(t, 2520, paidFallbackUnfinishedAfterSeconds(cfg))
}

type paidOpsAccountStatsRepo struct {
	AccountRepository
	accounts []Account
}

func (r *paidOpsAccountStatsRepo) ListOpsAccountsForStats(context.Context, string, *int64) ([]Account, error) {
	return r.accounts, nil
}

func TestPaidFallbackOrdinaryAvailabilityExcludesReserves(t *testing.T) {
	group := &Group{ID: 7, Name: "DS"}
	accounts := []Account{
		{ID: 1, Platform: "deepseek", Status: StatusActive, Schedulable: true, Groups: []*Group{group}},
		{ID: 2, Platform: "deepseek", Status: StatusError, Groups: []*Group{group}},
		{ID: 3, Platform: "deepseek", Status: StatusActive, Schedulable: true, Groups: []*Group{group}, Extra: map[string]any{PaidFallbackExtraKey: true}},
		{ID: 4, Platform: "deepseek", Status: StatusActive, Schedulable: true, Groups: []*Group{group}, Extra: map[string]any{PaidFallbackExtraKey: nil}},
		{ID: 5, Platform: "deepseek", Status: StatusError, Groups: []*Group{group}, Extra: map[string]any{PaidFallbackExtraKey: "false"}},
	}
	ops := &OpsService{accountRepo: &paidOpsAccountStatsRepo{accounts: accounts}}
	ops.SetMonitoringEnabled(true)
	platforms, groups, rows, _, err := ops.GetAccountAvailabilityStats(context.Background(), "", nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, platforms["deepseek"].TotalAccounts)
	require.EqualValues(t, 1, platforms["deepseek"].AvailableCount)
	require.EqualValues(t, 1, platforms["deepseek"].ErrorCount)
	require.EqualValues(t, 3, platforms["deepseek"].PaidTotal)
	require.EqualValues(t, 2, platforms["deepseek"].PaidAvailable)
	require.EqualValues(t, 2, groups[7].TotalAccounts)
	require.EqualValues(t, 3, groups[7].PaidTotal)
	require.True(t, rows[4].PaidFallback)
	require.False(t, rows[1].PaidFallback)
}
