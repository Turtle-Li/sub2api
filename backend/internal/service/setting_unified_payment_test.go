package service

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func bindingServiceFixture(t *testing.T) (*SettingService, *panelRateLimitSettingRepo, unifiedpay.StoredIntegration) {
	t.Helper()
	raw := config.UnifiedPaymentConfig{BaseURL: "https://pay.example.com", Environment: "sandbox", AppID: "app.sub2.sandbox", RequestKeyID: "key.sub2.sandbox", OrganizationID: "11111111-1111-4111-8111-111111111111", ProductID: "22222222-2222-4222-8222-222222222222", ReturnURL: "https://sub2.example.com/payment/result", RequestPrivateKeyVaultRef: "vault://secret/data/test#key", VaultAgentSocket: "/run/sub2api-payment-vault/public.sock"}
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	public, ok := key.Public().(ed25519.PublicKey)
	require.True(t, ok)
	stored := unifiedpay.StoredIntegration{Version: 1, BaseURL: raw.BaseURL, SavedAt: time.Now().UTC(), Config: unifiedpay.IntegrationConfig{SchemaVersion: "payment-integration.v1", Environment: unifiedpay.EnvironmentSandbox, OrganizationID: raw.OrganizationID, ProductID: raw.ProductID, AppID: raw.AppID, RequestKeyID: raw.RequestKeyID, PaymentMethods: []string{"alipay", "wechat_pay"}, ReturnURLs: []string{raw.ReturnURL}, WebhookEndpoints: []unifiedpay.IntegrationWebhookEndpoint{{URL: "https://sub2.example.com/api/v1/payment/webhook/unified", SigningKeyID: "webhook.test.key"}}, WebhookSigningKeys: []unifiedpay.IntegrationWebhookKey{{KeyID: "webhook.test.key", Algorithm: "Ed25519", PublicKey: base64.StdEncoding.EncodeToString(public), Status: "active"}}}}
	repo := &panelRateLimitSettingRepo{values: map[string]string{}}
	return &SettingService{settingRepo: repo, cfg: &config.Config{UnifiedPayment: raw}}, repo, stored
}

func bindingClaim() IdempotencyExecutionClaim {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return IdempotencyExecutionClaim{
		ID:                 1,
		RequestFingerprint: "binding-test-fingerprint",
		LockedUntil:        now.Add(30 * time.Second),
		ExpiresAt:          now.Add(24 * time.Hour),
	}
}

func finalizeBindingMutation(t *testing.T, mutation *UnifiedPaymentBindingMutation) error {
	t.Helper()
	status := mutation.Status()
	body, err := json.Marshal(status)
	require.NoError(t, err)
	claim := bindingClaim()
	return mutation.FinalizeIdempotencySuccess(context.Background(), claim, 200, string(body), claim.ExpiresAt)
}

func TestUnifiedPaymentBindingRestartAndManualFallback(t *testing.T) {
	s, repo, stored := bindingServiceFixture(t)
	ctx := context.Background()
	initial, err := s.GetUnifiedPaymentBindingStatus(ctx)
	require.NoError(t, err)
	require.True(t, initial.BootstrapReady)
	require.False(t, initial.Configured)
	require.Equal(t, uint64(0), initial.Revision)

	// A direct StoredIntegration is the pre-revision deployment format.
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, repo.Set(ctx, unifiedPaymentBindingSetting, string(encoded)))
	saved, err := s.GetUnifiedPaymentBindingStatus(ctx)
	require.NoError(t, err)
	require.True(t, saved.PendingRestart)
	require.Equal(t, uint64(0), saved.Revision)

	gateway, err := s.loadUnifiedPaymentGateway(s.cfg)
	require.NoError(t, err)
	require.False(t, gateway.Enabled(), "import must preserve explicit disabled runtime switch")
	loaded, err := s.GetUnifiedPaymentBindingStatus(ctx)
	require.NoError(t, err)
	require.False(t, loaded.PendingRestart)
	require.True(t, loaded.Configured)
	applied, err := stored.Apply(s.cfg.UnifiedPayment)
	require.NoError(t, err)
	require.Equal(t, "alipay,wechat_pay", applied.PaymentMethods)
	require.Contains(t, applied.WebhookPublicKeysJSON, "webhook.test.key")

	manualMutation, err := s.UseManualUnifiedPayment(ctx, loaded.Revision)
	require.NoError(t, err)
	require.NoError(t, finalizeBindingMutation(t, manualMutation))
	_, err = repo.GetValue(ctx, unifiedPaymentBindingSetting)
	require.ErrorIs(t, err, ErrSettingNotFound, "manual mode must preserve the old binary's deletion fallback")
	revisionRaw, err := repo.GetValue(ctx, unifiedPaymentBindingRevisionSetting)
	require.NoError(t, err)
	var revision persistedUnifiedPaymentBindingRevision
	require.NoError(t, json.Unmarshal([]byte(revisionRaw), &revision))
	require.Equal(t, uint64(1), revision.Revision)
	require.Empty(t, revision.BindingSHA256)
	manual := manualMutation.Status()
	require.True(t, manual.PendingRestart)
	require.False(t, manual.Configured)
	require.Equal(t, uint64(1), manual.Revision)

	_, err = s.loadUnifiedPaymentGateway(s.cfg)
	require.NoError(t, err)
	final, err := s.GetUnifiedPaymentBindingStatus(ctx)
	require.NoError(t, err)
	require.False(t, final.PendingRestart)
	require.False(t, final.Configured)
	require.Equal(t, uint64(1), final.Revision)
}

func TestUnifiedPaymentBindingWritesLegacyRawValueForOldBinaryRollback(t *testing.T) {
	s, repo, stored := bindingServiceFixture(t)
	ctx := context.Background()

	mutation, err := s.prepareUnifiedPaymentBindingMutation(ctx, 0, &stored)
	require.NoError(t, err)
	require.NoError(t, finalizeBindingMutation(t, mutation))

	// An older deployed binary reads the primary setting directly into
	// StoredIntegration and ignores the separate metadata key. New writes must
	// retain that exact format so a binary rollback stays readable.
	raw, err := repo.GetValue(ctx, unifiedPaymentBindingSetting)
	require.NoError(t, err)
	var oldBinaryStored unifiedpay.StoredIntegration
	require.NoError(t, json.Unmarshal([]byte(raw), &oldBinaryStored))
	applied, err := oldBinaryStored.Apply(s.cfg.UnifiedPayment)
	require.NoError(t, err)
	require.Equal(t, stored.Config.AppID, applied.AppID)
	_, err = repo.GetValue(ctx, unifiedPaymentBindingRevisionSetting)
	require.NoError(t, err)

	manual, err := s.UseManualUnifiedPayment(ctx, mutation.Status().Revision)
	require.NoError(t, err)
	require.NoError(t, finalizeBindingMutation(t, manual))
	_, err = repo.GetValue(ctx, unifiedPaymentBindingSetting)
	require.ErrorIs(t, err, ErrSettingNotFound, "old binary must fall back to deployment config after manual mode")
}

func TestUnifiedPaymentBindingRejectsDetectedLegacyWriterMetadataMismatch(t *testing.T) {
	s, repo, stored := bindingServiceFixture(t)
	ctx := context.Background()

	first, err := s.prepareUnifiedPaymentBindingMutation(ctx, 0, &stored)
	require.NoError(t, err)
	require.NoError(t, finalizeBindingMutation(t, first))
	metadataBefore, err := repo.GetValue(ctx, unifiedPaymentBindingRevisionSetting)
	require.NoError(t, err)

	// Simulate an old process that still writes the historical raw setting but
	// cannot update the new revision key. The service must fail closed instead
	// of silently rebasing the fence onto the uncoordinated write.
	legacyWriterValue := stored
	legacyWriterValue.SavedAt = legacyWriterValue.SavedAt.Add(time.Second)
	legacyRaw, err := json.Marshal(legacyWriterValue)
	require.NoError(t, err)
	require.NoError(t, repo.Set(ctx, unifiedPaymentBindingSetting, string(legacyRaw)))
	_, err = s.GetUnifiedPaymentBindingStatus(ctx)
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrUnifiedPaymentBindingMetadataMismatch), infraerrors.Code(err))
	metadataAfter, readErr := repo.GetValue(ctx, unifiedPaymentBindingRevisionSetting)
	require.NoError(t, readErr)
	require.Equal(t, metadataBefore, metadataAfter, "mismatch detection must not automatically rebase metadata")
}

func TestUnifiedPaymentBindingRejectsPoisonedStoredIdentityAndStoreFailure(t *testing.T) {
	for _, field := range []string{"app", "product", "environment", "origin", "return", "webhook", "key"} {
		t.Run(field, func(t *testing.T) {
			s, repo, stored := bindingServiceFixture(t)
			switch field {
			case "app":
				stored.Config.AppID = "app.other.sandbox"
			case "product":
				stored.Config.ProductID = "33333333-3333-4333-8333-333333333333"
			case "environment":
				stored.Config.Environment = unifiedpay.EnvironmentLive
			case "origin":
				stored.BaseURL = "https://evil.example"
			case "return":
				stored.Config.ReturnURLs = []string{"https://evil.example/"}
			case "webhook":
				stored.Config.WebhookEndpoints[0].URL = "https://evil.example/"
			case "key":
				stored.Config.WebhookSigningKeys[0].PublicKey = "not-a-key"
			}
			encoded, _ := json.Marshal(stored)
			require.NoError(t, repo.Set(context.Background(), unifiedPaymentBindingSetting, string(encoded)))
			gateway, err := s.loadUnifiedPaymentGateway(s.cfg)
			require.Error(t, err)
			require.Nil(t, gateway)
		})
	}
	s, repo, _ := bindingServiceFixture(t)
	repo.getValueErr = errors.New("synthetic storage outage")
	gateway, err := s.loadUnifiedPaymentGateway(s.cfg)
	require.Error(t, err)
	require.Nil(t, gateway)
}

func TestUnifiedPaymentBindingDelayedManualCannotEraseLaterSave(t *testing.T) {
	s, repo, stored := bindingServiceFixture(t)
	ctx := context.Background()
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, repo.Set(ctx, unifiedPaymentBindingSetting, string(encoded)))

	// This represents an old DELETE that had claimed its idempotency key but
	// had not reached the atomic finalizer before another admin saved a binding.
	oldManual, err := s.UseManualUnifiedPayment(ctx, 0)
	require.NoError(t, err)
	newer := stored
	newer.SavedAt = stored.SavedAt.Add(time.Second)
	laterSave, err := s.prepareUnifiedPaymentBindingMutation(ctx, 0, &newer)
	require.NoError(t, err)
	require.NoError(t, finalizeBindingMutation(t, laterSave))

	err = finalizeBindingMutation(t, oldManual)
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrUnifiedPaymentBindingVersionConflict), infraerrors.Code(err))
	current, err := s.GetUnifiedPaymentBindingStatus(ctx)
	require.NoError(t, err)
	require.True(t, current.Configured)
	require.Equal(t, uint64(1), current.Revision)
	require.NotNil(t, current.SavedAt)
	require.True(t, current.SavedAt.Equal(newer.SavedAt))
}

func TestUnifiedPaymentBindingStaleConcurrentSaveCannotOverwriteWinner(t *testing.T) {
	s, repo, stored := bindingServiceFixture(t)
	ctx := context.Background()
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, repo.Set(ctx, unifiedPaymentBindingSetting, string(encoded)))

	first := stored
	first.SavedAt = stored.SavedAt.Add(time.Second)
	second := stored
	second.SavedAt = stored.SavedAt.Add(2 * time.Second)
	firstSave, err := s.prepareUnifiedPaymentBindingMutation(ctx, 0, &first)
	require.NoError(t, err)
	staleSave, err := s.prepareUnifiedPaymentBindingMutation(ctx, 0, &second)
	require.NoError(t, err)
	require.NoError(t, finalizeBindingMutation(t, firstSave))

	err = finalizeBindingMutation(t, staleSave)
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrUnifiedPaymentBindingVersionConflict), infraerrors.Code(err))
	current, err := s.GetUnifiedPaymentBindingStatus(ctx)
	require.NoError(t, err)
	require.True(t, current.Configured)
	require.Equal(t, uint64(1), current.Revision)
	require.NotNil(t, current.SavedAt)
	require.True(t, current.SavedAt.Equal(first.SavedAt))
}

func TestUnifiedPaymentBindingMutationRequiresAtomicCommitRepository(t *testing.T) {
	s, repo, _ := bindingServiceFixture(t)
	// The normal fake supports atomic commits; remove that capability with a
	// minimal SettingRepository wrapper to prove the service fails before any
	// remote bind can be attempted.
	s.settingRepo = bindingReadOnlySettingRepository{SettingRepository: repo}
	_, err := s.UseManualUnifiedPayment(context.Background(), 0)
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrUnifiedPaymentBindingAtomicCommitUnavailable), infraerrors.Code(err))
}

type bindingReadOnlySettingRepository struct{ SettingRepository }
