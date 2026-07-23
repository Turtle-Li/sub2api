package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	batchImageCOSArchiveMarkerPrefix = "cos-jsonl:v1:"
	batchImageCOSArchiveContentType  = "application/x-ndjson"
	batchImageDeliveryMaxSourceFiles = 20
	batchImagePumpControlMaxBytes    = 256 * 1024
	batchImagePumpRequestMaxBytes    = 256 * 1024
	batchImagePumpTimestampHeader    = "X-Turtle-Pump-Timestamp"
	batchImagePumpNonceHeader        = "X-Turtle-Pump-Nonce"
	batchImagePumpSignatureHeader    = "X-Turtle-Pump-Signature"
)

type BatchImageResultDelivery interface {
	Applies(job *BatchImageJob) bool
	Process(ctx context.Context, job *BatchImageJob, provider BatchImageProvider, account *Account) (*BatchImageIndexResult, time.Duration, error)
}

type BatchImageDeliveryService struct {
	Repo       BatchImageRepository
	Store      BatchImageDeliveryObjectStore
	Config     *config.Config
	HTTPClient *http.Client
}

type batchImagePumpFile struct {
	Index     int    `json:"index"`
	SourceID  string `json:"source_id"`
	SourceURL string `json:"source_url"`
	ObjectKey string `json:"object_key"`
	UploadURL string `json:"upload_url"`
}

type batchImagePumpRequest struct {
	Version int                  `json:"version"`
	RunID   string               `json:"run_id"`
	JobID   string               `json:"job_id"`
	Files   []batchImagePumpFile `json:"files"`
}

type batchImagePumpArchivedFile struct {
	Index       int    `json:"index"`
	SourceID    string `json:"source_id"`
	ObjectKey   string `json:"object_key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag"`
}

type batchImagePumpOutput struct {
	Version     int                          `json:"version"`
	RunID       string                       `json:"run_id"`
	JobID       string                       `json:"job_id"`
	Status      string                       `json:"status"`
	SourceBytes int64                        `json:"source_bytes"`
	Files       []batchImagePumpArchivedFile `json:"files"`
}

type batchImagePumpStatus struct {
	RunID  string                `json:"run_id"`
	Status string                `json:"status"`
	Output *batchImagePumpOutput `json:"output"`
}

func NewBatchImageDeliveryService(repo BatchImageRepository, store BatchImageDeliveryObjectStore, cfg *config.Config) *BatchImageDeliveryService {
	return &BatchImageDeliveryService{
		Repo:       repo,
		Store:      store,
		Config:     cfg,
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *BatchImageDeliveryService) Applies(job *BatchImageJob) bool {
	return s != nil && s.Config != nil && s.Config.BatchImage.DeliveryEnabled &&
		job != nil && job.Provider == BatchImageProviderVertex
}

func (s *BatchImageDeliveryService) Process(ctx context.Context, job *BatchImageJob, provider BatchImageProvider, account *Account) (*BatchImageIndexResult, time.Duration, error) {
	if !s.Applies(job) {
		return nil, 0, ErrBatchImageDeliveryNotConfigured
	}
	if s.Repo == nil || s.Store == nil || provider == nil || account == nil {
		return nil, 0, ErrBatchImageDeliveryNotConfigured
	}
	runID := batchImageDeliveryRunID(job.BatchID)
	status, statusCode, err := s.pumpRequest(ctx, http.MethodGet, "/v1/jobs/"+runID, nil)
	if err != nil {
		return nil, 0, err
	}
	if statusCode == http.StatusNotFound {
		request, buildErr := s.buildPumpRequest(ctx, job, provider, account, runID)
		if buildErr != nil {
			return nil, 0, buildErr
		}
		body, marshalErr := json.Marshal(request)
		if marshalErr != nil || len(body) > batchImagePumpRequestMaxBytes {
			return nil, 0, ErrBatchImageDeliveryFailed
		}
		status, statusCode, err = s.pumpRequest(ctx, http.MethodPost, "/v1/jobs", body)
		if err != nil {
			return nil, 0, err
		}
	}
	if statusCode != http.StatusOK && statusCode != http.StatusAccepted {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	switch strings.TrimSpace(status.Status) {
	case "queued", "running", "waiting", "paused", "unknown":
		return nil, s.pollDelay(), nil
	case "complete":
		return s.indexCompletedArchive(ctx, job, provider, account, runID, status.Output)
	case "errored", "terminated":
		return nil, 0, ErrBatchImageDeliveryFailed
	default:
		return nil, s.pollDelay(), nil
	}
}

func (s *BatchImageDeliveryService) buildPumpRequest(ctx context.Context, job *BatchImageJob, provider BatchImageProvider, account *Account, runID string) (*batchImagePumpRequest, error) {
	sourceProvider, ok := provider.(BatchImageSignedResultSourceProvider)
	if !ok {
		return nil, ErrBatchImageDeliveryFailed
	}
	sources, err := sourceProvider.SignedResultSources(ctx, job, account, s.sourceURLTTL())
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, ErrBatchImageIndexNoResultLines
	}
	if len(sources) > batchImageDeliveryMaxSourceFiles {
		return nil, ErrBatchImageDeliveryFailed
	}
	request := &batchImagePumpRequest{
		Version: 2,
		RunID:   runID,
		JobID:   job.BatchID,
		Files:   make([]batchImagePumpFile, 0, len(sources)),
	}
	for index, source := range sources {
		key, keyErr := BatchImageDeliveryArchiveObjectKey(s.Config, job.BatchID, index)
		if keyErr != nil {
			return nil, keyErr
		}
		signed, signErr := s.Store.PresignPut(ctx, key, s.uploadURLTTL())
		if signErr != nil {
			return nil, ErrBatchImageDeliveryFailed
		}
		request.Files = append(request.Files, batchImagePumpFile{
			Index:     index,
			SourceID:  source.ID,
			SourceURL: source.URL,
			ObjectKey: key,
			UploadURL: signed,
		})
	}
	return request, nil
}

func (s *BatchImageDeliveryService) expectedItems(ctx context.Context, batchID string) ([]*BatchImageItem, error) {
	const pageSize = 500
	var result []*BatchImageItem
	for offset := 0; ; offset += pageSize {
		page, err := s.Repo.ListBatchImageItems(ctx, batchID, BatchImageItemFilter{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		if len(page) < pageSize {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CustomID < result[j].CustomID })
	return result, nil
}

func (s *BatchImageDeliveryService) indexCompletedArchive(ctx context.Context, job *BatchImageJob, provider BatchImageProvider, account *Account, runID string, output *batchImagePumpOutput) (*BatchImageIndexResult, time.Duration, error) {
	if output == nil || output.Version != 2 || output.RunID != runID ||
		output.JobID != job.BatchID || output.Status != "completed" ||
		len(output.Files) == 0 || len(output.Files) > batchImageDeliveryMaxSourceFiles ||
		output.SourceBytes <= 0 {
		return nil, 0, ErrBatchImageDeliveryFailed
	}

	sourceProvider, ok := provider.(BatchImageSignedResultSourceProvider)
	if !ok {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	sources, err := sourceProvider.SignedResultSources(ctx, job, account, s.sourceURLTTL())
	if err != nil || len(sources) != len(output.Files) {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	var verifiedBytes int64
	for index, file := range output.Files {
		expectedKey, keyErr := BatchImageDeliveryArchiveObjectKey(s.Config, job.BatchID, index)
		if keyErr != nil || file.Index != index || file.SourceID != sources[index].ID ||
			file.ObjectKey != expectedKey || file.Size <= 0 ||
			normalizeBatchImageArchiveContentType(file.ContentType) != batchImageCOSArchiveContentType {
			return nil, 0, ErrBatchImageDeliveryFailed
		}
		size, contentType, headErr := s.Store.Head(ctx, expectedKey)
		if headErr != nil || size != file.Size ||
			normalizeBatchImageArchiveContentType(contentType) != batchImageCOSArchiveContentType {
			return nil, 0, ErrBatchImageDeliveryFailed
		}
		verifiedBytes += size
	}
	if verifiedBytes != output.SourceBytes {
		return nil, 0, ErrBatchImageDeliveryFailed
	}

	successCount, failCount, ready := s.authoritativeCompletionCounts(ctx, job, provider, account)
	if !ready {
		return nil, s.pollDelay(), nil
	}
	expected, err := s.expectedItems(ctx, job.BatchID)
	if err != nil {
		return nil, 0, err
	}
	if len(expected) != job.ItemCount || successCount+failCount != len(expected) {
		return nil, 0, ErrBatchImageDeliveryFailed
	}

	marker := batchImageCOSArchiveMarker(len(output.Files))
	now := time.Now()
	params := make([]CreateBatchImageItemParams, 0, len(expected))
	for _, item := range expected {
		params = append(params, CreateBatchImageItemParams{
			JobID:                job.BatchID,
			CustomID:             item.CustomID,
			Status:               BatchImageItemStatusResultAvailable,
			ProviderSourceObject: &marker,
			ImageCount:           1,
			IndexedAt:            &now,
		})
	}
	result := &BatchImageIndexResult{
		TotalCount:   len(expected),
		SuccessCount: successCount,
		FailCount:    failCount,
	}
	if err := s.Repo.ReplaceBatchImageItemsForJob(ctx, job.BatchID, params, BatchImageCounts{
		SuccessCount: successCount,
		FailCount:    failCount,
	}); err != nil {
		return nil, 0, err
	}
	if err := s.Repo.AppendBatchImageEvent(ctx, job.BatchID, "cos_jsonl_archive_completed", map[string]any{
		"batch_id":       job.BatchID,
		"run_id":         runID,
		"success_count":  successCount,
		"fail_count":     failCount,
		"source_files":   len(output.Files),
		"source_bytes":   output.SourceBytes,
		"storage_bucket": s.Config.BatchImage.DeliveryCOSBucket,
	}); err != nil {
		logger.L().Warn("batch_image.cos_archive_event_failed",
			zap.String("batch_id", job.BatchID),
			zap.Error(err),
		)
	}
	return result, 0, nil
}

func (s *BatchImageDeliveryService) authoritativeCompletionCounts(ctx context.Context, job *BatchImageJob, provider BatchImageProvider, account *Account) (int, int, bool) {
	status, err := provider.Get(ctx, job, account)
	if err != nil || status == nil || status.InternalState != BatchProviderStateSucceeded ||
		status.SuccessfulCount == nil || status.FailedCount == nil {
		return 0, 0, false
	}
	successCount := *status.SuccessfulCount
	failCount := *status.FailedCount
	if status.IncompleteCount != nil {
		failCount += *status.IncompleteCount
	}
	if successCount < 0 || failCount < 0 || successCount+failCount != job.ItemCount {
		return 0, 0, false
	}
	return successCount, failCount, true
}

func (s *BatchImageDeliveryService) pumpRequest(ctx context.Context, method, path string, body []byte) (*batchImagePumpStatus, int, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(s.Config.BatchImage.DeliveryWorkerURL), "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, 0, ErrBatchImageDeliveryNotConfigured
	}
	base.Path = path
	base.RawQuery = ""
	base.Fragment = ""
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	signature := signBatchImagePumpRequest(
		s.Config.BatchImage.DeliverySharedSecret,
		method,
		path,
		timestamp,
		nonce,
		body,
	)
	request, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	request.Header.Set(batchImagePumpTimestampHeader, timestamp)
	request.Header.Set(batchImagePumpNonceHeader, nonce)
	request.Header.Set(batchImagePumpSignatureHeader, signature)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, batchImagePumpControlMaxBytes+1))
	if err != nil || len(raw) > batchImagePumpControlMaxBytes {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	if response.StatusCode == http.StatusNotFound {
		return &batchImagePumpStatus{}, response.StatusCode, nil
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return nil, response.StatusCode, ErrBatchImageDeliveryFailed
	}
	var parsed batchImagePumpStatus
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, response.StatusCode, ErrBatchImageDeliveryFailed
	}
	return &parsed, response.StatusCode, nil
}

func signBatchImagePumpRequest(secret, method, path, timestamp, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		strings.ToUpper(method),
		path,
		timestamp,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func batchImageDeliveryRunID(batchID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(batchID)))
	return strings.TrimSpace(batchID) + "-" + hex.EncodeToString(sum[:8])
}

func BatchImageDeliveryArchiveObjectKey(cfg *config.Config, batchID string, fileIndex int) (string, error) {
	if cfg == nil || !IsValidBatchImageID(batchID) ||
		fileIndex < 0 || fileIndex >= batchImageDeliveryMaxSourceFiles {
		return "", ErrBatchImageCleanupUnsafePath
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.BatchImage.DeliveryCOSPrefix), "/")
	if prefix == "" || strings.Contains(prefix, "..") {
		return "", ErrBatchImageCleanupUnsafePath
	}
	return fmt.Sprintf("%s/%s/raw/%04d.jsonl", prefix, batchID, fileIndex), nil
}

func batchImageCOSArchiveMarker(fileCount int) string {
	return batchImageCOSArchiveMarkerPrefix + strconv.Itoa(fileCount)
}

func batchImageCOSArchiveFileCount(item *BatchImageItem) (int, bool) {
	if item == nil || item.ProviderSourceObject == nil {
		return 0, false
	}
	raw := strings.TrimSpace(*item.ProviderSourceObject)
	if !strings.HasPrefix(raw, batchImageCOSArchiveMarkerPrefix) {
		return 0, false
	}
	count, err := strconv.Atoi(strings.TrimPrefix(raw, batchImageCOSArchiveMarkerPrefix))
	if err != nil || count <= 0 || count > batchImageDeliveryMaxSourceFiles {
		return 0, false
	}
	return count, true
}

func isBatchImageCOSArchivedItem(item *BatchImageItem) bool {
	_, ok := batchImageCOSArchiveFileCount(item)
	return ok
}

func normalizeBatchImageArchiveContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	if value == "application/jsonl" || value == "application/ndjson" {
		return batchImageCOSArchiveContentType
	}
	return value
}

func (s *BatchImageDeliveryService) sourceURLTTL() time.Duration {
	return time.Duration(s.Config.BatchImage.DeliverySourceURLTTLSeconds) * time.Second
}

func (s *BatchImageDeliveryService) uploadURLTTL() time.Duration {
	return time.Duration(s.Config.BatchImage.DeliveryUploadURLTTLSeconds) * time.Second
}

func (s *BatchImageDeliveryService) pollDelay() time.Duration {
	seconds := s.Config.BatchImage.DeliveryPollSeconds
	if seconds <= 0 {
		seconds = 10
	}
	return time.Duration(seconds) * time.Second
}

var _ BatchImageResultDelivery = (*BatchImageDeliveryService)(nil)
