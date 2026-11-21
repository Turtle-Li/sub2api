package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	BatchImageCheckpointSource = "provider_result"
	BatchImageCheckpointFinal  = "final_image"
)

// The private shared-volume copy survived, but its database commit must be reconciled.
var ErrBatchImageCheckpointImportPending = errors.New("batch image checkpoint import pending")

type BatchImageResultCheckpoint struct {
	Data          []byte
	MimeType      string
	Width, Height int
}

// The checkpoint store must not depend on the destination object store. Its
// first committed value for a job/item/stage is immutable until settlement.
type BatchImageResultCheckpointStore interface {
	GetBatchImageCheckpoint(context.Context, string, string, string) (*BatchImageResultCheckpoint, error)
	ListBatchImageCheckpointIDs(context.Context, string, string) (map[string]struct{}, error)
	SaveBatchImageCheckpoint(context.Context, string, string, string, BatchImageResultCheckpoint) (bool, error)
	BatchImageCheckpointCapacityAvailable(context.Context) (bool, error)
}

type BatchImageDeliveryRetryStore interface {
	BatchImageDeliveryRetryAfter(context.Context, string) (time.Duration, error)
	RecordBatchImageDeliveryFailure(ctx context.Context, batchID, stage, code string) (time.Duration, error)
}

type batchImageDeliveryFailure struct {
	stage string
	code  string
	cause error
}

func (e *batchImageDeliveryFailure) Error() string { return e.code }
func (e *batchImageDeliveryFailure) Unwrap() error { return e.cause }

func batchImageDeliveryError(stage, code string, cause error) error {
	return &batchImageDeliveryFailure{stage: stage, code: code, cause: cause}
}

// A response can arrive just as the worker's lease is lost. Finish the short,
// fenced checkpoint transaction, but never detach a provider call or COS publish.
func checkpointBatchImageResult(ctx context.Context, store BatchImageResultCheckpointStore, batchID, customID, stage string, value BatchImageResultCheckpoint) (bool, error) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	var err error
	for attempt := range 3 {
		var created bool
		attemptCtx, cancelAttempt := context.WithTimeout(persistCtx, 6*time.Second)
		created, err = store.SaveBatchImageCheckpoint(attemptCtx, batchID, customID, stage, value)
		cancelAttempt()
		if err == nil || errors.Is(err, ErrBatchImageIndexStateConflict) || errors.Is(err, ErrBatchImageCheckpointImportPending) {
			return created, err
		}
		if attempt < 2 {
			if waitErr := sleepWithContext(persistCtx, time.Duration(attempt+1)*250*time.Millisecond); waitErr != nil {
				return false, waitErr
			}
		}
	}
	return false, err
}

func (p *BatchImageProviderProcessor) retryDelivery(ctx context.Context, job *BatchImageJob, failure *batchImageDeliveryFailure) (BatchImageProcessResult, error) {
	if ctx.Err() != nil {
		return BatchImageProcessResult{}, ctx.Err()
	}
	slog.WarnContext(ctx, "batch_image.delivery_retry", "batch_id", job.BatchID, "provider", job.Provider, "stage", failure.stage, "code", failure.code)
	p.Alerts.observeBatchImage(ctx, job.BatchID, failure.stage, failure.code)
	delay := batchImageProviderErrorRequeue
	if store, ok := p.Repo.(BatchImageDeliveryRetryStore); ok {
		var err error
		delay, err = store.RecordBatchImageDeliveryFailure(ctx, job.BatchID, failure.stage, failure.code)
		if errors.Is(err, ErrBatchImageIndexStateConflict) {
			return BatchImageProcessResult{RequeueAfter: time.Millisecond}, nil
		}
		if err != nil {
			return BatchImageProcessResult{}, err
		}
	}
	return BatchImageProcessResult{RequeueAfter: delay}, nil
}

func (p *BatchImageProviderProcessor) deliveryDelay(ctx context.Context, batchID string) (time.Duration, error) {
	if store, ok := p.Repo.(BatchImageDeliveryRetryStore); ok {
		return store.BatchImageDeliveryRetryAfter(ctx, batchID)
	}
	return 0, nil
}
