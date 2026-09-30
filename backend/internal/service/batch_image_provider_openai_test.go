//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const testOpenAIImageBatchObjectPrefix = "sub2-batch-image/test"

type fakeOpenAIImageBatchObject struct {
	data        []byte
	contentType string
}

type fakeOpenAIImageBatchStore struct {
	mu      sync.Mutex
	objects map[string]fakeOpenAIImageBatchObject
}

type seekableOutputOpenAIImageBatchStore struct {
	*fakeOpenAIImageBatchStore
	outputPutWasSeekable bool
}

func (s *seekableOutputOpenAIImageBatchStore) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if strings.HasSuffix(key, "/output/results.jsonl") {
		if _, ok := body.(io.ReadSeeker); !ok {
			return errors.New("combined output body is not seekable")
		}
		s.outputPutWasSeekable = true
	}
	return s.fakeOpenAIImageBatchStore.Put(ctx, key, contentType, body, size)
}

func newFakeOpenAIImageBatchStore() *fakeOpenAIImageBatchStore {
	return &fakeOpenAIImageBatchStore{objects: make(map[string]fakeOpenAIImageBatchObject)}
}

func (s *fakeOpenAIImageBatchStore) Put(_ context.Context, key, contentType string, body io.Reader, size int64) error {
	payload, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(payload)) != size {
		return errors.New("unexpected object size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = fakeOpenAIImageBatchObject{data: append([]byte(nil), payload...), contentType: contentType}
	return nil
}

func (s *fakeOpenAIImageBatchStore) PutIfAbsent(_ context.Context, key, contentType string, body io.Reader, size int64) (bool, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return false, err
	}
	if int64(len(payload)) != size {
		return false, errors.New("unexpected object size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objects[key]; exists {
		return false, nil
	}
	s.objects[key] = fakeOpenAIImageBatchObject{data: append([]byte(nil), payload...), contentType: contentType}
	return true, nil
}

func (s *fakeOpenAIImageBatchStore) Open(_ context.Context, key string) (io.ReadCloser, int64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[key]
	if !ok {
		return nil, 0, "", errors.New("object not found")
	}
	payload := append([]byte(nil), object.data...)
	return io.NopCloser(bytes.NewReader(payload)), int64(len(payload)), object.contentType, nil
}

func (s *fakeOpenAIImageBatchStore) Delete(_ context.Context, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		delete(s.objects, key)
	}
	return nil
}

func (s *fakeOpenAIImageBatchStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

type fakeOpenAIImageBatchExecutor struct {
	mu               sync.Mutex
	requests         []OpenAIImageBatchItemRequest
	busyOnce         bool
	admissionErrOnce error
	waitForContext   bool
	beforeHook       func()
	upstreamStarts   int
}

type synchronizedOpenAIImageBatchExecutor struct {
	ready          chan struct{}
	release        <-chan struct{}
	mu             sync.Mutex
	upstreamStarts int
}

func (e *synchronizedOpenAIImageBatchExecutor) Execute(
	ctx context.Context,
	_ *Account,
	request OpenAIImageBatchItemRequest,
	beforeUpstream func() error,
) (*OpenAIImageBatchItemResult, error) {
	select {
	case e.ready <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := beforeUpstream(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.upstreamStarts++
	e.mu.Unlock()
	return &OpenAIImageBatchItemResult{Base64Data: "image-" + request.CustomID, MimeType: "image/png"}, nil
}

func (e *fakeOpenAIImageBatchExecutor) Execute(
	ctx context.Context,
	_ *Account,
	request OpenAIImageBatchItemRequest,
	beforeUpstream func() error,
) (*OpenAIImageBatchItemResult, error) {
	e.mu.Lock()
	if e.busyOnce {
		e.busyOnce = false
		e.mu.Unlock()
		return nil, errBatchImageOpenAIAccountBusy
	}
	if e.admissionErrOnce != nil {
		err := e.admissionErrOnce
		e.admissionErrOnce = nil
		e.mu.Unlock()
		return nil, err
	}
	e.requests = append(e.requests, request)
	waitForContext := e.waitForContext
	beforeHook := e.beforeHook
	e.mu.Unlock()
	if beforeHook != nil {
		beforeHook()
	}
	if err := beforeUpstream(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.upstreamStarts++
	e.mu.Unlock()
	if waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &OpenAIImageBatchItemResult{Base64Data: "image-" + request.CustomID, MimeType: "image/png"}, nil
}

func TestOpenAIImagesBatchProviderExecutesDistinctItemsAndCombinesResults(t *testing.T) {
	ctx := context.Background()
	store := &seekableOutputOpenAIImageBatchStore{fakeOpenAIImageBatchStore: newFakeOpenAIImageBatchStore()}
	executor := &fakeOpenAIImageBatchExecutor{}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	batchID := "imgbatch_0123456789abcdef0123456789abcdef"
	input := BatchImageInput{
		BatchID:          batchID,
		Model:            "gpt-image-2.5-sunburst",
		ImageSize:        ImageBillingSize1K,
		ResponseMimeType: "image/png",
		Items: []BatchImageInputItem{
			{CustomID: "red", Prompt: "red ceramic cup", ReferenceImages: []BatchImageReference{{Type: "PRODUCT_TRUTH", MimeType: "image/png", Data: []byte("red-ref")}}},
			{CustomID: "blue", Prompt: "blue fabric bag", ReferenceImages: []BatchImageReference{{Type: "STYLE_REFERENCE", MimeType: "image/jpeg", Data: []byte("blue-ref")}}},
		},
	}

	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(created.ProviderInputRef, testOpenAIImageBatchObjectPrefix+"/provider/openai/"))
	job := &BatchImageJob{
		BatchID:          batchID,
		ProviderJobName:  batchImageStringPtr(created.ProviderJobName),
		ProviderInputRef: batchImageStringPtr(created.ProviderInputRef),
	}

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateRunning, status.InternalState)
	status, err = provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateRunning, status.InternalState)
	status, err = provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateSucceeded, status.InternalState)
	require.True(t, status.Done)
	require.True(t, store.outputPutWasSeekable)

	executor.mu.Lock()
	require.Len(t, executor.requests, 2)
	require.Equal(t, "red ceramic cup", executor.requests[0].Prompt)
	require.Equal(t, []byte("red-ref"), executor.requests[0].ReferenceImages[0].Data)
	require.Equal(t, "blue fabric bag", executor.requests[1].Prompt)
	require.Equal(t, []byte("blue-ref"), executor.requests[1].ReferenceImages[0].Data)
	executor.mu.Unlock()

	job.ProviderOutputRef = batchImageStringPtr(status.ProviderOutputRef)
	result, contentType, err := provider.OpenResult(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, "application/x-ndjson", contentType)
	defer result.Close()
	payload, err := io.ReadAll(result)
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(payload), []byte("\n"))
	require.Len(t, lines, 2)
	require.Equal(t, "red", gjson.GetBytes(lines[0], "key").String())
	require.Equal(t, "image-red", gjson.GetBytes(lines[0], "response.candidates.0.content.parts.0.inlineData.data").String())
	require.Equal(t, "blue", gjson.GetBytes(lines[1], "key").String())
	require.Equal(t, "image-blue", gjson.GetBytes(lines[1], "response.candidates.0.content.parts.0.inlineData.data").String())
	store.mu.Lock()
	for key := range store.objects {
		require.False(t, strings.HasPrefix(key, provider.baseKey(batchID)+"/items/"), "temporary item object remained after result combination: %s", key)
	}
	store.mu.Unlock()
}

func TestOpenAIImagesBatchProviderSubmissionAvailabilityNeedsStorageNotWorkerExecutor(t *testing.T) {
	store := newFakeOpenAIImageBatchStore()
	publicProvider := NewOpenAIImagesBatchProvider(store, nil, testOpenAIImageBatchObjectPrefix)
	require.True(t, publicProvider.SubmissionAvailable())
	require.False(t, NewOpenAIImagesBatchProvider(nil, nil, testOpenAIImageBatchObjectPrefix).SubmissionAvailable())
	require.False(t, NewOpenAIImagesBatchProvider(store, nil, "").SubmissionAvailable())

	batchID := "imgbatch_99999999999999999999999999999999"
	created, err := publicProvider.Submit(
		context.Background(),
		&BatchImageJob{BatchID: batchID},
		&Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "queued", Prompt: "one image"}}},
	)
	require.NoError(t, err)
	require.Equal(t, batchImageOpenAIProviderJobPrefix+batchID, created.ProviderJobName)
}

func TestOpenAIImagesBatchProviderRejectsAspectRatioThatCannotBeNativeSource1K(t *testing.T) {
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, nil, testOpenAIImageBatchObjectPrefix)
	batchID := "imgbatch_98989898989898989898989898989898"
	_, err := provider.Submit(
		context.Background(),
		&BatchImageJob{BatchID: batchID},
		&Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		BatchImageInput{
			BatchID:     batchID,
			Model:       "gpt-image-2.5-flare",
			AspectRatio: "16:9",
			Items:       []BatchImageInputItem{{CustomID: "wide", Prompt: "one image"}},
		},
	)
	require.ErrorIs(t, err, ErrBatchImageProviderInvalidInput)
	require.Empty(t, store.objects)
}

func TestOpenAIImagesBatchProviderRejectsModelWithoutCustomSourceDimensions(t *testing.T) {
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, nil, testOpenAIImageBatchObjectPrefix)
	batchID := "imgbatch_97979797979797979797979797979797"
	_, err := provider.Submit(
		context.Background(),
		&BatchImageJob{BatchID: batchID},
		&Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		BatchImageInput{
			BatchID: batchID,
			Model:   "gpt-image-1.5",
			Items:   []BatchImageInputItem{{CustomID: "legacy", Prompt: "one image"}},
		},
	)
	require.ErrorIs(t, err, ErrBatchImageProviderInvalidInput)
	require.Empty(t, store.objects)
}

func TestOpenAIImagesBatchProviderDoesNotReplayAmbiguousAttempt(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	executor := &fakeOpenAIImageBatchExecutor{}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 92, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_11111111111111111111111111111111"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "ambiguous", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef)}

	marker := []byte("started-before-crash")
	require.NoError(t, store.Put(ctx, provider.itemAttemptKey(batchID, 0), "text/plain", bytes.NewReader(marker), int64(len(marker))))

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateRunning, status.InternalState)
	status, err = provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateSucceeded, status.InternalState)
	require.Empty(t, executor.requests)

	job.ProviderOutputRef = batchImageStringPtr(status.ProviderOutputRef)
	result, _, err := provider.OpenResult(ctx, job, account)
	require.NoError(t, err)
	defer result.Close()
	payload, err := io.ReadAll(result)
	require.NoError(t, err)
	parsed, err := ParseBatchImageResultLine(bytes.TrimSpace(payload), 1)
	require.NoError(t, err)
	require.Equal(t, BatchImageParsedStatusFailed, parsed.Status)
	require.Equal(t, "PROVIDER_ITEM_FAILED", parsed.ErrorCode)
}

func TestOpenAIImagesBatchProviderAtomicAttemptClaimAllowsOnlyOneConcurrentUpstream(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	release := make(chan struct{})
	executor := &synchronizedOpenAIImageBatchExecutor{ready: make(chan struct{}, 2), release: release}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 192, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 2}
	batchID := "imgbatch_19191919191919191919191919191919"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "claimed-once", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef), ItemCount: 1}

	type advanceResult struct {
		status *BatchProviderStatus
		err    error
	}
	results := make(chan advanceResult, 2)
	for range 2 {
		go func() {
			status, advanceErr := provider.Advance(ctx, job, account)
			results <- advanceResult{status: status, err: advanceErr}
		}()
	}
	for range 2 {
		select {
		case <-executor.ready:
		case <-time.After(time.Second):
			t.Fatal("concurrent advance did not reach the attempt claim")
		}
	}
	close(release)
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.NotNil(t, result.status)
		require.Equal(t, BatchProviderStateRunning, result.status.InternalState)
	}
	executor.mu.Lock()
	require.Equal(t, 1, executor.upstreamStarts)
	executor.mu.Unlock()
	started, err := store.Exists(ctx, provider.itemAttemptKey(batchID, 0))
	require.NoError(t, err)
	require.True(t, started)

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateSucceeded, status.InternalState)
}

func TestOpenAIImagesBatchProviderItemResultFirstWriterWins(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, nil, testOpenAIImageBatchObjectPrefix)
	key := provider.itemResultKey("imgbatch_20202020202020202020202020202020", 0)
	first := batchImageOpenAIFailureLine("item-1", "PROVIDER_ITEM_FAILED", "first result")
	second := batchImageOpenAIFailureLine("item-1", "PROVIDER_ATTEMPT_OUTCOME_UNKNOWN", "late takeover result")

	created, err := provider.putResultLine(ctx, key, first)
	require.NoError(t, err)
	require.True(t, created)
	created, err = provider.putResultLine(ctx, key, second)
	require.NoError(t, err)
	require.False(t, created)

	body, _, _, err := store.Open(ctx, key)
	require.NoError(t, err)
	defer body.Close()
	persisted, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, append(bytes.TrimSpace(first), '\n'), persisted)
}

func TestOpenAIImagesBatchProviderBusyAccountDoesNotStartAttempt(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	executor := &fakeOpenAIImageBatchExecutor{busyOnce: true}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 93, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_22222222222222222222222222222222"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "queued", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef)}

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, batchImageOpenAIAccountBusyDelay, status.SuggestedRequeueAfter)
	started, err := store.Exists(ctx, provider.itemAttemptKey(batchID, 0))
	require.NoError(t, err)
	require.False(t, started)

	_, err = provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Len(t, executor.requests, 1)
}

func TestOpenAIImagesBatchProviderRetriesAdmissionErrorBeforeAttempt(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	executor := &fakeOpenAIImageBatchExecutor{admissionErrOnce: errors.New("redis temporarily unavailable")}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 97, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_66666666666666666666666666666666"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "retry", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef)}

	_, err = provider.Advance(ctx, job, account)
	require.ErrorContains(t, err, "redis temporarily unavailable")
	started, err := store.Exists(ctx, provider.itemAttemptKey(batchID, 0))
	require.NoError(t, err)
	require.False(t, started)
	resultExists, err := store.Exists(ctx, provider.itemResultKey(batchID, 0))
	require.NoError(t, err)
	require.False(t, resultExists)

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateRunning, status.InternalState)
	require.Len(t, executor.requests, 1)
}

func TestOpenAIImagesBatchProviderBoundsOneItemAndContinues(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	executor := &fakeOpenAIImageBatchExecutor{waitForContext: true}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	provider.itemExecutionTimeout = 10 * time.Millisecond
	account := &Account{ID: 98, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_77777777777777777777777777777777"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "timeout", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef), ItemCount: 1}

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateRunning, status.InternalState)
	status, err = provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateSucceeded, status.InternalState)

	job.ProviderOutputRef = batchImageStringPtr(status.ProviderOutputRef)
	result, _, err := provider.OpenResult(ctx, job, account)
	require.NoError(t, err)
	defer result.Close()
	payload, err := io.ReadAll(result)
	require.NoError(t, err)
	require.Equal(t, "PROVIDER_ITEM_TIMEOUT", gjson.GetBytes(payload, "error.code").String())
}

func TestOpenAIImagesBatchProviderGetDoesNotStartPendingItem(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	executor := &fakeOpenAIImageBatchExecutor{}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 94, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_33333333333333333333333333333333"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "pending", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef)}

	status, err := provider.Get(ctx, job, account)
	require.NoError(t, err)
	require.Equal(t, BatchProviderStateRunning, status.InternalState)
	require.Empty(t, executor.requests)
	started, err := store.Exists(ctx, provider.itemAttemptKey(batchID, 0))
	require.NoError(t, err)
	require.False(t, started)
}

func TestOpenAIImagesBatchProviderCancelPersistsDurableMarker(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, &fakeOpenAIImageBatchExecutor{}, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 95, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_44444444444444444444444444444444"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "cancelled", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef)}

	require.NoError(t, provider.Cancel(ctx, job, account))
	status, err := provider.Get(ctx, job, account)
	require.NoError(t, err)
	require.True(t, status.Done)
	require.Equal(t, BatchProviderStateCancelled, status.InternalState)
	exists, err := store.Exists(ctx, provider.cancelKey(batchID))
	require.NoError(t, err)
	require.True(t, exists)
}

func TestOpenAIImagesBatchProviderCancelFencesItemAfterAdmissionBeforeUpstream(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	executor := &fakeOpenAIImageBatchExecutor{}
	provider := NewOpenAIImagesBatchProvider(store, executor, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 195, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	batchID := "imgbatch_45454545454545454545454545454545"
	input := BatchImageInput{BatchID: batchID, Model: "gpt-image-2.5-flare", Items: []BatchImageInputItem{{CustomID: "cancelled-before-upstream", Prompt: "one image"}}}
	created, err := provider.Submit(ctx, &BatchImageJob{BatchID: batchID}, account, input)
	require.NoError(t, err)
	job := &BatchImageJob{BatchID: batchID, ProviderJobName: batchImageStringPtr(created.ProviderJobName), ProviderInputRef: batchImageStringPtr(created.ProviderInputRef)}
	executor.beforeHook = func() {
		require.NoError(t, provider.PersistCancelIntent(ctx, job))
	}

	status, err := provider.Advance(ctx, job, account)
	require.NoError(t, err)
	require.True(t, status.Done)
	require.Equal(t, BatchProviderStateCancelled, status.InternalState)
	executor.mu.Lock()
	require.Zero(t, executor.upstreamStarts)
	executor.mu.Unlock()
	started, err := store.Exists(ctx, provider.itemAttemptKey(batchID, 0))
	require.NoError(t, err)
	require.False(t, started)
}

func TestOpenAIImagesBatchProviderOutputCleanupDeletesItemObjectsWithoutManifest(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, &fakeOpenAIImageBatchExecutor{}, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 96, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_55555555555555555555555555555555"
	job := &BatchImageJob{
		BatchID:         batchID,
		Provider:        BatchImageProviderOpenAI,
		ProviderJobName: batchImageStringPtr(batchImageOpenAIProviderJobPrefix + batchID),
		ItemCount:       1,
	}
	objects := map[string][]byte{
		provider.outputKey(batchID):         []byte("combined\n"),
		provider.itemAttemptKey(batchID, 0): []byte("started"),
		provider.itemResultKey(batchID, 0):  []byte("image-data\n"),
	}
	for key, payload := range objects {
		require.NoError(t, store.Put(ctx, key, "application/octet-stream", bytes.NewReader(payload), int64(len(payload))))
	}

	require.NoError(t, provider.Cleanup(ctx, job, account, CleanupTargetOutput))
	for key := range objects {
		exists, err := store.Exists(ctx, key)
		require.NoError(t, err)
		require.False(t, exists, "cleanup left object %s", key)
	}
}

func TestOpenAIImagesBatchProviderInputCleanupDerivesCrashGapKey(t *testing.T) {
	ctx := context.Background()
	store := newFakeOpenAIImageBatchStore()
	provider := NewOpenAIImagesBatchProvider(store, &fakeOpenAIImageBatchExecutor{}, testOpenAIImageBatchObjectPrefix)
	account := &Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	batchID := "imgbatch_88888888888888888888888888888888"
	inputKey := provider.inputKey(batchID)
	payload := []byte(`{"version":1}`)
	require.NoError(t, store.Put(ctx, inputKey, "application/json", bytes.NewReader(payload), int64(len(payload))))
	job := &BatchImageJob{BatchID: batchID, Provider: BatchImageProviderOpenAI, ItemCount: 1}

	require.NoError(t, provider.Cleanup(ctx, job, account, CleanupTargetInput))
	exists, err := store.Exists(ctx, inputKey)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestOpenAIImagesBatchProviderCleanupRejectsUnsafeBatchID(t *testing.T) {
	provider := NewOpenAIImagesBatchProvider(newFakeOpenAIImageBatchStore(), nil, testOpenAIImageBatchObjectPrefix)
	job := &BatchImageJob{
		BatchID:  "imgbatch_/../../other-job",
		Provider: BatchImageProviderOpenAI,
	}

	err := provider.CleanupWithoutAccount(context.Background(), job, CleanupTargetAll)
	require.ErrorIs(t, err, ErrBatchImageProviderMissingJobName)
}

func TestBuildOpenAIImageBatchItemPayloadUsesOneSourceTierRequestAndOrderedReferences(t *testing.T) {
	body, parsed, err := buildOpenAIImageBatchItemPayload(OpenAIImageBatchItemRequest{
		Model:            "gpt-image-2.5-sunburst",
		Prompt:           "place the product on the table",
		ResponseMimeType: "image/webp",
		AspectRatio:      "4:5",
		ReferenceImages: []BatchImageReference{
			{Type: "PRODUCT_TRUTH", MimeType: "image/png", Data: []byte("product")},
			{Type: "SCENE_REFERENCE", MimeType: "image/jpeg", Data: []byte("scene")},
		},
	}, AccountTypeOAuth)
	require.NoError(t, err)
	require.Equal(t, openAIImagesEditsEndpoint, parsed.Endpoint)
	require.Equal(t, 1, parsed.N)
	require.Equal(t, "768x960", parsed.Size)
	require.Equal(t, ImageBillingSize1K, parsed.SizeTier)
	require.Len(t, parsed.InputImageURLs, 2)
	require.Contains(t, parsed.Prompt, "Reference image 1")
	require.Contains(t, parsed.Prompt, "PRODUCT_TRUTH")
	require.Contains(t, parsed.Prompt, "Reference image 2")
	require.Contains(t, parsed.Prompt, "SCENE_REFERENCE")
	require.Equal(t, int64(1), gjson.GetBytes(body, "n").Int())
	require.Equal(t, "768x960", gjson.GetBytes(body, "size").String())
	require.False(t, gjson.GetBytes(body, "response_format").Exists())
	require.Equal(t, "webp", gjson.GetBytes(body, "output_format").String())
	require.Len(t, gjson.GetBytes(body, "images").Array(), 2)
}

func TestGatewayOpenAIImageBatchItemExecutorSendsMultipartEditsForAPIKey(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U=","output_format":"png"}]}`)),
	}}
	executor := &GatewayOpenAIImageBatchItemExecutor{Gateway: &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}}
	account := &Account{
		ID:          196,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.example.test/v1"},
	}
	markerCalls := 0
	result, err := executor.Execute(context.Background(), account, OpenAIImageBatchItemRequest{
		Model:            "gpt-image-2.5-sunburst",
		Prompt:           "keep the product shape and place it in the new scene",
		ResponseMimeType: "image/png",
		AspectRatio:      "4:5",
		ReferenceImages: []BatchImageReference{
			{Type: "PRODUCT_TRUTH", MimeType: "image/png", Data: []byte("product-bytes")},
			{Type: "SCENE_REFERENCE", MimeType: "image/jpeg", Data: []byte("scene-bytes")},
		},
	}, func() error {
		markerCalls++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "aW1hZ2U=", result.Base64Data)
	require.Equal(t, "image/png", result.MimeType)
	require.Equal(t, 1, markerCalls)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, openAIImagesEditsEndpoint, upstream.lastReq.URL.Path)
	require.True(t, strings.HasPrefix(upstream.lastReq.Header.Get("Content-Type"), "multipart/form-data; boundary="))

	outbound, err := http.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(upstream.lastBody))
	require.NoError(t, err)
	outbound.Header.Set("Content-Type", upstream.lastReq.Header.Get("Content-Type"))
	require.NoError(t, outbound.ParseMultipartForm(1<<20))
	require.Equal(t, "gpt-image-2.5-sunburst", outbound.FormValue("model"))
	require.Equal(t, "1", outbound.FormValue("n"))
	require.Equal(t, "768x960", outbound.FormValue("size"))
	require.Empty(t, outbound.FormValue("response_format"))
	require.Contains(t, outbound.FormValue("prompt"), "Reference image 1")
	files := outbound.MultipartForm.File["image[]"]
	require.Len(t, files, 2)
	require.Equal(t, "reference-01.png", files[0].Filename)
	require.Equal(t, "image/png", files[0].Header.Get("Content-Type"))
	require.Equal(t, "reference-02.jpg", files[1].Filename)
	require.Equal(t, "image/jpeg", files[1].Header.Get("Content-Type"))
	first, err := files[0].Open()
	require.NoError(t, err)
	firstBytes, err := io.ReadAll(first)
	require.NoError(t, err)
	require.NoError(t, first.Close())
	require.Equal(t, []byte("product-bytes"), firstBytes)
	second, err := files[1].Open()
	require.NoError(t, err)
	secondBytes, err := io.ReadAll(second)
	require.NoError(t, err)
	require.NoError(t, second.Close())
	require.Equal(t, []byte("scene-bytes"), secondBytes)
	require.NotContains(t, string(upstream.lastBody), "data:image/")
}

func TestParseOpenAIImageBatchItemResponseSniffsMimeTypeWhenFormatIsOmitted(t *testing.T) {
	tests := map[string][]byte{
		"image/jpeg": {0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00},
		"image/webp": {'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P', 'V', 'P'},
	}
	for expectedMimeType, imageBytes := range tests {
		t.Run(expectedMimeType, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString(imageBytes)
			result, err := parseOpenAIImageBatchItemResponse(
				[]byte(`{"data":[{"b64_json":"`+encoded+`"}]}`),
				"image/png",
			)
			require.NoError(t, err)
			require.Equal(t, expectedMimeType, result.MimeType)
		})
	}
}

func TestGatewayOpenAIImageBatchItemExecutorRejectsUnsupportedMappedModelBeforeUpstream(t *testing.T) {
	for _, mappedModel := range []string{"gpt-image-1.5", "grok-imagine-image"} {
		t.Run(mappedModel, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			executor := &GatewayOpenAIImageBatchItemExecutor{Gateway: &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}}
			account := &Account{
				ID:       197,
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key":  "test-key",
					"base_url": "https://api.example.test/v1",
					"model_mapping": map[string]any{
						"gpt-image-2.5-sunburst": mappedModel,
					},
				},
			}
			markerCalls := 0
			result, err := executor.Execute(context.Background(), account, OpenAIImageBatchItemRequest{
				Model:       "gpt-image-2.5-sunburst",
				Prompt:      "one product photo",
				AspectRatio: "1:1",
			}, func() error {
				markerCalls++
				return nil
			})
			require.ErrorIs(t, err, ErrBatchImageProviderInvalidInput)
			require.Nil(t, result)
			require.Equal(t, 0, markerCalls)
			require.Nil(t, upstream.lastReq)
		})
	}
}

func TestBatchImageOpenAI1KSizeForAspectRatioUsesValidNativeDimensions(t *testing.T) {
	tests := map[string]string{
		"":    "1024x1024",
		"1:1": "1024x1024",
		"2:3": "672x1008",
		"3:2": "1008x672",
		"3:4": "768x1024",
		"4:3": "1024x768",
		"4:5": "768x960",
		"5:4": "960x768",
	}
	for aspectRatio, expected := range tests {
		t.Run(aspectRatio, func(t *testing.T) {
			size, err := batchImageOpenAI1KSizeForAspectRatio(aspectRatio)
			require.NoError(t, err)
			require.Equal(t, expected, size)
			parts := strings.Split(size, "x")
			require.Len(t, parts, 2)
			width, err := strconv.Atoi(parts[0])
			require.NoError(t, err)
			height, err := strconv.Atoi(parts[1])
			require.NoError(t, err)
			require.Zero(t, width%16)
			require.Zero(t, height%16)
			require.GreaterOrEqual(t, width*height, 655360)
			require.LessOrEqual(t, width*height, 8294400)
			require.LessOrEqual(t, width, 3840)
			require.LessOrEqual(t, height, 3840)
			require.LessOrEqual(t, float64(max(width, height))/float64(min(width, height)), 3.0)
		})
	}
	for _, aspectRatio := range []string{"9:16", "16:9", "21:9"} {
		t.Run("unsupported_"+strings.ReplaceAll(aspectRatio, ":", "_"), func(t *testing.T) {
			_, err := batchImageOpenAI1KSizeForAspectRatio(aspectRatio)
			require.ErrorIs(t, err, ErrBatchImageProviderInvalidInput)
		})
	}
}
