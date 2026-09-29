package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	batchImageOpenAIManifestVersion   = 1
	batchImageOpenAIInputMaxBytes     = 256 * 1024 * 1024
	batchImageOpenAIResultMaxBytes    = 32 * 1024 * 1024
	batchImageOpenAIMaxItems          = 10000
	batchImageOpenAIContinueDelay     = 250 * time.Millisecond
	batchImageOpenAIAccountBusyDelay  = 2 * time.Second
	batchImageOpenAIItemTimeout       = 20 * time.Minute
	batchImageOpenAIProviderJobPrefix = "openai-images:"
)

var (
	errBatchImageOpenAIAccountBusy           = errors.New("OpenAI image batch account is busy")
	errBatchImageOpenAICancelled             = errors.New("OpenAI image batch was cancelled before the next upstream attempt")
	errBatchImageOpenAIAttemptAlreadyClaimed = errors.New("OpenAI image batch item attempt was already claimed")
)

type openAIImageBatchExecutionContextKey struct{}

func withOpenAIImageBatchExecution(ctx context.Context) context.Context {
	return context.WithValue(ctx, openAIImageBatchExecutionContextKey{}, true)
}

func isOpenAIImageBatchExecution(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(openAIImageBatchExecutionContextKey{}).(bool)
	return value
}

type OpenAIImageBatchItemRequest struct {
	BatchID          string
	CustomID         string
	Model            string
	Prompt           string
	ResponseMimeType string
	AspectRatio      string
	ReferenceImages  []BatchImageReference
}

type OpenAIImageBatchItemResult struct {
	Base64Data string
	MimeType   string
}

// OpenAIImageBatchItemExecutor runs exactly one n=1 image request. The hook is
// invoked after account concurrency admission and immediately before any
// non-idempotent upstream work.
type OpenAIImageBatchItemExecutor interface {
	Execute(
		ctx context.Context,
		account *Account,
		request OpenAIImageBatchItemRequest,
		beforeUpstream func() error,
	) (*OpenAIImageBatchItemResult, error)
}

type openAIImageBatchManifest struct {
	Version int             `json:"version"`
	Input   BatchImageInput `json:"input"`
}

type OpenAIImagesBatchProvider struct {
	store                BatchImageProviderObjectStore
	executor             OpenAIImageBatchItemExecutor
	objectPrefix         string
	itemExecutionTimeout time.Duration
}

func NewOpenAIImagesBatchProvider(store BatchImageProviderObjectStore, executor OpenAIImageBatchItemExecutor, objectPrefix string) *OpenAIImagesBatchProvider {
	return &OpenAIImagesBatchProvider{
		store:                store,
		executor:             executor,
		objectPrefix:         strings.Trim(strings.TrimSpace(objectPrefix), "/"),
		itemExecutionTimeout: batchImageOpenAIItemTimeout,
	}
}

func (p *OpenAIImagesBatchProvider) Name() string {
	return BatchImageProviderOpenAI
}

func (p *OpenAIImagesBatchProvider) SupportsAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformOpenAI {
		return false
	}
	switch account.Type {
	case AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey:
		return true
	default:
		return false
	}
}

func (p *OpenAIImagesBatchProvider) SubmissionAvailable() bool {
	return p != nil && p.store != nil && p.hasSafeObjectPrefix()
}

func (p *OpenAIImagesBatchProvider) Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error) {
	if !p.SupportsAccount(account) {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	if p == nil || p.store == nil || !p.hasSafeObjectPrefix() {
		return nil, ErrBatchImageProviderStorageUnavailable
	}
	if input.BatchID == "" && job != nil {
		input.BatchID = job.BatchID
	}
	if input.Model == "" && job != nil {
		input.Model = job.Model
	}
	if !isSafeOpenAIImageBatchID(input.BatchID) || !isOpenAIImageBatchModel(input.Model) || len(input.Items) == 0 || len(input.Items) > batchImageOpenAIMaxItems {
		return nil, ErrBatchImageProviderInvalidInput
	}
	if _, err := batchImageOpenAI1KSizeForAspectRatio(input.AspectRatio); err != nil {
		return nil, err
	}
	for _, item := range input.Items {
		if strings.TrimSpace(item.CustomID) == "" || strings.TrimSpace(item.Prompt) == "" {
			return nil, ErrBatchImageProviderInvalidInput
		}
		for _, ref := range item.ReferenceImages {
			if len(ref.Data) == 0 || strings.TrimSpace(ref.FileURI) != "" {
				return nil, ErrBatchImageProviderInvalidInput
			}
		}
	}

	payload, err := json.Marshal(openAIImageBatchManifest{Version: batchImageOpenAIManifestVersion, Input: input})
	if err != nil || len(payload) == 0 || len(payload) > batchImageOpenAIInputMaxBytes {
		return nil, ErrBatchImageProviderInvalidInput.WithCause(err)
	}
	inputKey := p.inputKey(input.BatchID)
	if err := p.store.Put(ctx, inputKey, "application/json", bytes.NewReader(payload), int64(len(payload))); err != nil {
		return nil, ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	return &BatchProviderJob{
		ProviderJobName:  batchImageOpenAIProviderJobPrefix + input.BatchID,
		ProviderInputRef: inputKey,
		RawState:         "QUEUED",
	}, nil
}

func (p *OpenAIImagesBatchProvider) Get(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error) {
	return p.status(ctx, job, account, false)
}

func (p *OpenAIImagesBatchProvider) Advance(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error) {
	return p.status(ctx, job, account, true)
}

func (p *OpenAIImagesBatchProvider) status(ctx context.Context, job *BatchImageJob, account *Account, advance bool) (*BatchProviderStatus, error) {
	if !p.SupportsAccount(account) {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	if p == nil || p.store == nil || !p.hasSafeObjectPrefix() {
		return nil, ErrBatchImageProviderStorageUnavailable
	}
	batchID, err := batchImageOpenAIJobBatchID(job)
	if err != nil {
		return nil, err
	}
	outputKey := p.outputKey(batchID)
	if exists, existsErr := p.store.Exists(ctx, outputKey); existsErr != nil {
		return nil, ErrBatchImageProviderStorageUnavailable.WithCause(existsErr)
	} else if exists {
		if cleanupErr := p.cleanupItemObjects(ctx, batchID, job.ItemCount); cleanupErr != nil {
			return nil, cleanupErr
		}
		return batchImageOpenAISucceededStatus(outputKey), nil
	}
	if cancelled, cancelErr := p.cancelRequested(ctx, batchID); cancelErr != nil {
		return nil, cancelErr
	} else if cancelled {
		return &BatchProviderStatus{RawState: "CANCELLED", InternalState: BatchProviderStateCancelled, Done: true}, nil
	}

	manifest, err := p.openManifest(ctx, job, batchID)
	if err != nil {
		return nil, err
	}
	for itemIndex, item := range manifest.Input.Items {
		resultKey := p.itemResultKey(batchID, itemIndex)
		if exists, existsErr := p.store.Exists(ctx, resultKey); existsErr != nil {
			return nil, ErrBatchImageProviderStorageUnavailable.WithCause(existsErr)
		} else if exists {
			continue
		}

		attemptKey := p.itemAttemptKey(batchID, itemIndex)
		if started, startedErr := p.store.Exists(ctx, attemptKey); startedErr != nil {
			return nil, ErrBatchImageProviderStorageUnavailable.WithCause(startedErr)
		} else if started {
			if !advance {
				return batchImageOpenAIRunningStatus(batchImageOpenAIContinueDelay), nil
			}
			line := batchImageOpenAIFailureLine(item.CustomID, "PROVIDER_ATTEMPT_OUTCOME_UNKNOWN", "the previous upstream attempt ended before its result was durably recorded")
			if _, putErr := p.putResultLine(ctx, resultKey, line); putErr != nil {
				return nil, putErr
			}
			return batchImageOpenAIRunningStatus(batchImageOpenAIContinueDelay), nil
		}

		if !advance {
			return batchImageOpenAIRunningStatus(batchImageOpenAIContinueDelay), nil
		}
		if p.executor == nil {
			return nil, ErrBatchImageProviderSubmitFailed
		}
		if cancelled, cancelErr := p.cancelRequested(ctx, batchID); cancelErr != nil {
			return nil, cancelErr
		} else if cancelled {
			return &BatchProviderStatus{RawState: "CANCELLED", InternalState: BatchProviderStateCancelled, Done: true}, nil
		}

		started := false
		executionTimeout := p.itemExecutionTimeout
		if executionTimeout <= 0 {
			executionTimeout = batchImageOpenAIItemTimeout
		}
		itemCtx, cancelItem := context.WithTimeout(ctx, executionTimeout)
		result, executeErr := p.executor.Execute(itemCtx, account, OpenAIImageBatchItemRequest{
			BatchID:          batchID,
			CustomID:         item.CustomID,
			Model:            manifest.Input.Model,
			Prompt:           item.Prompt,
			ResponseMimeType: manifest.Input.ResponseMimeType,
			AspectRatio:      manifest.Input.AspectRatio,
			ReferenceImages:  item.ReferenceImages,
		}, func() error {
			// This hook is the upstream-attempt linearization point. Check before
			// and after the durable attempt marker so an acknowledged cancel can
			// fence the next item without replaying an ambiguous request.
			cancelled, cancelErr := p.cancelRequested(ctx, batchID)
			if cancelErr != nil {
				return cancelErr
			}
			if cancelled {
				return errBatchImageOpenAICancelled
			}
			marker := []byte(time.Now().UTC().Format(time.RFC3339Nano))
			claimed, claimErr := p.store.PutIfAbsent(ctx, attemptKey, "text/plain", bytes.NewReader(marker), int64(len(marker)))
			if claimErr != nil {
				return ErrBatchImageProviderStorageUnavailable.WithCause(claimErr)
			}
			if !claimed {
				return errBatchImageOpenAIAttemptAlreadyClaimed
			}
			cancelled, cancelErr = p.cancelRequested(ctx, batchID)
			if cancelErr != nil || cancelled {
				if deleteErr := p.store.Delete(ctx, []string{attemptKey}); deleteErr != nil {
					return ErrBatchImageProviderStorageUnavailable.WithCause(deleteErr)
				}
				if cancelErr != nil {
					return cancelErr
				}
				return errBatchImageOpenAICancelled
			}
			started = true
			return nil
		})
		itemTimedOut := errors.Is(executeErr, context.DeadlineExceeded) && ctx.Err() == nil
		cancelItem()
		if errors.Is(executeErr, errBatchImageOpenAIAccountBusy) && !started {
			return batchImageOpenAIRunningStatus(batchImageOpenAIAccountBusyDelay), nil
		}
		if errors.Is(executeErr, errBatchImageOpenAICancelled) && !started {
			return &BatchProviderStatus{RawState: "CANCELLED", InternalState: BatchProviderStateCancelled, Done: true}, nil
		}
		if errors.Is(executeErr, errBatchImageOpenAIAttemptAlreadyClaimed) && !started {
			return batchImageOpenAIRunningStatus(batchImageOpenAIContinueDelay), nil
		}
		// Admission/cache failures happen before the durable attempt marker and
		// therefore before any non-idempotent upstream request. Leave the item
		// pending so a healthy worker can retry it instead of cementing a false
		// per-item failure.
		if executeErr != nil && !started {
			return nil, executeErr
		}
		// A parent cancellation means the worker lost its lease or is shutting
		// down. Do not mutate durable item state after that ownership boundary.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		var line []byte
		if itemTimedOut {
			line = batchImageOpenAIFailureLine(item.CustomID, "PROVIDER_ITEM_TIMEOUT", "the upstream image request exceeded its per-item execution deadline")
		} else if executeErr != nil {
			line = batchImageOpenAIFailureLine(item.CustomID, "PROVIDER_ITEM_FAILED", sanitizeBatchImagePublicMessage(executeErr.Error()))
		} else {
			line, err = batchImageOpenAISuccessLine(item.CustomID, result)
			if err != nil {
				line = batchImageOpenAIFailureLine(item.CustomID, "EMPTY_IMAGE_OUTPUT", "OpenAI Images returned no usable image output")
			}
		}
		if _, putErr := p.putResultLine(ctx, resultKey, line); putErr != nil {
			return nil, putErr
		}
		return batchImageOpenAIRunningStatus(batchImageOpenAIContinueDelay), nil
	}

	if err := p.combineResults(ctx, batchID, manifest.Input.Items, outputKey); err != nil {
		return nil, err
	}
	if err := p.cleanupItemObjects(ctx, batchID, len(manifest.Input.Items)); err != nil {
		return nil, err
	}
	return batchImageOpenAISucceededStatus(outputKey), nil
}

func (p *OpenAIImagesBatchProvider) Cancel(ctx context.Context, job *BatchImageJob, account *Account) error {
	if !p.SupportsAccount(account) {
		return ErrBatchImageProviderUnsupportedAccount
	}
	return p.PersistCancelIntent(ctx, job)
}

func (p *OpenAIImagesBatchProvider) PersistCancelIntent(ctx context.Context, job *BatchImageJob) error {
	if p == nil || p.store == nil || !p.hasSafeObjectPrefix() {
		return ErrBatchImageProviderStorageUnavailable
	}
	batchID, err := batchImageOpenAIJobBatchID(job)
	if err != nil {
		return err
	}
	marker := []byte(time.Now().UTC().Format(time.RFC3339Nano))
	if err := p.store.Put(ctx, p.cancelKey(batchID), "text/plain", bytes.NewReader(marker), int64(len(marker))); err != nil {
		return ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	return nil
}

func (p *OpenAIImagesBatchProvider) cancelRequested(ctx context.Context, batchID string) (bool, error) {
	cancelled, err := p.store.Exists(ctx, p.cancelKey(batchID))
	if err != nil {
		return false, ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	return cancelled, nil
}

func (p *OpenAIImagesBatchProvider) OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error) {
	if !p.SupportsAccount(account) {
		return nil, "", ErrBatchImageProviderUnsupportedAccount
	}
	if p == nil || p.store == nil || !p.hasSafeObjectPrefix() {
		return nil, "", ErrBatchImageProviderStorageUnavailable
	}
	ref := batchImageProviderOutputRef(job)
	if ref == "" {
		return nil, "", ErrBatchImageProviderMissingResultRef
	}
	body, _, contentType, err := p.store.Open(ctx, ref)
	if err != nil {
		return nil, "", ErrBatchImageProviderMissingResultRef.WithCause(err)
	}
	return body, contentType, nil
}

func (p *OpenAIImagesBatchProvider) Cleanup(ctx context.Context, job *BatchImageJob, account *Account, target CleanupTarget) error {
	if account != nil && !p.SupportsAccount(account) {
		return ErrBatchImageProviderUnsupportedAccount
	}
	return p.CleanupWithoutAccount(ctx, job, target)
}

func (p *OpenAIImagesBatchProvider) CleanupWithoutAccount(ctx context.Context, job *BatchImageJob, target CleanupTarget) error {
	if p == nil || p.store == nil || !p.hasSafeObjectPrefix() {
		return ErrBatchImageProviderStorageUnavailable
	}
	batchID, err := batchImageOpenAICleanupBatchID(job)
	if err != nil {
		return err
	}
	keys := make([]string, 0, 3)
	switch target {
	case CleanupTargetInput:
		keys = append(keys, p.inputKey(batchID), p.cancelKey(batchID))
	case CleanupTargetOutput:
		keys = append(keys, p.outputKey(batchID))
	case CleanupTargetAll:
		keys = append(keys, p.inputKey(batchID), p.outputKey(batchID), p.cancelKey(batchID))
	default:
		return ErrUnsupportedCleanupTarget
	}
	if err := p.cleanupItemObjects(ctx, batchID, job.ItemCount); err != nil {
		return err
	}
	if err := p.store.Delete(ctx, keys); err != nil {
		return ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	return nil
}

func (p *OpenAIImagesBatchProvider) openManifest(ctx context.Context, job *BatchImageJob, batchID string) (*openAIImageBatchManifest, error) {
	ref := batchImageProviderInputRef(job)
	if ref == "" {
		ref = p.inputKey(batchID)
	}
	body, size, _, err := p.store.Open(ctx, ref)
	if err != nil {
		return nil, ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	defer func() { _ = body.Close() }()
	if size <= 0 || size > batchImageOpenAIInputMaxBytes {
		return nil, ErrBatchImageProviderInvalidInput
	}
	payload, err := io.ReadAll(io.LimitReader(body, batchImageOpenAIInputMaxBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > batchImageOpenAIInputMaxBytes {
		return nil, ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	var manifest openAIImageBatchManifest
	if err := json.Unmarshal(payload, &manifest); err != nil || manifest.Version != batchImageOpenAIManifestVersion || manifest.Input.BatchID != batchID {
		return nil, ErrBatchImageProviderInvalidInput.WithCause(err)
	}
	return &manifest, nil
}

func (p *OpenAIImagesBatchProvider) putResultLine(ctx context.Context, key string, line []byte) (bool, error) {
	line = append(bytes.TrimSpace(line), '\n')
	if len(line) <= 1 || len(line) > batchImageOpenAIResultMaxBytes {
		return false, ErrBatchImageProviderInvalidInput
	}
	created, err := p.store.PutIfAbsent(ctx, key, "application/x-ndjson", bytes.NewReader(line), int64(len(line)))
	if err != nil {
		return false, ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	return created, nil
}

func (p *OpenAIImagesBatchProvider) combineResults(ctx context.Context, batchID string, items []BatchImageInputItem, outputKey string) error {
	keys := make([]string, 0, len(items))
	totalSize := int64(0)
	for itemIndex := range items {
		key := p.itemResultKey(batchID, itemIndex)
		body, size, _, err := p.store.Open(ctx, key)
		if err != nil {
			return ErrBatchImageProviderStorageUnavailable.WithCause(err)
		}
		_ = body.Close()
		if size <= 0 || size > batchImageOpenAIResultMaxBytes {
			return ErrBatchImageProviderInvalidInput
		}
		keys = append(keys, key)
		totalSize += size
	}

	reader, writer := io.Pipe()
	copyDone := make(chan error, 1)
	go func() {
		var copyErr error
		defer func() {
			_ = writer.CloseWithError(copyErr)
			copyDone <- copyErr
		}()
		for _, key := range keys {
			body, _, _, err := p.store.Open(ctx, key)
			if err != nil {
				copyErr = err
				return
			}
			_, err = io.Copy(writer, body)
			closeErr := body.Close()
			if err != nil {
				copyErr = err
				return
			}
			if closeErr != nil {
				copyErr = closeErr
				return
			}
		}
	}()
	putErr := p.store.Put(ctx, outputKey, "application/x-ndjson", reader, totalSize)
	_ = reader.CloseWithError(putErr)
	copyErr := <-copyDone
	if putErr != nil {
		return ErrBatchImageProviderStorageUnavailable.WithCause(putErr)
	}
	if copyErr != nil {
		return ErrBatchImageProviderStorageUnavailable.WithCause(copyErr)
	}
	return nil
}

func (p *OpenAIImagesBatchProvider) cleanupItemObjects(ctx context.Context, batchID string, itemCount int) error {
	if itemCount < 1 || itemCount > batchImageOpenAIMaxItems {
		return ErrBatchImageProviderInvalidInput
	}
	keys := make([]string, 0, itemCount*2)
	for itemIndex := 0; itemIndex < itemCount; itemIndex++ {
		keys = append(keys,
			p.itemAttemptKey(batchID, itemIndex),
			p.itemResultKey(batchID, itemIndex),
		)
	}
	if err := p.store.Delete(ctx, keys); err != nil {
		return ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	return nil
}

func (p *OpenAIImagesBatchProvider) hasSafeObjectPrefix() bool {
	return p != nil && p.objectPrefix != "" && !strings.Contains(p.objectPrefix, "..")
}

func batchImageOpenAIJobBatchID(job *BatchImageJob) (string, error) {
	if job == nil || !isSafeOpenAIImageBatchID(job.BatchID) {
		return "", ErrBatchImageProviderMissingJobName
	}
	jobName := batchImageProviderJobName(job)
	if jobName != batchImageOpenAIProviderJobPrefix+job.BatchID {
		return "", ErrBatchImageProviderMissingJobName
	}
	return job.BatchID, nil
}

func batchImageOpenAICleanupBatchID(job *BatchImageJob) (string, error) {
	if job == nil || job.Provider != BatchImageProviderOpenAI || !isSafeOpenAIImageBatchID(job.BatchID) {
		return "", ErrBatchImageProviderMissingJobName
	}
	jobName := batchImageProviderJobName(job)
	if jobName == "" {
		// OpenAI input keys are deterministic from the public batch ID. This
		// permits retention cleanup after a crash between manifest upload and
		// persisting provider_job_name, without granting ListBucket permission.
		return job.BatchID, nil
	}
	if jobName != batchImageOpenAIProviderJobPrefix+job.BatchID {
		return "", ErrBatchImageProviderUnsafeCleanupPath
	}
	return job.BatchID, nil
}

func isSafeOpenAIImageBatchID(batchID string) bool {
	batchID = strings.TrimSpace(batchID)
	if !strings.HasPrefix(batchID, "imgbatch_") || len(batchID) > 64 || len(batchID) == len("imgbatch_") {
		return false
	}
	for _, char := range batchID {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '_', char == '-':
		default:
			return false
		}
	}
	return true
}

func batchImageOpenAIRunningStatus(delay time.Duration) *BatchProviderStatus {
	return &BatchProviderStatus{
		RawState:              "RUNNING",
		InternalState:         BatchProviderStateRunning,
		SuggestedRequeueAfter: delay,
	}
}

func batchImageOpenAISucceededStatus(outputKey string) *BatchProviderStatus {
	return &BatchProviderStatus{
		RawState:          "SUCCEEDED",
		InternalState:     BatchProviderStateSucceeded,
		Done:              true,
		ProviderOutputRef: outputKey,
	}
}

func batchImageOpenAISuccessLine(customID string, result *OpenAIImageBatchItemResult) ([]byte, error) {
	if result == nil || strings.TrimSpace(result.Base64Data) == "" {
		return nil, errors.New("missing image output")
	}
	mimeType := normalizeBatchImageReferenceMimeType(result.MimeType)
	if mimeType == "" {
		mimeType = "image/png"
	}
	return json.Marshal(map[string]any{
		"key": customID,
		"response": map[string]any{"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{
				"inlineData": map[string]any{"mimeType": mimeType, "data": strings.TrimSpace(result.Base64Data)},
			}}},
		}}},
	})
}

func batchImageOpenAIFailureLine(customID, code, message string) []byte {
	line, _ := json.Marshal(map[string]any{
		"key": customID,
		"error": map[string]any{
			"code":    truncateBatchImageMessage(strings.TrimSpace(code), 80),
			"message": truncateBatchImageMessage(sanitizeBatchImagePublicMessage(message), batchImageMaxErrorMessageLength),
		},
	})
	return line
}

func (p *OpenAIImagesBatchProvider) inputKey(batchID string) string {
	return p.baseKey(batchID) + "/input/manifest.json"
}

func (p *OpenAIImagesBatchProvider) outputKey(batchID string) string {
	return p.baseKey(batchID) + "/output/results.jsonl"
}

func (p *OpenAIImagesBatchProvider) cancelKey(batchID string) string {
	return p.baseKey(batchID) + "/control/cancelled"
}

func (p *OpenAIImagesBatchProvider) itemAttemptKey(batchID string, itemIndex int) string {
	return fmt.Sprintf("%s/items/%06d.started", p.baseKey(batchID), itemIndex)
}

func (p *OpenAIImagesBatchProvider) itemResultKey(batchID string, itemIndex int) string {
	return fmt.Sprintf("%s/items/%06d.jsonl", p.baseKey(batchID), itemIndex)
}

func (p *OpenAIImagesBatchProvider) baseKey(batchID string) string {
	return path.Join(p.objectPrefix, "provider", "openai", strings.TrimSpace(batchID))
}

type GatewayOpenAIImageBatchItemExecutor struct {
	Gateway *OpenAIGatewayService
}

func (e *GatewayOpenAIImageBatchItemExecutor) Execute(
	ctx context.Context,
	account *Account,
	request OpenAIImageBatchItemRequest,
	beforeUpstream func() error,
) (*OpenAIImageBatchItemResult, error) {
	if e == nil || e.Gateway == nil || account == nil {
		return nil, errors.New("OpenAI image batch executor is not configured")
	}
	if !isOpenAIImageBatchModel(account.GetMappedModel(request.Model)) {
		return nil, ErrBatchImageProviderInvalidInput
	}
	body, parsed, err := buildOpenAIImageBatchItemPayload(request, account.Type)
	if err != nil {
		return nil, err
	}
	slot, err := e.Gateway.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	if slot == nil || !slot.Acquired {
		return nil, errBatchImageOpenAIAccountBusy
	}
	if slot.ReleaseFunc != nil {
		defer slot.ReleaseFunc()
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	executionCtx := withOpenAIImageBatchExecution(WithOpenAIImagesEndpoint(WithOpenAIImageGenerationIntent(ctx)))
	httpRequest, err := http.NewRequestWithContext(executionCtx, http.MethodPost, parsed.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", parsed.ContentType)
	ginContext.Request = httpRequest
	if beforeUpstream != nil {
		if err := beforeUpstream(); err != nil {
			return nil, err
		}
	}

	forwarded, err := e.Gateway.ForwardImages(executionCtx, ginContext, account, body, parsed, "")
	if err != nil {
		return nil, err
	}
	if forwarded == nil || forwarded.ImageCount < 1 || recorder.Code >= http.StatusBadRequest {
		return nil, errors.New("OpenAI Images returned no image output")
	}
	return parseOpenAIImageBatchItemResponse(recorder.Body.Bytes(), request.ResponseMimeType)
}

func buildOpenAIImageBatchItemPayload(request OpenAIImageBatchItemRequest, accountType string) ([]byte, *OpenAIImagesRequest, error) {
	if !isOpenAIImageBatchModel(request.Model) || strings.TrimSpace(request.Prompt) == "" {
		return nil, nil, ErrBatchImageProviderInvalidInput
	}
	sourceSize, err := batchImageOpenAI1KSizeForAspectRatio(request.AspectRatio)
	if err != nil {
		return nil, nil, err
	}
	prompt, imageURLs, err := batchImageOpenAIReferencePayload(
		request.Prompt,
		request.ReferenceImages,
		accountType != AccountTypeAPIKey,
	)
	if err != nil {
		return nil, nil, err
	}
	endpoint := openAIImagesGenerationsEndpoint
	payload := map[string]any{
		"model":  strings.TrimSpace(request.Model),
		"prompt": prompt,
		"n":      1,
		"size":   sourceSize,
	}
	outputFormat := batchImageOpenAIOutputFormat(request.ResponseMimeType)
	if outputFormat != "" {
		payload["output_format"] = outputFormat
	}
	if len(imageURLs) > 0 {
		endpoint = openAIImagesEditsEndpoint
	}
	if len(request.ReferenceImages) > 0 && accountType == AccountTypeAPIKey {
		return buildOpenAIImageBatchMultipartPayload(request, prompt, sourceSize, outputFormat)
	}
	if len(imageURLs) > 0 {
		images := make([]map[string]string, 0, len(imageURLs))
		for _, imageURL := range imageURLs {
			images = append(images, map[string]string{"image_url": imageURL})
		}
		payload["images"] = images
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	parsed := &OpenAIImagesRequest{
		Endpoint:           endpoint,
		ContentType:        "application/json",
		Model:              strings.TrimSpace(request.Model),
		ExplicitModel:      true,
		Prompt:             prompt,
		N:                  1,
		Size:               sourceSize,
		ExplicitSize:       true,
		SizeTier:           ImageBillingSize1K,
		ResponseFormat:     "b64_json",
		OutputFormat:       outputFormat,
		HasNativeOptions:   outputFormat != "",
		RequiredCapability: OpenAIImagesCapabilityNative,
		InputImageURLs:     imageURLs,
		Body:               body,
	}
	return body, parsed, nil
}

func buildOpenAIImageBatchMultipartPayload(
	request OpenAIImageBatchItemRequest,
	prompt string,
	sourceSize string,
	outputFormat string,
) ([]byte, *OpenAIImagesRequest, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	fields := []struct {
		name  string
		value string
	}{
		{name: "model", value: strings.TrimSpace(request.Model)},
		{name: "prompt", value: prompt},
		{name: "n", value: "1"},
		{name: "size", value: sourceSize},
	}
	if outputFormat != "" {
		fields = append(fields, struct {
			name  string
			value string
		}{name: "output_format", value: outputFormat})
	}
	for _, field := range fields {
		if err := writer.WriteField(field.name, field.value); err != nil {
			return nil, nil, err
		}
	}

	uploads := make([]OpenAIImagesUpload, 0, len(request.ReferenceImages))
	for index, ref := range request.ReferenceImages {
		mimeType := normalizeBatchImageReferenceMimeType(ref.MimeType)
		extension := batchImageFileExtension(mimeType)
		if mimeType == "" || extension == "" || len(ref.Data) == 0 || strings.TrimSpace(ref.FileURI) != "" {
			return nil, nil, ErrBatchImageProviderInvalidInput
		}
		fileName := fmt.Sprintf("reference-%02d.%s", index+1, extension)
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="%s"`, fileName))
		header.Set("Content-Type", mimeType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, nil, err
		}
		if _, err := part.Write(ref.Data); err != nil {
			return nil, nil, err
		}
		uploads = append(uploads, OpenAIImagesUpload{
			FieldName:   "image[]",
			FileName:    fileName,
			ContentType: mimeType,
			Data:        append([]byte(nil), ref.Data...),
		})
	}
	if err := writer.Close(); err != nil {
		return nil, nil, err
	}
	body := buffer.Bytes()
	contentType := writer.FormDataContentType()
	return body, &OpenAIImagesRequest{
		Endpoint:           openAIImagesEditsEndpoint,
		ContentType:        contentType,
		Multipart:          true,
		Model:              strings.TrimSpace(request.Model),
		ExplicitModel:      true,
		Prompt:             prompt,
		N:                  1,
		Size:               sourceSize,
		ExplicitSize:       true,
		SizeTier:           ImageBillingSize1K,
		ResponseFormat:     "b64_json",
		OutputFormat:       outputFormat,
		HasNativeOptions:   outputFormat != "",
		RequiredCapability: OpenAIImagesCapabilityNative,
		Uploads:            uploads,
		Body:               body,
	}, nil
}

func batchImageOpenAI1KSizeForAspectRatio(aspectRatio string) (string, error) {
	switch strings.TrimSpace(aspectRatio) {
	case "":
		return "1024x1024", nil
	case "1:1":
		return "1024x1024", nil
	case "2:3":
		return "672x1008", nil
	case "3:2":
		return "1008x672", nil
	case "3:4":
		return "768x1024", nil
	case "4:3":
		return "1024x768", nil
	case "4:5":
		return "768x960", nil
	case "5:4":
		return "960x768", nil
	case "9:16", "16:9", "21:9":
		// Exact dimensions for these ratios cannot satisfy both the native
		// minimum pixel count and Sub2API's longest-edge <= 1024 source-1K
		// contract. Reject instead of silently sending a 2K-class source.
		return "", ErrBatchImageProviderInvalidInput
	default:
		return "", ErrBatchImageProviderInvalidInput
	}
}

func batchImageOpenAIReferencePayload(prompt string, refs []BatchImageReference, includeDataURLs bool) (string, []string, error) {
	prompt = strings.TrimSpace(prompt)
	if len(refs) == 0 {
		return prompt, nil, nil
	}
	var guide strings.Builder
	guide.WriteString(prompt)
	guide.WriteString("\n\nReference image authority. Images are supplied in this exact order:")
	var urls []string
	if includeDataURLs {
		urls = make([]string, 0, len(refs))
	}
	for index, ref := range refs {
		mimeType := normalizeBatchImageReferenceMimeType(ref.MimeType)
		if mimeType == "" || len(ref.Data) == 0 || strings.TrimSpace(ref.FileURI) != "" {
			return "", nil, ErrBatchImageProviderInvalidInput
		}
		roleGuide, ok := batchImageReferenceRoleGuide(ref.Type)
		if !ok {
			roleGuide = "The reference image has no authority beyond the explicit user prompt."
		}
		roleGuide = strings.Replace(roleGuide, "The immediately following image", fmt.Sprintf("Reference image %d", index+1), 1)
		guide.WriteString(fmt.Sprintf("\nReference image %d: %s", index+1, roleGuide))
		if includeDataURLs {
			urls = append(urls, "data:"+mimeType+";base64,"+base64.StdEncoding.EncodeToString(ref.Data))
		}
	}
	return guide.String(), urls, nil
}

func batchImageOpenAIOutputFormat(mimeType string) string {
	switch normalizeBatchImageReferenceMimeType(mimeType) {
	case "image/jpeg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func parseOpenAIImageBatchItemResponse(body []byte, expectedMimeType string) (*OpenAIImageBatchItemResult, error) {
	item := gjson.GetBytes(body, "data.0")
	encoded := strings.TrimSpace(item.Get("b64_json").String())
	declaredMimeType := "image/" + strings.TrimSpace(item.Get("output_format").String())
	if encoded == "" {
		dataURL := strings.TrimSpace(item.Get("url").String())
		if strings.HasPrefix(dataURL, "data:") {
			comma := strings.IndexByte(dataURL, ',')
			if comma > len("data:") && strings.Contains(dataURL[:comma], ";base64") {
				encoded = strings.TrimSpace(dataURL[comma+1:])
				declaredMimeType = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(dataURL[:comma], "data:"), ";base64"))
			}
		}
	}
	if encoded == "" {
		return nil, errors.New("OpenAI Images response is missing inline image data")
	}
	mimeType := detectOpenAIImageBatchMimeType(encoded, declaredMimeType, expectedMimeType)
	return &OpenAIImageBatchItemResult{Base64Data: encoded, MimeType: mimeType}, nil
}

func detectOpenAIImageBatchMimeType(encoded, declaredMimeType, expectedMimeType string) string {
	prefix := make([]byte, 512)
	read, err := io.ReadFull(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), prefix)
	if err == nil || errors.Is(err, io.ErrUnexpectedEOF) {
		if mimeType := normalizeBatchImageReferenceMimeType(http.DetectContentType(prefix[:read])); mimeType != "" {
			return mimeType
		}
	}
	for _, candidate := range []string{declaredMimeType, expectedMimeType} {
		if mimeType := normalizeBatchImageReferenceMimeType(candidate); mimeType != "" {
			return mimeType
		}
	}
	return "image/png"
}
