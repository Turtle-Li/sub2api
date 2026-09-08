//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const unifiedPaymentBindingRevisionTestSchema = "sub2api.unified_payment_binding_revision.v1"

type unifiedPaymentBindingRevisionTestValue struct {
	SchemaVersion string `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	BindingSHA256 string `json:"binding_sha256"`
}

func newBindingAtomicIdempotencyRecord(
	t *testing.T,
	repo *idempotencyRepository,
	prefix string,
	lockedUntil time.Time,
) (*service.IdempotencyRecord, service.IdempotencyExecuteOptions) {
	t.Helper()
	identity := sha256.Sum256([]byte(uniqueTestValue(t, prefix)))
	token := hex.EncodeToString(identity[:24])
	opts := service.IdempotencyExecuteOptions{
		Scope:          "binding-scope-" + token,
		ActorScope:     "admin:42",
		Method:         "POST",
		Route:          "/api/v1/admin/settings/unified-payment-binding",
		IdempotencyKey: "binding-key-" + token,
		Payload:        map[string]any{"operation": prefix, "revision": 0},
		RequireKey:     true,
	}
	fingerprint, err := service.BuildIdempotencyFingerprint(opts.Method, opts.Route, opts.ActorScope, opts.Payload)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiresAt := now.Add(24 * time.Hour)
	record := &service.IdempotencyRecord{
		Scope:              opts.Scope,
		IdempotencyKeyHash: service.HashIdempotencyKey(opts.IdempotencyKey),
		RequestFingerprint: fingerprint,
		Status:             service.IdempotencyStatusProcessing,
		LockedUntil:        &lockedUntil,
		ExpiresAt:          expiresAt,
	}
	owner, err := repo.CreateProcessing(context.Background(), record)
	require.NoError(t, err)
	require.True(t, owner)
	return record, opts
}

func bindingAtomicClaim(record *service.IdempotencyRecord) service.IdempotencyExecutionClaim {
	return service.IdempotencyExecutionClaim{
		ID:                 record.ID,
		RequestFingerprint: record.RequestFingerprint,
		LockedUntil:        *record.LockedUntil,
		ExpiresAt:          record.ExpiresAt,
	}
}

func bindingRevisionValue(t *testing.T, revision uint64, bindingRaw string) string {
	t.Helper()
	digest := ""
	if bindingRaw != "" {
		sum := sha256.Sum256([]byte(bindingRaw))
		digest = hex.EncodeToString(sum[:])
	}
	encoded, err := json.Marshal(unifiedPaymentBindingRevisionTestValue{
		SchemaVersion: unifiedPaymentBindingRevisionTestSchema,
		Revision:      revision,
		BindingSHA256: digest,
	})
	require.NoError(t, err)
	return string(encoded)
}

func uniqueBindingSettingKey(t *testing.T, prefix string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(uniqueTestValue(t, prefix)))
	return "unified-binding-" + hex.EncodeToString(sum[:24])
}

func bindingAtomicCommit(
	bindingKey, bindingExpected, bindingValue string,
	bindingDelete bool,
	revisionKey, revisionExpected, revisionValue string,
	record *service.IdempotencyRecord,
	response string,
) service.UnifiedPaymentBindingIdempotencyCommit {
	return service.UnifiedPaymentBindingIdempotencyCommit{
		BindingKey:            bindingKey,
		BindingExpectedValue:  bindingExpected,
		BindingValue:          bindingValue,
		BindingDelete:         bindingDelete,
		RevisionKey:           revisionKey,
		RevisionExpectedValue: revisionExpected,
		RevisionValue:         revisionValue,
		Claim:                 bindingAtomicClaim(record),
		ResponseStatus:        200,
		ResponseBody:          response,
		ExpiresAt:             record.ExpiresAt,
	}
}

func cleanupBindingSettings(t *testing.T, ctx context.Context, bindingKey, revisionKey string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM settings WHERE key IN ($1, $2)", bindingKey, revisionKey)
	})
}

func requireBindingSettingAbsent(t *testing.T, settings *settingRepository, ctx context.Context, key string) {
	t.Helper()
	_, err := settings.GetValue(ctx, key)
	require.ErrorIs(t, err, service.ErrSettingNotFound)
}

func TestUnifiedPaymentBindingAtomicCommitPostgresReplaysAfterResponseLoss(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(testEntClient(t)).(*settingRepository)
	idempotency := &idempotencyRepository{sql: integrationDB}
	bindingKey := uniqueBindingSettingKey(t, "atomic-lost-response-binding")
	revisionKey := uniqueBindingSettingKey(t, "atomic-lost-response-revision")
	cleanupBindingSettings(t, ctx, bindingKey, revisionKey)

	oldBinding := `{"version":0}`
	oldRevision := bindingRevisionValue(t, 4, oldBinding)
	newBinding := `{"version":1}`
	newRevision := bindingRevisionValue(t, 5, newBinding)
	require.NoError(t, settings.Set(ctx, bindingKey, oldBinding))
	require.NoError(t, settings.Set(ctx, revisionKey, oldRevision))
	lockedUntil := time.Now().UTC().Truncate(time.Microsecond).Add(30 * time.Second)
	record, opts := newBindingAtomicIdempotencyRecord(t, idempotency, "atomic-lost-response", lockedUntil)
	responseBody, err := json.Marshal(map[string]any{"configured": true, "revision": 5})
	require.NoError(t, err)

	outcome, err := settings.CommitUnifiedPaymentBindingAndIdempotencySuccess(
		ctx,
		bindingAtomicCommit(bindingKey, oldBinding, newBinding, false, revisionKey, oldRevision, newRevision, record, string(responseBody)),
	)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedPaymentBindingIdempotencyCommitSucceeded, outcome)
	storedBinding, err := settings.GetValue(ctx, bindingKey)
	require.NoError(t, err)
	require.Equal(t, newBinding, storedBinding)
	storedRevision, err := settings.GetValue(ctx, revisionKey)
	require.NoError(t, err)
	require.Equal(t, newRevision, storedRevision)

	// A response lost after commit must replay the committed response without
	// running the executor or attempting another settings mutation.
	executorCalls := 0
	coordinator := service.NewIdempotencyCoordinator(idempotency, service.DefaultIdempotencyConfig())
	replayed, err := coordinator.Execute(ctx, opts, func(context.Context) (any, error) {
		executorCalls++
		return map[string]any{"unexpected": true}, nil
	})
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, 0, executorCalls)
}

func TestUnifiedPaymentBindingAtomicCommitPostgresRollsBackStaleClaimAndConcurrentSave(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(testEntClient(t)).(*settingRepository)
	idempotency := &idempotencyRepository{sql: integrationDB}

	t.Run("stale finalizer rolls back both setting writes", func(t *testing.T) {
		bindingKey := uniqueBindingSettingKey(t, "atomic-stale-binding")
		revisionKey := uniqueBindingSettingKey(t, "atomic-stale-revision")
		cleanupBindingSettings(t, ctx, bindingKey, revisionKey)
		oldBinding := `{"version":7}`
		oldRevision := bindingRevisionValue(t, 7, oldBinding)
		newBinding := `{"version":8}`
		newRevision := bindingRevisionValue(t, 8, newBinding)
		require.NoError(t, settings.Set(ctx, bindingKey, oldBinding))
		require.NoError(t, settings.Set(ctx, revisionKey, oldRevision))

		now := time.Now().UTC().Truncate(time.Microsecond)
		oldLock := now.Add(-time.Second)
		record, _ := newBindingAtomicIdempotencyRecord(t, idempotency, "atomic-stale", oldLock)
		oldClaim := bindingAtomicClaim(record)
		newLock := now.Add(30 * time.Second)
		newExpires := now.Add(48 * time.Hour)
		reclaimed, err := idempotency.TryReclaim(ctx, record.ID, service.IdempotencyStatusProcessing, now, newLock, newExpires)
		require.NoError(t, err)
		require.True(t, reclaimed)

		commit := bindingAtomicCommit(bindingKey, oldBinding, newBinding, false, revisionKey, oldRevision, newRevision, record, `{"revision":8}`)
		commit.Claim = oldClaim
		outcome, err := settings.CommitUnifiedPaymentBindingAndIdempotencySuccess(ctx, commit)
		require.NoError(t, err)
		require.Equal(t, service.UnifiedPaymentBindingIdempotencyCommitClaimLost, outcome)
		storedBinding, err := settings.GetValue(ctx, bindingKey)
		require.NoError(t, err)
		require.Equal(t, oldBinding, storedBinding, "claim loss must roll back the binding update")
		storedRevision, err := settings.GetValue(ctx, revisionKey)
		require.NoError(t, err)
		require.Equal(t, oldRevision, storedRevision, "claim loss must roll back the revision update")
		got, err := idempotency.GetByScopeAndKeyHash(ctx, record.Scope, record.IdempotencyKeyHash)
		require.NoError(t, err)
		require.Equal(t, service.IdempotencyStatusProcessing, got.Status)
		require.NotNil(t, got.LockedUntil)
		require.True(t, got.LockedUntil.Equal(newLock))
	})

	t.Run("two concurrent saves with the same revision have one winner", func(t *testing.T) {
		bindingKey := uniqueBindingSettingKey(t, "atomic-concurrent-binding")
		revisionKey := uniqueBindingSettingKey(t, "atomic-concurrent-revision")
		cleanupBindingSettings(t, ctx, bindingKey, revisionKey)
		oldBinding := `{"version":7}`
		oldRevision := bindingRevisionValue(t, 7, oldBinding)
		require.NoError(t, settings.Set(ctx, bindingKey, oldBinding))
		require.NoError(t, settings.Set(ctx, revisionKey, oldRevision))
		lockedUntil := time.Now().UTC().Truncate(time.Microsecond).Add(30 * time.Second)
		firstRecord, _ := newBindingAtomicIdempotencyRecord(t, idempotency, "atomic-concurrent-first", lockedUntil)
		secondRecord, _ := newBindingAtomicIdempotencyRecord(t, idempotency, "atomic-concurrent-second", lockedUntil)
		firstBinding := `{"version":11}`
		firstRevision := bindingRevisionValue(t, 8, firstBinding)
		secondBinding := `{"version":22}`
		secondRevision := bindingRevisionValue(t, 8, secondBinding)

		type result struct {
			outcome service.UnifiedPaymentBindingIdempotencyCommitOutcome
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		var wg sync.WaitGroup
		for _, attempt := range []struct {
			record   *service.IdempotencyRecord
			binding  string
			revision string
		}{
			{record: firstRecord, binding: firstBinding, revision: firstRevision},
			{record: secondRecord, binding: secondBinding, revision: secondRevision},
		} {
			attempt := attempt
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				outcome, err := settings.CommitUnifiedPaymentBindingAndIdempotencySuccess(
					ctx,
					bindingAtomicCommit(bindingKey, oldBinding, attempt.binding, false, revisionKey, oldRevision, attempt.revision, attempt.record, `{"revision":8}`),
				)
				results <- result{outcome: outcome, err: err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		successes := 0
		versionConflicts := 0
		for result := range results {
			require.NoError(t, result.err)
			switch result.outcome {
			case service.UnifiedPaymentBindingIdempotencyCommitSucceeded:
				successes++
			case service.UnifiedPaymentBindingIdempotencyCommitVersionConflict:
				versionConflicts++
			default:
				t.Fatalf("unexpected atomic commit outcome: %v", result.outcome)
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, versionConflicts)
		storedBinding, err := settings.GetValue(ctx, bindingKey)
		require.NoError(t, err)
		storedRevision, err := settings.GetValue(ctx, revisionKey)
		require.NoError(t, err)
		switch storedBinding {
		case firstBinding:
			require.Equal(t, firstRevision, storedRevision)
		case secondBinding:
			require.Equal(t, secondRevision, storedRevision)
		default:
			t.Fatalf("unexpected winning binding: %s", storedBinding)
		}
	})
}

func TestUnifiedPaymentBindingAtomicCommitPostgresAbsentManualABA(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(testEntClient(t)).(*settingRepository)
	idempotency := &idempotencyRepository{sql: integrationDB}
	bindingKey := uniqueBindingSettingKey(t, "atomic-absent-binding")
	revisionKey := uniqueBindingSettingKey(t, "atomic-absent-revision")
	cleanupBindingSettings(t, ctx, bindingKey, revisionKey)
	lockedUntil := time.Now().UTC().Truncate(time.Microsecond).Add(30 * time.Second)

	// Establish a durable manual tombstone while the legacy raw key is absent.
	initialManualRecord, _ := newBindingAtomicIdempotencyRecord(t, idempotency, "atomic-absent-initial-manual", lockedUntil)
	manualRevisionOne := bindingRevisionValue(t, 1, "")
	outcome, err := settings.CommitUnifiedPaymentBindingAndIdempotencySuccess(
		ctx,
		bindingAtomicCommit(bindingKey, "", "", true, revisionKey, "", manualRevisionOne, initialManualRecord, `{"revision":1}`),
	)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedPaymentBindingIdempotencyCommitSucceeded, outcome)
	requireBindingSettingAbsent(t, settings, ctx, bindingKey)
	storedRevision, err := settings.GetValue(ctx, revisionKey)
	require.NoError(t, err)
	require.Equal(t, manualRevisionOne, storedRevision)

	// A delayed DELETE sees the same absent raw key as a later bind. The raw
	// absence alone is therefore not a fence; the paired revision CAS is.
	delayedManualRecord, _ := newBindingAtomicIdempotencyRecord(t, idempotency, "atomic-absent-delayed-manual", lockedUntil)
	rebindRecord, _ := newBindingAtomicIdempotencyRecord(t, idempotency, "atomic-absent-rebind", lockedUntil)
	reboundBinding := `{"version":3}`
	reboundRevision := bindingRevisionValue(t, 2, reboundBinding)
	delayedManualRevision := bindingRevisionValue(t, 2, "")

	outcome, err = settings.CommitUnifiedPaymentBindingAndIdempotencySuccess(
		ctx,
		bindingAtomicCommit(bindingKey, "", reboundBinding, false, revisionKey, manualRevisionOne, reboundRevision, rebindRecord, `{"revision":2,"configured":true}`),
	)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedPaymentBindingIdempotencyCommitSucceeded, outcome)

	outcome, err = settings.CommitUnifiedPaymentBindingAndIdempotencySuccess(
		ctx,
		bindingAtomicCommit(bindingKey, "", "", true, revisionKey, manualRevisionOne, delayedManualRevision, delayedManualRecord, `{"revision":2,"configured":false}`),
	)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedPaymentBindingIdempotencyCommitVersionConflict, outcome)
	storedBinding, err := settings.GetValue(ctx, bindingKey)
	require.NoError(t, err)
	require.Equal(t, reboundBinding, storedBinding, "stale DELETE must not erase the rebound legacy binding")
	storedRevision, err = settings.GetValue(ctx, revisionKey)
	require.NoError(t, err)
	require.Equal(t, reboundRevision, storedRevision, "stale DELETE must not restore its old manual revision")
}

// Exercise the public coordinator's lease recovery, not a direct TryReclaim call.
type bindingRecoveryFinalizer struct {
	repository *settingRepository
	commit     service.UnifiedPaymentBindingIdempotencyCommit
}

func (f *bindingRecoveryFinalizer) IdempotencyResponseData() any {
	return map[string]any{"revision": 1}
}
func (f *bindingRecoveryFinalizer) FinalizeIdempotencySuccess(ctx context.Context, claim service.IdempotencyExecutionClaim, status int, body string, expires time.Time) error {
	commit := f.commit
	commit.Claim = claim
	commit.ResponseStatus = status
	commit.ResponseBody = body
	commit.ExpiresAt = expires
	outcome, err := f.repository.CommitUnifiedPaymentBindingAndIdempotencySuccess(ctx, commit)
	if err != nil {
		return err
	}
	if outcome != service.UnifiedPaymentBindingIdempotencyCommitSucceeded {
		return service.ErrIdempotencyInProgress
	}
	return nil
}
func TestUnifiedPaymentBindingAtomicCommitPostgresCoordinatorRecoversExpiredLease(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(testEntClient(t)).(*settingRepository)
	idempotency := &idempotencyRepository{sql: integrationDB}
	bindingKey := uniqueBindingSettingKey(t, "lease-recovery-binding")
	revisionKey := uniqueBindingSettingKey(t, "lease-recovery-revision")
	cleanupBindingSettings(t, ctx, bindingKey, revisionKey)
	// Model a crashed API process: its lease ended, but replay retention is 24h.
	record, opts := newBindingAtomicIdempotencyRecord(t, idempotency, "lease-recovery", time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second))
	require.True(t, record.ExpiresAt.After(time.Now().Add(23*time.Hour)))
	opts.RequireFencedFinalizer = true
	newBinding := `{"version":1}`
	newRevision := bindingRevisionValue(t, 1, newBinding)
	coordinator := service.NewIdempotencyCoordinator(idempotency, service.DefaultIdempotencyConfig())
	calls := 0
	execute := func(context.Context) (any, error) {
		calls++
		return &bindingRecoveryFinalizer{repository: settings, commit: bindingAtomicCommit(bindingKey, "", newBinding, false, revisionKey, "", newRevision, record, "")}, nil
	}
	result, err := coordinator.Execute(ctx, opts, execute)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	got, err := settings.GetValue(ctx, bindingKey)
	require.NoError(t, err)
	require.Equal(t, newBinding, got)
	got, err = settings.GetValue(ctx, revisionKey)
	require.NoError(t, err)
	require.Equal(t, newRevision, got)
	replay, err := coordinator.Execute(ctx, opts, execute)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, 1, calls)
}
