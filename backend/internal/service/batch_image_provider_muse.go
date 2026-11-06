package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const museImageMaxBytes = 20 * 1024 * 1024

type museImageTask struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Data   []struct {
		Base64 string `json:"b64_json"`
	} `json:"data"`
}
type museImageBatchExecutor struct {
	store   BatchImageProviderObjectStore
	gateway *OpenAIGatewayService
}

func NewMuseImagesBatchProvider(store BatchImageProviderObjectStore, prefix string, gateway *OpenAIGatewayService) *OpenAIImagesBatchProvider {
	executor := &museImageBatchExecutor{store: store, gateway: gateway}
	p := NewOpenAIImagesBatchProvider(store, executor, prefix)
	p.providerName = BatchImageProviderMuse
	return p
}
func (e *museImageBatchExecutor) Execute(ctx context.Context, account *Account, request OpenAIImageBatchItemRequest, before func() error) (*OpenAIImageBatchItemResult, error) {
	if !batchImageAccountSupportsProviderModel(BatchImageProviderMuse, account, request.Model) || len(request.ReferenceImages) > 1 || request.AspectRatio != "" {
		return nil, ErrBatchImageProviderInvalidInput
	}
	payload := map[string]any{"model": "muse-image", "prompt": request.Prompt, "n": 1, "response_format": "b64_json"}
	if len(request.ReferenceImages) == 1 {
		ref := request.ReferenceImages[0]
		if len(ref.Data) == 0 || ref.FileURI != "" {
			return nil, ErrBatchImageProviderInvalidInput
		}
		payload["reference_image"] = "data:" + ref.MimeType + ";base64," + base64.StdEncoding.EncodeToString(ref.Data)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(request.StateKey))
	task, err := e.call(ctx, account, http.MethodPost, "", body, hex.EncodeToString(digest[:]), before)
	if err != nil {
		return nil, err
	}
	if task.TaskID == "" || len(task.TaskID) > 256 {
		return nil, errors.New("Muse returned no valid task ID")
	}
	receipt := []byte(task.TaskID)
	if err := e.store.Put(ctx, request.StateKey, "text/plain", bytes.NewReader(receipt), int64(len(receipt))); err != nil {
		return nil, ErrBatchImageProviderStorageUnavailable.WithCause(err)
	}
	return nil, errImageBatchItemPending
}
func (e *museImageBatchExecutor) Resume(ctx context.Context, account *Account, request OpenAIImageBatchItemRequest) (*OpenAIImageBatchItemResult, error) {
	exists, err := e.store.Exists(ctx, request.StateKey)
	if err != nil {
		return nil, errImageBatchItemPending
	}
	if !exists {
		return nil, errors.New("Muse submission outcome unknown; no durable task ID")
	}
	body, size, _, err := e.store.Open(ctx, request.StateKey)
	if err != nil {
		return nil, errImageBatchItemPending
	}
	defer body.Close()
	if size <= 0 || size > 256 {
		return nil, errors.New("invalid Muse task receipt")
	}
	receipt, err := io.ReadAll(io.LimitReader(body, 257))
	if err != nil {
		return nil, errImageBatchItemPending
	}
	task, err := e.call(ctx, account, http.MethodGet, string(receipt), nil, "", nil)
	if err != nil {
		return nil, errImageBatchItemPending
	}
	switch task.Status {
	case "queued", "processing":
		return nil, errImageBatchItemPending
	case "failed":
		return nil, errors.New("Muse image task failed")
	case "completed":
		if len(task.Data) != 1 {
			return nil, errors.New("Muse returned invalid image count")
		}
		return decodeMuseImage(task.Data[0].Base64)
	default:
		return nil, errors.New("Muse returned unknown task status")
	}
}
func decodeMuseImage(encoded string) (*OpenAIImageBatchItemResult, error) {
	if len(encoded) == 0 || len(encoded) > base64.StdEncoding.EncodedLen(museImageMaxBytes) {
		return nil, errors.New("Muse image exceeds size limit")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > museImageMaxBytes {
		return nil, errors.New("Muse returned invalid image bytes")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 64*1024*1024 || (format != "png" && format != "jpeg" && format != "webp") {
		return nil, errors.New("Muse returned unsupported image content")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("Muse returned corrupt image content")
	}
	return &OpenAIImageBatchItemResult{Base64Data: encoded, MimeType: "image/" + format}, nil
}
func (e *museImageBatchExecutor) call(ctx context.Context, account *Account, method, taskID string, body []byte, key string, before func() error) (*museImageTask, error) {
	if account == nil || account.Type != AccountTypeAPIKey || account.Platform != PlatformOpenAI {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	if e.gateway == nil || e.gateway.httpUpstream == nil || e.gateway.concurrencyService == nil {
		return nil, errors.New("Muse account runtime is unavailable")
	}
	if _, err := ResolveAccountProxyURL(account); err != nil {
		return nil, err
	}
	slot, err := e.gateway.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	if slot == nil || !slot.Acquired {
		return nil, errBatchImageOpenAIAccountBusy
	}
	if slot.ReleaseFunc != nil {
		defer slot.ReleaseFunc()
	}
	// Slots cover one HTTP exchange including response consumption, not remote queue time.
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 60*time.Second)
	defer cancel()
	base := strings.TrimRight(strings.TrimSpace(account.GetCredential("base_url")), "/")
	endpoint, err := url.Parse(base)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, ErrBatchImageProviderInvalidInput
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/v1") + "/v1/images/tasks"
	if taskID != "" {
		if strings.ContainsAny(taskID, "/?#\\") || taskID == "." || taskID == ".." {
			return nil, ErrBatchImageProviderInvalidInput
		}
		endpoint.Path += "/" + taskID
	}
	endpoint.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+batchImageProviderAPIKey(account))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if before != nil {
		if err := before(); err != nil {
			return nil, err
		}
	}
	send := e.gateway.doOpenAIUpstream
	if method == http.MethodPost {
		send = e.gateway.doOpenAIModelUpstream
	}
	resp, err := send(req, "", account)
	if err != nil {
		return nil, errors.New("Muse transport failed")
	}
	defer resp.Body.Close()
	expected := http.StatusOK
	if method == http.MethodPost {
		expected = http.StatusAccepted
	}
	if resp.StatusCode != expected {
		return nil, errors.New("Muse request rejected")
	}
	limit := int64(base64.StdEncoding.EncodedLen(museImageMaxBytes) + 65536)
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("Muse response exceeds size limit")
	}
	var task museImageTask
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, errors.New("Muse response is invalid")
	}
	return &task, nil
}
