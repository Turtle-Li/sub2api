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
	batchImageCOSDeliveryMarker   = "cos:v1"
	batchImagePumpControlMaxBytes = 1280 * 1024
	batchImagePumpRequestMaxBytes = 900 * 1024
	batchImagePumpTimestampHeader = "X-Turtle-Pump-Timestamp"
	batchImagePumpNonceHeader     = "X-Turtle-Pump-Nonce"
	batchImagePumpSignatureHeader = "X-Turtle-Pump-Signature"
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

type batchImagePumpSource struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type batchImagePumpUpload struct {
	Index     int    `json:"index"`
	ObjectKey string `json:"object_key"`
	URL       string `json:"url"`
}

type batchImagePumpRequestItem struct {
	CustomID string                 `json:"custom_id"`
	Uploads  []batchImagePumpUpload `json:"uploads"`
}

type batchImagePumpRequest struct {
	Version int                         `json:"version"`
	RunID   string                      `json:"run_id"`
	JobID   string                      `json:"job_id"`
	Sources []batchImagePumpSource      `json:"sources"`
	Items   []batchImagePumpRequestItem `json:"items"`
}

type batchImagePumpImage struct {
	Index     int    `json:"index"`
	ObjectKey string `json:"object_key"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	MimeType  string `json:"mime_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ETag      string `json:"etag"`
}

type batchImagePumpError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type batchImagePumpResultItem struct {
	CustomID string                `json:"custom_id"`
	Status   string                `json:"status"`
	Error    *batchImagePumpError  `json:"error"`
	Images   []batchImagePumpImage `json:"images"`
}

type batchImagePumpOutput struct {
	Version      int                        `json:"version"`
	RunID        string                     `json:"run_id"`
	JobID        string                     `json:"job_id"`
	Status       string                     `json:"status"`
	SuccessCount int                        `json:"success_count"`
	FailCount    int                        `json:"fail_count"`
	UnknownCount int                        `json:"unknown_count"`
	SourceBytes  int64                      `json:"source_bytes"`
	ImageBytes   int64                      `json:"image_bytes"`
	Items        []batchImagePumpResultItem `json:"items"`
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
		request, err := s.buildPumpRequest(ctx, job, provider, account, runID)
		if err != nil {
			return nil, 0, err
		}
		body, err := json.Marshal(request)
		if err != nil || len(body) > batchImagePumpRequestMaxBytes {
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
		return s.indexCompletedDelivery(ctx, job, runID, status.Output)
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
	expected, err := s.expectedItems(ctx, job.BatchID)
	if err != nil {
		return nil, err
	}
	if len(expected) == 0 {
		return nil, ErrBatchImageIndexNoResultLines
	}
	request := &batchImagePumpRequest{
		Version: 1,
		RunID:   runID,
		JobID:   job.BatchID,
		Sources: make([]batchImagePumpSource, 0, len(sources)),
		Items:   make([]batchImagePumpRequestItem, 0, len(expected)),
	}
	for _, source := range sources {
		request.Sources = append(request.Sources, batchImagePumpSource{ID: source.ID, URL: source.URL})
	}
	maxImages := s.Config.BatchImage.MaxOutputImagesPerItem
	if maxImages <= 0 || maxImages > 16 {
		maxImages = 4
	}
	for _, item := range expected {
		out := batchImagePumpRequestItem{
			CustomID: item.CustomID,
			Uploads:  make([]batchImagePumpUpload, 0, maxImages),
		}
		for imageIndex := 0; imageIndex < maxImages; imageIndex++ {
			key, err := BatchImageDeliveryObjectKey(s.Config, job.BatchID, item.CustomID, imageIndex)
			if err != nil {
				return nil, err
			}
			signed, err := s.Store.PresignPut(ctx, key, s.uploadURLTTL())
			if err != nil {
				return nil, ErrBatchImageDeliveryFailed
			}
			out.Uploads = append(out.Uploads, batchImagePumpUpload{
				Index:     imageIndex,
				ObjectKey: key,
				URL:       signed,
			})
		}
		request.Items = append(request.Items, out)
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

func (s *BatchImageDeliveryService) indexCompletedDelivery(ctx context.Context, job *BatchImageJob, runID string, output *batchImagePumpOutput) (*BatchImageIndexResult, time.Duration, error) {
	if output == nil || output.Version != 1 || output.RunID != runID || output.JobID != job.BatchID || output.Status != "completed" {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	if output.SuccessCount < 0 || output.FailCount < 0 || output.UnknownCount < 0 ||
		output.SourceBytes < 0 || output.ImageBytes < 0 {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	expected, err := s.expectedItems(ctx, job.BatchID)
	if err != nil {
		return nil, 0, err
	}
	expectedByID := make(map[string]*BatchImageItem, len(expected))
	for _, item := range expected {
		expectedByID[item.CustomID] = item
	}
	if len(output.Items) != len(expectedByID) || output.SuccessCount+output.FailCount != len(expectedByID) {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	seen := make(map[string]struct{}, len(output.Items))
	now := time.Now()
	params := make([]CreateBatchImageItemParams, 0, len(output.Items))
	result := &BatchImageIndexResult{}
	for _, item := range output.Items {
		if _, ok := expectedByID[item.CustomID]; !ok {
			return nil, 0, ErrBatchImageDeliveryFailed
		}
		if _, duplicate := seen[item.CustomID]; duplicate {
			return nil, 0, ErrBatchImageDuplicateCustomID
		}
		seen[item.CustomID] = struct{}{}
		param := CreateBatchImageItemParams{
			JobID:     job.BatchID,
			CustomID:  item.CustomID,
			Status:    BatchImageItemStatusFailed,
			IndexedAt: &now,
		}
		switch item.Status {
		case "succeeded":
			if len(item.Images) == 0 {
				return nil, 0, ErrBatchImageDeliveryFailed
			}
			primaryMime := normalizeBatchImageDeliveryMime(item.Images[0].MimeType)
			for imageIndex, image := range item.Images {
				expectedKey, err := BatchImageDeliveryObjectKey(s.Config, job.BatchID, item.CustomID, imageIndex)
				if err != nil || image.Index != imageIndex || image.ObjectKey != expectedKey ||
					image.Size <= 0 || !isBatchImageDeliveryMime(image.MimeType) ||
					normalizeBatchImageDeliveryMime(image.MimeType) != primaryMime ||
					image.Width <= 0 || image.Height <= 0 || !isSHA256Hex(image.SHA256) {
					return nil, 0, ErrBatchImageDeliveryFailed
				}
				size, contentType, err := s.Store.Head(ctx, image.ObjectKey)
				if err != nil || size != image.Size ||
					normalizeBatchImageDeliveryMime(contentType) != normalizeBatchImageDeliveryMime(image.MimeType) {
					return nil, 0, ErrBatchImageDeliveryFailed
				}
			}
			mimeType := primaryMime
			extension := batchImageFileExtension(mimeType)
			marker := batchImageCOSDeliveryMarker
			param.Status = BatchImageItemStatusSuccess
			param.ProviderSourceObject = &marker
			param.MimeType = &mimeType
			param.FileExtension = &extension
			param.ImageCount = len(item.Images)
			result.SuccessCount++
		case "failed":
			code := sanitizeBatchImageDeliveryCode(item.Error)
			message := "provider returned an item error"
			if item.Error != nil && strings.TrimSpace(item.Error.Message) != "" {
				message = sanitizeBatchImagePublicMessage(item.Error.Message)
			}
			param.ErrorCode = &code
			param.ErrorMessage = &message
			result.FailCount++
		default:
			return nil, 0, ErrBatchImageDeliveryFailed
		}
		params = append(params, param)
		result.TotalCount++
	}
	if result.SuccessCount != output.SuccessCount || result.FailCount != output.FailCount {
		return nil, 0, ErrBatchImageDeliveryFailed
	}
	if err := s.Repo.ReplaceBatchImageItemsForJob(ctx, job.BatchID, params, BatchImageCounts{
		SuccessCount: result.SuccessCount,
		FailCount:    result.FailCount,
	}); err != nil {
		return nil, 0, err
	}
	if err := s.Repo.AppendBatchImageEvent(ctx, job.BatchID, "cos_delivery_completed", map[string]any{
		"batch_id":       job.BatchID,
		"run_id":         runID,
		"success_count":  result.SuccessCount,
		"fail_count":     result.FailCount,
		"unknown_count":  output.UnknownCount,
		"source_bytes":   output.SourceBytes,
		"image_bytes":    output.ImageBytes,
		"storage_bucket": s.Config.BatchImage.DeliveryCOSBucket,
	}); err != nil {
		logger.L().Warn("batch_image.cos_delivery_event_failed",
			zap.String("batch_id", job.BatchID),
			zap.Error(err),
		)
	}
	return result, 0, nil
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
		// url.Error embeds the request URL. Keep even the Worker origin out of
		// propagated operational errors for a consistent no-capability-log rule.
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

func BatchImageDeliveryObjectKey(cfg *config.Config, batchID, customID string, imageIndex int) (string, error) {
	if cfg == nil || !IsValidBatchImageID(batchID) || strings.TrimSpace(customID) == "" ||
		imageIndex < 0 || imageIndex >= 16 {
		return "", ErrBatchImageCleanupUnsafePath
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.BatchImage.DeliveryCOSPrefix), "/")
	if prefix == "" || strings.Contains(prefix, "..") {
		return "", ErrBatchImageCleanupUnsafePath
	}
	customHash := sha256.Sum256([]byte(customID))
	return fmt.Sprintf("%s/%s/%s/%d", prefix, batchID, hex.EncodeToString(customHash[:]), imageIndex), nil
}

func isBatchImageCOSDeliveredItem(item *BatchImageItem) bool {
	return item != nil && strings.TrimSpace(batchImageDerefString(item.ProviderSourceObject)) == batchImageCOSDeliveryMarker
}

func isBatchImageDeliveryMime(value string) bool {
	switch normalizeBatchImageDeliveryMime(value) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func normalizeBatchImageDeliveryMime(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	if value == "image/jpg" {
		return "image/jpeg"
	}
	return value
}

func isSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sanitizeBatchImageDeliveryCode(itemError *batchImagePumpError) string {
	code := "PROVIDER_ITEM_FAILED"
	if itemError != nil && strings.TrimSpace(itemError.Code) != "" {
		code = strings.TrimSpace(itemError.Code)
	}
	var out strings.Builder
	for _, character := range code {
		switch {
		case character >= 'A' && character <= 'Z',
			character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '_',
			character == '-':
			_, _ = out.WriteRune(character)
		}
		if out.Len() >= 100 {
			break
		}
	}
	if out.Len() == 0 {
		return "PROVIDER_ITEM_FAILED"
	}
	return out.String()
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
