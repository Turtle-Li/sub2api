package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// Keep this key in the old raw StoredIntegration form. Older binaries read
	// only this key, so a bind stays readable and manual mode stays a deletion.
	unifiedPaymentBindingSetting = "payment_unified_public_binding_v1"
	// The revision is deliberately a second, ignored-by-old-binaries setting.
	// It is the durable tombstone/fence for manual mode and new writers.
	unifiedPaymentBindingRevisionSetting = "payment_unified_public_binding_revision_v1"
	unifiedPaymentBindingRevisionSchema  = "sub2api.unified_payment_binding_revision.v1"

	// This was emitted only by an unreleased local implementation. Accept it on
	// read so a developer can convert it through the next guarded mutation; do
	// not emit it because deployed older binaries cannot consume it.
	unifiedPaymentBindingLegacyEnvelopeSchema = "sub2api.unified_payment_binding.v1"
)

var (
	ErrUnifiedPaymentBindingVersionConflict = infraerrors.Conflict(
		"UNIFIED_PAYMENT_BINDING_VERSION_CONFLICT",
		"unified payment binding changed; refresh and retry",
	)
	ErrUnifiedPaymentBindingMetadataMismatch = infraerrors.Conflict(
		"UNIFIED_PAYMENT_BINDING_METADATA_MISMATCH",
		"unified payment binding was changed by an incompatible writer; stop writers and restore an audited state",
	)
	ErrUnifiedPaymentBindingAtomicCommitUnavailable = infraerrors.ServiceUnavailable(
		"UNIFIED_PAYMENT_BINDING_ATOMIC_COMMIT_UNAVAILABLE",
		"unified payment binding cannot be committed safely right now",
	)
	ErrUnifiedPaymentBindingVersionExhausted = infraerrors.BadRequest(
		"UNIFIED_PAYMENT_BINDING_VERSION_EXHAUSTED",
		"unified payment binding revision cannot be incremented",
	)
)

type UnifiedPaymentBindingStatus struct {
	BaseURL        string     `json:"base_url"`
	AppID          string     `json:"app_id"`
	Environment    string     `json:"environment"`
	ReturnURL      string     `json:"return_url"`
	WebhookURL     string     `json:"webhook_url"`
	Revision       uint64     `json:"revision"`
	Configured     bool       `json:"configured"`
	BootstrapReady bool       `json:"bootstrap_ready"`
	RuntimeEnabled bool       `json:"runtime_enabled"`
	PendingRestart bool       `json:"pending_restart"`
	SavedAt        *time.Time `json:"saved_at,omitempty"`
}

// UnifiedPaymentBindingIdempotencyCommitOutcome explains why an atomic
// setting/idempotency transaction did not commit. The caller deliberately
// treats an old claim differently from a user-visible binding version clash.
type UnifiedPaymentBindingIdempotencyCommitOutcome uint8

const (
	UnifiedPaymentBindingIdempotencyCommitSucceeded UnifiedPaymentBindingIdempotencyCommitOutcome = iota + 1
	UnifiedPaymentBindingIdempotencyCommitVersionConflict
	UnifiedPaymentBindingIdempotencyCommitClaimLost
)

// UnifiedPaymentBindingIdempotencyCommit is written in one PostgreSQL
// transaction. Both raw binding state and the separate revision metadata use
// exact-value CAS; Claim identifies the particular idempotency lease that is
// allowed to publish the response.
type UnifiedPaymentBindingIdempotencyCommit struct {
	BindingKey            string
	BindingExpectedValue  string
	BindingValue          string
	BindingDelete         bool
	RevisionKey           string
	RevisionExpectedValue string
	RevisionValue         string
	Claim                 IdempotencyExecutionClaim
	ResponseStatus        int
	ResponseBody          string
	ExpiresAt             time.Time
}

// unifiedPaymentBindingAtomicCommitRepository is intentionally narrower than
// SettingRepository. Only this setting needs to atomically publish state and
// an idempotency response, so unrelated setting consumers keep their existing
// small repository contract.
type unifiedPaymentBindingAtomicCommitRepository interface {
	CommitUnifiedPaymentBindingAndIdempotencySuccess(
		ctx context.Context,
		commit UnifiedPaymentBindingIdempotencyCommit,
	) (UnifiedPaymentBindingIdempotencyCommitOutcome, error)
}

type persistedUnifiedPaymentBindingRevision struct {
	SchemaVersion string `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	BindingSHA256 string `json:"binding_sha256"`
}

// persistedUnifiedPaymentBindingLegacyEnvelope exists only to read the brief
// unreleased envelope format described above. New writes always use the legacy
// raw StoredIntegration key plus the separate revision metadata key.
type persistedUnifiedPaymentBindingLegacyEnvelope struct {
	SchemaVersion string                        `json:"schema_version"`
	Revision      uint64                        `json:"revision"`
	Integration   *unifiedpay.StoredIntegration `json:"integration"`
}

type unifiedPaymentBindingRecord struct {
	Raw         string
	Digest      string
	RevisionRaw string
	Revision    uint64
	Integration *unifiedpay.StoredIntegration
}

// UnifiedPaymentBindingMutation is returned to the durable idempotency
// coordinator after the central service call has completed. Its finalizer
// commits the local binding state and replay response together, avoiding a
// crash gap between a successful setting mutation and MarkSucceeded.
type UnifiedPaymentBindingMutation struct {
	status     UnifiedPaymentBindingStatus
	commit     UnifiedPaymentBindingIdempotencyCommit
	repository unifiedPaymentBindingAtomicCommitRepository
}

func (m *UnifiedPaymentBindingMutation) IdempotencyResponseData() any {
	if m == nil {
		return UnifiedPaymentBindingStatus{}
	}
	return m.status
}

func (m *UnifiedPaymentBindingMutation) Status() UnifiedPaymentBindingStatus {
	if m == nil {
		return UnifiedPaymentBindingStatus{}
	}
	return m.status
}

func (m *UnifiedPaymentBindingMutation) FinalizeIdempotencySuccess(
	ctx context.Context,
	claim IdempotencyExecutionClaim,
	responseStatus int,
	responseBody string,
	expiresAt time.Time,
) error {
	if m == nil || m.repository == nil {
		return ErrUnifiedPaymentBindingAtomicCommitUnavailable
	}
	commit := m.commit
	commit.Claim = claim
	commit.ResponseStatus = responseStatus
	commit.ResponseBody = responseBody
	commit.ExpiresAt = expiresAt

	outcome, err := m.repository.CommitUnifiedPaymentBindingAndIdempotencySuccess(ctx, commit)
	if err != nil {
		return ErrUnifiedPaymentBindingAtomicCommitUnavailable.WithCause(err)
	}
	switch outcome {
	case UnifiedPaymentBindingIdempotencyCommitSucceeded:
		return nil
	case UnifiedPaymentBindingIdempotencyCommitVersionConflict:
		return ErrUnifiedPaymentBindingVersionConflict
	case UnifiedPaymentBindingIdempotencyCommitClaimLost:
		return ErrIdempotencyInProgress
	default:
		return ErrUnifiedPaymentBindingAtomicCommitUnavailable
	}
}

func publicBindingDigest(raw string) string {
	if raw == "" {
		return ""
	}
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func (s *SettingService) readUnifiedPaymentBinding(ctx context.Context) (unifiedPaymentBindingRecord, error) {
	if s == nil || s.settingRepo == nil {
		return unifiedPaymentBindingRecord{}, unifiedpay.ErrInvalidConfiguration
	}

	record := unifiedPaymentBindingRecord{}
	raw, err := s.settingRepo.GetValue(ctx, unifiedPaymentBindingSetting)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return unifiedPaymentBindingRecord{}, err
	}
	if err == nil {
		record.Raw = raw
		record.Digest = publicBindingDigest(raw)

		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return unifiedPaymentBindingRecord{}, unifiedpay.ErrInvalidConfiguration
		}
		if schema, wrapped := fields["schema_version"]; wrapped {
			var version string
			if json.Unmarshal(schema, &version) != nil || version != unifiedPaymentBindingLegacyEnvelopeSchema {
				return unifiedPaymentBindingRecord{}, unifiedpay.ErrInvalidConfiguration
			}
			if _, hasIntegration := fields["integration"]; !hasIntegration {
				return unifiedPaymentBindingRecord{}, unifiedpay.ErrInvalidConfiguration
			}
			var legacy persistedUnifiedPaymentBindingLegacyEnvelope
			if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
				return unifiedPaymentBindingRecord{}, unifiedpay.ErrInvalidConfiguration
			}
			record.Revision = legacy.Revision
			record.Integration = legacy.Integration
		} else {
			var stored unifiedpay.StoredIntegration
			if err := json.Unmarshal([]byte(raw), &stored); err != nil {
				return unifiedPaymentBindingRecord{}, unifiedpay.ErrInvalidConfiguration
			}
			record.Integration = &stored
		}
	}

	revisionRaw, err := s.settingRepo.GetValue(ctx, unifiedPaymentBindingRevisionSetting)
	if errors.Is(err, ErrSettingNotFound) {
		return record, nil
	}
	if err != nil {
		return unifiedPaymentBindingRecord{}, err
	}
	var revision persistedUnifiedPaymentBindingRevision
	if err := json.Unmarshal([]byte(revisionRaw), &revision); err != nil || revision.SchemaVersion != unifiedPaymentBindingRevisionSchema || revision.BindingSHA256 != publicBindingDigest(record.Raw) {
		return unifiedPaymentBindingRecord{}, ErrUnifiedPaymentBindingMetadataMismatch
	}
	if record.Revision != 0 && record.Revision != revision.Revision {
		return unifiedPaymentBindingRecord{}, ErrUnifiedPaymentBindingMetadataMismatch
	}
	record.RevisionRaw = revisionRaw
	record.Revision = revision.Revision
	return record, nil
}

func (s *SettingService) loadUnifiedPaymentGateway(cfg *config.Config) (*unifiedpay.Gateway, error) {
	if s == nil || cfg == nil {
		return nil, unifiedpay.ErrInvalidConfiguration
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	record, err := s.readUnifiedPaymentBinding(ctx)
	if err != nil {
		return nil, err
	}
	copied := *cfg
	if record.Integration != nil {
		copied.UnifiedPayment, err = record.Integration.Apply(cfg.UnifiedPayment)
		if err != nil {
			return nil, err
		}
	}
	gateway, err := unifiedpay.NewFromAppConfig(&copied)
	if err != nil {
		return nil, err
	}
	s.unifiedPaymentAppliedDigest.Store(record.Digest)
	return gateway, nil
}

func (s *SettingService) bindingStatusForRecord(record unifiedPaymentBindingRecord) (UnifiedPaymentBindingStatus, error) {
	if s == nil || s.cfg == nil {
		return UnifiedPaymentBindingStatus{}, unifiedpay.ErrInvalidConfiguration
	}
	raw := s.cfg.UnifiedPayment
	webhook, _ := unifiedpay.ExpectedWebhookURL(raw)
	base := raw.BaseURL
	if base == "" {
		base = "https://pay.totools.cn"
	}
	applied, _ := s.unifiedPaymentAppliedDigest.Load().(string)
	result := UnifiedPaymentBindingStatus{
		BaseURL:        base,
		AppID:          raw.AppID,
		Environment:    raw.Environment,
		ReturnURL:      raw.ReturnURL,
		WebhookURL:     webhook,
		Revision:       record.Revision,
		RuntimeEnabled: raw.Enabled,
		PendingRestart: applied != record.Digest,
		BootstrapReady: raw.AppID != "" && raw.RequestKeyID != "" && raw.OrganizationID != "" && raw.ProductID != "" && raw.RequestPrivateKeyVaultRef != "" && raw.VaultAgentSocket != "" && webhook != "",
	}
	if record.Integration != nil {
		if _, err := record.Integration.Apply(raw); err != nil {
			return UnifiedPaymentBindingStatus{}, err
		}
		result.Configured = true
		result.SavedAt = &record.Integration.SavedAt
	}
	return result, nil
}

func (s *SettingService) GetUnifiedPaymentBindingStatus(ctx context.Context) (UnifiedPaymentBindingStatus, error) {
	record, err := s.readUnifiedPaymentBinding(ctx)
	if err != nil {
		return UnifiedPaymentBindingStatus{}, err
	}
	return s.bindingStatusForRecord(record)
}

func (s *SettingService) atomicUnifiedPaymentBindingRepository() (unifiedPaymentBindingAtomicCommitRepository, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrUnifiedPaymentBindingAtomicCommitUnavailable
	}
	repository, ok := s.settingRepo.(unifiedPaymentBindingAtomicCommitRepository)
	if !ok {
		return nil, ErrUnifiedPaymentBindingAtomicCommitUnavailable
	}
	return repository, nil
}

func nextUnifiedPaymentBindingRecord(current unifiedPaymentBindingRecord, integration *unifiedpay.StoredIntegration) (unifiedPaymentBindingRecord, error) {
	if current.Revision == ^uint64(0) {
		return unifiedPaymentBindingRecord{}, ErrUnifiedPaymentBindingVersionExhausted
	}
	next := unifiedPaymentBindingRecord{Revision: current.Revision + 1, Integration: integration}
	if integration != nil {
		encoded, err := json.Marshal(integration)
		if err != nil {
			return unifiedPaymentBindingRecord{}, err
		}
		next.Raw = string(encoded)
		next.Digest = publicBindingDigest(next.Raw)
	}
	revisionRaw, err := json.Marshal(persistedUnifiedPaymentBindingRevision{
		SchemaVersion: unifiedPaymentBindingRevisionSchema,
		Revision:      next.Revision,
		BindingSHA256: publicBindingDigest(next.Raw),
	})
	if err != nil {
		return unifiedPaymentBindingRecord{}, err
	}
	next.RevisionRaw = string(revisionRaw)
	return next, nil
}

func (s *SettingService) newUnifiedPaymentBindingMutation(
	current unifiedPaymentBindingRecord,
	expectedRevision uint64,
	integration *unifiedpay.StoredIntegration,
) (*UnifiedPaymentBindingMutation, error) {
	if current.Revision != expectedRevision {
		return nil, ErrUnifiedPaymentBindingVersionConflict
	}
	repository, err := s.atomicUnifiedPaymentBindingRepository()
	if err != nil {
		return nil, err
	}
	next, err := nextUnifiedPaymentBindingRecord(current, integration)
	if err != nil {
		return nil, err
	}
	status, err := s.bindingStatusForRecord(next)
	if err != nil {
		return nil, err
	}
	return &UnifiedPaymentBindingMutation{
		status: status,
		commit: UnifiedPaymentBindingIdempotencyCommit{
			BindingKey:            unifiedPaymentBindingSetting,
			BindingExpectedValue:  current.Raw,
			BindingValue:          next.Raw,
			BindingDelete:         integration == nil,
			RevisionKey:           unifiedPaymentBindingRevisionSetting,
			RevisionExpectedValue: current.RevisionRaw,
			RevisionValue:         next.RevisionRaw,
		},
		repository: repository,
	}, nil
}

func (s *SettingService) prepareUnifiedPaymentBindingMutation(
	ctx context.Context,
	expectedRevision uint64,
	integration *unifiedpay.StoredIntegration,
) (*UnifiedPaymentBindingMutation, error) {
	current, err := s.readUnifiedPaymentBinding(ctx)
	if err != nil {
		return nil, err
	}
	return s.newUnifiedPaymentBindingMutation(current, expectedRevision, integration)
}

// BindUnifiedPayment validates/synchronizes with the central service outside a
// local database transaction. The returned mutation is committed with the
// idempotency response by the coordinator; a concurrent local change is
// rejected by exact raw/revision CAS instead of being overwritten.
func (s *SettingService) BindUnifiedPayment(
	ctx context.Context,
	base, code, idempotencyKey string,
	expectedRevision uint64,
) (*UnifiedPaymentBindingMutation, error) {
	if s == nil || s.cfg == nil || s.settingRepo == nil {
		return nil, unifiedpay.ErrInvalidConfiguration
	}
	// Fail before the remote call when this request was already stale, and also
	// verify the atomic persistence capability before issuing an external sync.
	current, err := s.readUnifiedPaymentBinding(ctx)
	if err != nil {
		return nil, err
	}
	if current.Revision != expectedRevision {
		return nil, ErrUnifiedPaymentBindingVersionConflict
	}
	if _, err := s.atomicUnifiedPaymentBindingRepository(); err != nil {
		return nil, err
	}
	stored, err := unifiedpay.SynchronizeIntegration(ctx, s.cfg.UnifiedPayment, base, code, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return s.newUnifiedPaymentBindingMutation(current, expectedRevision, &stored)
}

// UseManualUnifiedPayment deletes the legacy raw binding key so an older
// binary continues to use its deployment configuration. Its separate revision
// metadata remains as a durable tombstone that fences delayed DELETE retries.
func (s *SettingService) UseManualUnifiedPayment(ctx context.Context, expectedRevision uint64) (*UnifiedPaymentBindingMutation, error) {
	if s == nil || s.settingRepo == nil {
		return nil, unifiedpay.ErrInvalidConfiguration
	}
	return s.prepareUnifiedPaymentBindingMutation(ctx, expectedRevision, nil)
}
