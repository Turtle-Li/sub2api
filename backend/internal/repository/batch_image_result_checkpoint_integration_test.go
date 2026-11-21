//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBatchImageCheckpointPostgresSettlesWalletOnceAfterFinalizationOutage(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Balance: 10})
	cleanupCommittedFixture(t, `DELETE FROM users WHERE id = $1`, user.ID)
	account := mustCreateAccount(t, client, &service.Account{Name: "local delivery recovery fixture", Platform: service.PlatformOpenAI})
	cleanupCommittedFixture(t, `DELETE FROM accounts WHERE id = $1`, account.ID)
	repo := &batchImageRepository{db: integrationDB, sql: integrationDB}
	billing := NewUsageBillingRepository(client, integrationDB)
	batchID := batchImageTestID(t, "wallet-recovery")
	walletSlot, studioGroupID, holdAmount := int64(service.ImageStudioWalletOwnerSlot), int64(45), 0.30
	hold, err := billing.ReserveBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID: service.BatchImageHoldRequestID(batchID), APIKeyID: walletSlot, BatchID: batchID, UserID: user.ID, HoldAmount: holdAmount,
	})
	require.NoError(t, err)
	require.True(t, hold.Applied)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM usage_billing_dedup WHERE api_key_id = $1 AND request_id IN ($2, $3)`,
			walletSlot, service.BatchImageHoldRequestID(batchID), service.BatchImageCaptureRequestID(batchID))
		require.NoError(t, err)
	})
	_, err = repo.CreateBatchImageJob(ctx, service.CreateBatchImageJobParams{
		BatchID: batchID, UserID: user.ID, APIKeyID: &walletSlot, StudioGroupID: &studioGroupID, AccountID: &account.ID,
		Provider: service.BatchImageProviderOpenAI, Model: "gpt-image-2.6-sunburst", ImageSize: "2K",
		Status: service.BatchImageJobStatusSubmitted, ItemCount: 1, SuccessCount: 1, HoldAmount: &holdAmount,
		BillableUnitPrice: 0.20, PricingSnapshotVersion: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM batch_image_jobs WHERE batch_id = $1`, batchID)
		require.NoError(t, err)
	})
	_, err = repo.CreateBatchImageItem(ctx, service.CreateBatchImageItemParams{
		JobID: batchID, CustomID: "image-1", Status: service.BatchImageItemStatusSuccess,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpdateBatchImageJobProviderOutputRef(ctx, batchID, service.BatchImageShanghaiSourceRef))
	require.NoError(t, repo.TransitionBatchImageJobStatus(ctx, batchID, service.BatchImageJobStatusIndexing, service.BatchImageTransitionOptions{}))
	_, err = repo.SaveBatchImageCheckpoint(ctx, batchID, "image-1", service.BatchImageCheckpointFinal,
		service.BatchImageResultCheckpoint{Data: []byte("completed output"), MimeType: "image/png", Width: 2048, Height: 3072})
	require.NoError(t, err)
	require.NoError(t, repo.TransitionBatchImageJobStatus(ctx, batchID, service.BatchImageJobStatusSettling, service.BatchImageTransitionOptions{}))
	ack := service.BatchImageShanghaiAcknowledgement{BatchID: batchID, CustomID: "image-1"}
	acks, err := repo.ListBatchImageShanghaiAcknowledgements(ctx, 16)
	require.NoError(t, err)
	require.NotContains(t, acks, ack)
	require.ErrorIs(t, repo.TransitionBatchImageJobStatus(ctx, batchID, service.BatchImageJobStatusFailed, service.BatchImageTransitionOptions{}), service.ErrBatchImageDeliveryPending)
	settlement := &service.BatchImageSettlementService{Repo: failBatchImageSettlementFinalization{repo}, BillingRepo: billing, Pricing: &service.BatchImageModelPricingResolver{}}
	for range 6 {
		_, err := settlement.Settle(ctx, batchID)
		require.ErrorContains(t, err, "SETTLEMENT_FINALIZATION_FAILED")
	}
	staged, err := repo.GetBatchImageCheckpoint(ctx, batchID, "image-1", service.BatchImageCheckpointFinal)
	require.NoError(t, err)
	require.NotNil(t, staged)
	settlement = &service.BatchImageSettlementService{Repo: repo, BillingRepo: billing, Pricing: &service.BatchImageModelPricingResolver{}}
	result, err := settlement.Settle(ctx, batchID)
	require.NoError(t, err)
	require.InDelta(t, 0.20, result.ActualCost, 1e-9)
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 9.8, balance, 1e-9)
	require.Zero(t, frozen)
	job, err := repo.GetBatchImageJobByBatchID(ctx, batchID)
	require.NoError(t, err)
	require.Nil(t, job.LastErrorCode)
	require.Equal(t, service.BatchImageJobStatusCompleted, job.Status)
	acks, err = repo.ListBatchImageShanghaiAcknowledgements(ctx, 16)
	require.NoError(t, err)
	require.Contains(t, acks, ack)
	require.NoError(t, repo.ConfirmBatchImageShanghaiAcknowledgement(ctx, batchID))
	require.NoError(t, repo.ConfirmBatchImageShanghaiAcknowledgement(ctx, batchID))
	require.Error(t, repo.UpdateBatchImageJobProviderOutputRef(ctx, batchID, service.BatchImageShanghaiSourceRef))
	acks, err = repo.ListBatchImageShanghaiAcknowledgements(ctx, 16)
	require.NoError(t, err)
	require.NotContains(t, acks, ack)
	require.Equal(t, 360*time.Hour, job.OutputExpiresAt.Sub(*job.SettledAt))
	staged, err = repo.GetBatchImageCheckpoint(ctx, batchID, "image-1", service.BatchImageCheckpointFinal)
	require.NoError(t, err)
	require.Nil(t, staged)
}

type failBatchImageSettlementFinalization struct{ service.BatchImageRepository }

func (failBatchImageSettlementFinalization) MarkBatchImageJobSettled(context.Context, service.MarkBatchImageJobSettledParams) error {
	return errors.New("state commit unavailable after capture")
}

func TestBatchImageCheckpointPostgresImportsPrivateSpoolAfterDatabaseOutage(t *testing.T) {
	ctx := t.Context()
	spool := &batchImageCheckpointSpool{root: filepath.Join(t.TempDir(), "spool")}
	healthy := &batchImageRepository{db: integrationDB, sql: integrationDB, spool: spool}
	job, err := healthy.CreateBatchImageJob(ctx, service.CreateBatchImageJobParams{
		BatchID: batchImageTestID(t, "spool"), UserID: 1, ItemCount: 1, Model: "gpt-image-2.6-sunburst",
		Provider: service.BatchImageProviderOpenAI, Status: service.BatchImageJobStatusRunning, ImageSize: "2K",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM batch_image_jobs WHERE batch_id = $1`, job.BatchID)
	})
	const itemID = "image-1"
	_, err = healthy.CreateBatchImageItem(ctx, service.CreateBatchImageItemParams{JobID: job.BatchID, CustomID: itemID, Status: service.BatchImageItemStatusPending})
	require.NoError(t, err)
	disconnected, err := sql.Open("postgres", integrationDSN)
	require.NoError(t, err)
	require.NoError(t, disconnected.Close())
	workerDuringOutage := &batchImageRepository{db: disconnected, sql: disconnected, spool: spool}
	original := service.BatchImageResultCheckpoint{Data: []byte("{\"result\":\"original\"}\n"), MimeType: "application/x-ndjson"}
	_, err = workerDuringOutage.SaveBatchImageCheckpoint(ctx, job.BatchID, itemID, service.BatchImageCheckpointSource, original)
	require.ErrorIs(t, err, service.ErrBatchImageCheckpointImportPending)
	_, err = workerDuringOutage.SaveBatchImageCheckpoint(ctx, job.BatchID, itemID, service.BatchImageCheckpointSource,
		service.BatchImageResultCheckpoint{Data: []byte("duplicate worker"), MimeType: "application/x-ndjson"})
	require.ErrorIs(t, err, service.ErrBatchImageCheckpointImportPending)
	paths, err := spool.readyFiles(job.BatchID, service.BatchImageCheckpointSource)
	require.NoError(t, err)
	require.Len(t, paths, 1)
	info, err := os.Stat(paths[0])
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.ErrorIs(t, healthy.TransitionBatchImageJobStatus(ctx, job.BatchID, service.BatchImageJobStatusCancelled, service.BatchImageTransitionOptions{}), service.ErrBatchImageDeliveryPending)

	// There is no in-memory handoff from the worker that received the response.
	rebuilt := &batchImageRepository{db: integrationDB, sql: integrationDB, spool: &batchImageCheckpointSpool{root: spool.root}}
	ids, err := rebuilt.ListBatchImageCheckpointIDs(ctx, job.BatchID, service.BatchImageCheckpointSource)
	require.NoError(t, err)
	require.Contains(t, ids, itemID)
	checkpoint, err := rebuilt.GetBatchImageCheckpoint(ctx, job.BatchID, itemID, service.BatchImageCheckpointSource)
	require.NoError(t, err)
	require.Equal(t, &original, checkpoint)
	_, err = os.Stat(paths[0])
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, rebuilt.TransitionBatchImageJobStatus(ctx, job.BatchID, service.BatchImageJobStatusIndexing, service.BatchImageTransitionOptions{}))
	final := service.BatchImageResultCheckpoint{Data: []byte{137, 80, 78, 71}, MimeType: "image/png", Width: 2048, Height: 3072}
	_, err = workerDuringOutage.SaveBatchImageCheckpoint(ctx, job.BatchID, itemID, service.BatchImageCheckpointFinal, final)
	require.ErrorIs(t, err, service.ErrBatchImageCheckpointImportPending)
	checkpoint, err = rebuilt.GetBatchImageCheckpoint(ctx, job.BatchID, itemID, service.BatchImageCheckpointFinal)
	require.NoError(t, err)
	require.Equal(t, &final, checkpoint)
}

func TestBatchImageCheckpointPostgresAdvancesOnlyCurrentVertexTransport(t *testing.T) {
	ctx := t.Context()
	repo := &batchImageRepository{db: integrationDB, sql: integrationDB}
	job, err := repo.CreateBatchImageJob(ctx, service.CreateBatchImageJobParams{
		BatchID: batchImageTestID(t, "pump"), UserID: 1, ItemCount: 1, Model: "gemini-test",
		Provider: service.BatchImageProviderVertex, Status: service.BatchImageJobStatusIndexing, ImageSize: "1K",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM batch_image_jobs WHERE batch_id = $1`, job.BatchID)
	})
	readAttempt, err := repo.BatchImagePumpAttempt(ctx, job.BatchID)
	require.NoError(t, err)
	require.Zero(t, readAttempt)
	require.ErrorIs(t, repo.AdvanceBatchImagePumpAttempt(ctx, job.BatchID, 1), service.ErrBatchImageIndexStateConflict)
	require.NoError(t, repo.AdvanceBatchImagePumpAttempt(ctx, job.BatchID, 0))
	other := &batchImageRepository{db: integrationDB, sql: integrationDB}
	require.ErrorIs(t, other.AdvanceBatchImagePumpAttempt(ctx, job.BatchID, 0), service.ErrBatchImageIndexStateConflict)
	require.NoError(t, other.AdvanceBatchImagePumpAttempt(ctx, job.BatchID, 1))
	readAttempt, err = repo.BatchImagePumpAttempt(ctx, job.BatchID)
	require.NoError(t, err)
	require.Equal(t, 2, readAttempt)
	require.NoError(t, repo.TransitionBatchImageJobStatus(ctx, job.BatchID, service.BatchImageJobStatusSettling, service.BatchImageTransitionOptions{}))
	require.ErrorIs(t, repo.AdvanceBatchImagePumpAttempt(ctx, job.BatchID, 2), service.ErrBatchImageIndexStateConflict)
}

func TestBatchImageCheckpointPostgresSurvivesRepositoryRebuildAndFencesCancellation(t *testing.T) {
	ctx := t.Context()
	repo := &batchImageRepository{db: integrationDB, sql: integrationDB}
	job, err := repo.CreateBatchImageJob(ctx, service.CreateBatchImageJobParams{
		BatchID: batchImageTestID(t, "result-checkpoint"), UserID: 1, ItemCount: 1, Model: "gpt-image-2.6-sunburst",
		Provider: service.BatchImageProviderOpenAI, Status: service.BatchImageJobStatusRunning, ImageSize: "2K",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM batch_image_jobs WHERE batch_id = $1`, job.BatchID)
	})
	const customID = "image-1"
	_, err = repo.CreateBatchImageItem(ctx, service.CreateBatchImageItemParams{JobID: job.BatchID, CustomID: customID, Status: service.BatchImageItemStatusPending})
	require.NoError(t, err)
	available, err := repo.BatchImageCheckpointCapacityAvailable(ctx)
	require.NoError(t, err)
	require.True(t, available)
	original := service.BatchImageResultCheckpoint{Data: []byte("original JSONL\n"), MimeType: "application/x-ndjson"}
	created, err := repo.SaveBatchImageCheckpoint(ctx, job.BatchID, customID, service.BatchImageCheckpointSource, original)
	require.NoError(t, err)
	require.True(t, created)
	_, err = repo.SaveBatchImageCheckpoint(ctx, job.BatchID, "different-item", service.BatchImageCheckpointSource, original)
	require.ErrorIs(t, err, service.ErrBatchImageIndexStateConflict)

	repo = &batchImageRepository{db: integrationDB, sql: integrationDB}
	created, err = repo.SaveBatchImageCheckpoint(ctx, job.BatchID, customID, service.BatchImageCheckpointSource,
		service.BatchImageResultCheckpoint{Data: []byte("late conflicting answer"), MimeType: "application/x-ndjson"})
	require.NoError(t, err)
	require.False(t, created)
	checkpoint, err := repo.GetBatchImageCheckpoint(ctx, job.BatchID, customID, service.BatchImageCheckpointSource)
	require.NoError(t, err)
	require.Equal(t, &original, checkpoint)

	for _, status := range []string{service.BatchImageJobStatusCancelled, service.BatchImageJobStatusFailed} {
		require.ErrorIs(t, repo.TransitionBatchImageJobStatus(ctx, job.BatchID, status, service.BatchImageTransitionOptions{}), service.ErrBatchImageDeliveryPending)
	}
	for _, want := range []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second} {
		delay, err := repo.RecordBatchImageDeliveryFailure(ctx, job.BatchID, "provider_archive", "COS_UNAVAILABLE")
		require.NoError(t, err)
		require.Equal(t, want, delay)
	}
	laterRepo := &batchImageRepository{db: integrationDB, sql: integrationDB}
	delay, err := laterRepo.BatchImageDeliveryRetryAfter(ctx, job.BatchID)
	require.NoError(t, err)
	require.Greater(t, delay, 40*time.Second)
	require.LessOrEqual(t, delay, 45*time.Second)

	require.NoError(t, laterRepo.TransitionBatchImageJobStatus(ctx, job.BatchID, service.BatchImageJobStatusIndexing, service.BatchImageTransitionOptions{}))
	_, err = laterRepo.SaveBatchImageCheckpoint(ctx, job.BatchID, customID, service.BatchImageCheckpointSource, original)
	require.ErrorIs(t, err, service.ErrBatchImageIndexStateConflict)
	final := service.BatchImageResultCheckpoint{Data: []byte{0x89, 0x50, 0x4e, 0x47}, MimeType: "image/png", Width: 2048, Height: 3072}
	created, err = laterRepo.SaveBatchImageCheckpoint(ctx, job.BatchID, customID, service.BatchImageCheckpointFinal, final)
	require.NoError(t, err)
	require.True(t, created)
	checkpoint, err = laterRepo.GetBatchImageCheckpoint(ctx, job.BatchID, customID, service.BatchImageCheckpointFinal)
	require.NoError(t, err)
	require.Equal(t, &final, checkpoint)
	cleanup, err := laterRepo.ListBatchImageJobsDueForOutputCleanup(ctx, time.Now().Add(365*24*time.Hour), 1000)
	require.NoError(t, err)
	for _, candidate := range cleanup {
		require.NotEqual(t, job.BatchID, candidate.BatchID)
	}

	require.NoError(t, laterRepo.TransitionBatchImageJobStatus(ctx, job.BatchID, service.BatchImageJobStatusSettling, service.BatchImageTransitionOptions{}))
	expires := time.Now().Add(360 * time.Hour)
	require.NoError(t, laterRepo.MarkBatchImageJobSettled(ctx, service.MarkBatchImageJobSettledParams{
		BatchID: job.BatchID, ActualCost: 0.2, ManifestHash: "durable-delivery-" + job.BatchID, OutputExpiresAt: &expires,
	}))
	for _, stage := range []string{service.BatchImageCheckpointSource, service.BatchImageCheckpointFinal} {
		checkpoint, err := laterRepo.GetBatchImageCheckpoint(ctx, job.BatchID, customID, stage)
		require.NoError(t, err)
		require.Nil(t, checkpoint)
	}
	_, err = laterRepo.SaveBatchImageCheckpoint(ctx, job.BatchID, customID, service.BatchImageCheckpointFinal, final)
	require.ErrorIs(t, err, service.ErrBatchImageIndexStateConflict)
	_, err = laterRepo.BeginBatchImageUpscaleWrite(ctx, job.BatchID)
	require.ErrorIs(t, err, service.ErrBatchImageIndexStateConflict)
	delay, err = laterRepo.BatchImageDeliveryRetryAfter(ctx, job.BatchID)
	require.NoError(t, err)
	require.Zero(t, delay)
}

func TestOperationalAlertPostgresDedupeLeaseRecoveryAndDelivery(t *testing.T) {
	ctx := t.Context()
	outbox := NewOperationalAlertOutbox(integrationDB)
	alert := service.OperationalAlert{Component: "image_studio", Stage: "final_archive", SubjectID: uuid.NewString(), Code: "IMAGE_UPSCALE_STORE_FAILED"}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM operational_alert_outbox WHERE subject_id = $1`, alert.SubjectID)
	})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- outbox.EnqueueOperationalAlert(ctx, alert) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	first, err := outbox.ClaimOperationalAlert(ctx)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, alert, first.Alert)
	rebuilt := NewOperationalAlertOutbox(integrationDB)
	none, err := rebuilt.ClaimOperationalAlert(ctx)
	require.NoError(t, err)
	require.Nil(t, none)

	_, err = integrationDB.ExecContext(ctx, `UPDATE operational_alert_outbox SET retry_at = NOW() WHERE id = $1`, first.ID)
	require.NoError(t, err)
	second, err := rebuilt.ClaimOperationalAlert(ctx)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, first.ID, second.ID)
	require.NotEqual(t, first.Claim, second.Claim)
	require.NoError(t, outbox.FinishOperationalAlert(ctx, *first, true))
	require.NoError(t, rebuilt.FinishOperationalAlert(ctx, *second, false))
	var delivered bool
	var attempts int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT delivered_at IS NOT NULL, attempts FROM operational_alert_outbox WHERE id = $1`, second.ID).Scan(&delivered, &attempts))
	require.False(t, delivered, "a stale claim may not acknowledge a newer notification attempt")
	require.Equal(t, 2, attempts)
	none, err = rebuilt.ClaimOperationalAlert(ctx)
	require.NoError(t, err)
	require.Nil(t, none, "a refused message waits for backoff")
	_, err = integrationDB.ExecContext(ctx, `UPDATE operational_alert_outbox SET retry_at = NOW() WHERE id = $1`, first.ID)
	require.NoError(t, err)
	third, err := rebuilt.ClaimOperationalAlert(ctx)
	require.NoError(t, err)
	require.NotNil(t, third)
	require.NoError(t, rebuilt.FinishOperationalAlert(ctx, *third, true))
	require.NoError(t, outbox.EnqueueOperationalAlert(ctx, alert))
	none, err = outbox.ClaimOperationalAlert(ctx)
	require.NoError(t, err)
	require.Nil(t, none, "repeated reports of the same failure are deduplicated after delivery")
}
