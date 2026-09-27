package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	_ "golang.org/x/image/webp"
)

const (
	imageUpscaleVaultSocket        = "/run/sub2api-upscale-vault/public.sock"
	imageUpscaleMaxSourceBytes     = 16 * 1024 * 1024
	imageUpscaleMaxSourceDimension = 2048
	imageUpscaleMaxSourcePixels    = 2 * 1024 * 1024
)

type ImageUpscaleError struct {
	Code       string
	StatusCode int
	Temporary  bool
	Cause      error
}

func (e *ImageUpscaleError) Error() string {
	if e == nil {
		return "image upscale failed"
	}
	return "image upscale failed: " + e.Code
}

func (e *ImageUpscaleError) Unwrap() error { return e.Cause }

type ImageUpscaleResult struct {
	Data     []byte
	MimeType string
	Width    int
	Height   int
	Scale    int
}

type imageUpscaleAPIKeyLoader func(context.Context) ([]byte, error)

type ImageUpscaleService struct {
	cfg        config.ImageUpscaleConfig
	httpClient *http.Client
	loadAPIKey imageUpscaleAPIKeyLoader
	slots      chan struct{}
	waiting    atomic.Int64
}

var sharedImageUpscalers = struct {
	sync.Mutex
	byConfig map[*config.Config]*ImageUpscaleService
}{byConfig: make(map[*config.Config]*ImageUpscaleService)}

// SharedImageUpscaleService returns one limiter/client per process config so
// synchronous and batch requests share the same backpressure budget.
func SharedImageUpscaleService(cfg *config.Config) *ImageUpscaleService {
	if cfg == nil {
		return NewImageUpscaleService(nil)
	}
	sharedImageUpscalers.Lock()
	defer sharedImageUpscalers.Unlock()
	if service := sharedImageUpscalers.byConfig[cfg]; service != nil {
		return service
	}
	service := NewImageUpscaleService(cfg)
	sharedImageUpscalers.byConfig[cfg] = service
	return service
}

func NewImageUpscaleService(cfg *config.Config) *ImageUpscaleService {
	settings := config.ImageUpscaleConfig{}
	if cfg != nil {
		settings = cfg.ImageUpscale
	}
	requestTimeout := time.Duration(settings.RequestTimeoutSeconds) * time.Second
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}
	maxConcurrent := settings.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &ImageUpscaleService{
		cfg:        settings,
		httpClient: &http.Client{Timeout: requestTimeout},
		loadAPIKey: newImageUpscaleVaultLoader(settings.VaultAgentSocket, settings.APIKeyVaultRef),
		slots:      make(chan struct{}, maxConcurrent),
	}
}

func (s *ImageUpscaleService) Active() bool {
	return s != nil && s.cfg.Active() && s.loadAPIKey != nil && s.httpClient != nil
}

func Image25UpscaleScale(model, sizeTier string) (int, bool) {
	if !isImage25UpscaleModel(model) {
		return 0, false
	}
	switch strings.ToUpper(strings.TrimSpace(sizeTier)) {
	case "2K":
		return 2, true
	case "4K":
		return 4, true
	default:
		return 0, false
	}
}

func isImage25UpscaleModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gemini-2.5-flash-image", "gemini-2.5-flash-image-preview":
		return true
	default:
		return false
	}
}

func isImage25SupportedAspectRatio(aspectRatio string) bool {
	switch strings.TrimSpace(aspectRatio) {
	case "", "1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9":
		return true
	default:
		return false
	}
}

func image25RequestedUpscaleScale(model, requestedSize string) (int, bool) {
	_, scale, ok := image25RequestedUpscalePlan(model, requestedSize)
	return scale, ok
}

func image25RequestedUpscalePlan(model, requestedSize string) (string, int, bool) {
	scale, ok := Image25UpscaleScale(model, requestedSize)
	if !ok {
		return "", 0, false
	}
	return ImageBillingSize1K, scale, true
}

func (s *ImageUpscaleService) Upscale(ctx context.Context, source []byte, scale int) (*ImageUpscaleResult, error) {
	return s.upscaleWithSourceLoader(ctx, scale, false, func(context.Context) ([]byte, error) {
		return source, nil
	})
}

func (s *ImageUpscaleService) UpscaleBase64(ctx context.Context, encoded string, scale int) (*ImageUpscaleResult, error) {
	normalized, err := prepareUpscaleSourceBase64(encoded, imageUpscaleMaxSourceBytes)
	if err != nil {
		return nil, err
	}
	return s.upscaleWithSourceLoader(ctx, scale, true, func(context.Context) ([]byte, error) {
		return decodePreparedUpscaleSourceBase64(normalized)
	})
}

func (s *ImageUpscaleService) upscaleBase64FromLoader(
	ctx context.Context,
	scale int,
	loader func(context.Context) (string, error),
) (*ImageUpscaleResult, error) {
	return s.upscaleWithSourceLoader(ctx, scale, true, func(loadCtx context.Context) ([]byte, error) {
		if loader == nil {
			return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, nil)
		}
		encoded, err := loader(loadCtx)
		if err != nil {
			return nil, err
		}
		normalized, err := prepareUpscaleSourceBase64(encoded, imageUpscaleMaxSourceBytes)
		if err != nil {
			return nil, err
		}
		return decodePreparedUpscaleSourceBase64(normalized)
	})
}

func (s *ImageUpscaleService) upscaleWithSourceLoader(
	ctx context.Context,
	scale int,
	clearSource bool,
	loader func(context.Context) ([]byte, error),
) (*ImageUpscaleResult, error) {
	if !s.Active() {
		return nil, imageUpscaleError("UNAVAILABLE", 0, false, nil)
	}
	if scale != 2 && scale != 4 {
		return nil, imageUpscaleError("INVALID_SCALE", 0, false, nil)
	}
	jobCtx, cancel := imageUpscaleLifecycleContext(ctx, s.cfg)
	defer cancel()
	if err := s.acquire(jobCtx); err != nil {
		return nil, err
	}
	defer func() { <-s.slots }()
	if loader == nil {
		return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, nil)
	}
	source, err := loader(jobCtx)
	if err != nil {
		return nil, err
	}
	if clearSource {
		defer clearBytes(source)
	}
	sourceWidth, sourceHeight, _, err := validateUpscaleSource(source)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.loadAPIKey(jobCtx)
	if err != nil {
		return nil, imageUpscaleError("CREDENTIAL_UNAVAILABLE", 0, false, err)
	}
	defer clearBytes(apiKey)

	jobID, err := s.submit(jobCtx, source, scale, apiKey)
	if err != nil {
		return nil, err
	}
	if err := s.waitForCompletion(jobCtx, jobID, apiKey); err != nil {
		return nil, err
	}
	result, mimeType, err := s.downloadResult(jobCtx, jobID, apiKey)
	if err != nil {
		return nil, err
	}
	width, height, detectedMime, err := decodeUpscaleImageConfig(result)
	if err != nil {
		return nil, imageUpscaleError("INVALID_RESULT_IMAGE", 0, false, err)
	}
	if width != sourceWidth*scale || height != sourceHeight*scale {
		return nil, imageUpscaleError("INVALID_RESULT_DIMENSIONS", 0, false,
			fmt.Errorf("got %dx%d, expected %dx%d", width, height, sourceWidth*scale, sourceHeight*scale))
	}
	if mimeType == "" {
		mimeType = detectedMime
	}
	if mimeType != detectedMime {
		return nil, imageUpscaleError("INVALID_RESULT_MIME", 0, false, nil)
	}
	return &ImageUpscaleResult{Data: result, MimeType: detectedMime, Width: width, Height: height, Scale: scale}, nil
}

func prepareUpscaleSourceBase64(raw string, maxBytes int64) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= len("data:") && strings.EqualFold(raw[:len("data:")], "data:") {
		separator := strings.IndexByte(raw, ',')
		if separator < 0 || separator+1 >= len(raw) {
			return "", imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, nil)
		}
		raw = strings.TrimSpace(raw[separator+1:])
	}
	raw = strings.TrimRight(raw, "=")
	if raw == "" || len(raw)%4 == 1 {
		return "", imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, nil)
	}
	if maxBytes <= 0 || int64(base64.RawStdEncoding.DecodedLen(len(raw))) > maxBytes {
		return "", imageUpscaleError("SOURCE_LIMIT_EXCEEDED", 0, false, errors.New("source image exceeds byte limit"))
	}
	return raw, nil
}

func decodePreparedUpscaleSourceBase64(encoded string) ([]byte, error) {
	source, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, err)
	}
	return source, nil
}

func validateUpscaleSource(source []byte) (int, int, string, error) {
	if len(source) > imageUpscaleMaxSourceBytes {
		return 0, 0, "", imageUpscaleError("SOURCE_LIMIT_EXCEEDED", 0, false, errors.New("source image exceeds byte limit"))
	}
	width, height, mimeType, err := decodeUpscaleImageConfig(source)
	if err != nil {
		return 0, 0, "", imageUpscaleError("INVALID_SOURCE_IMAGE", 0, false, err)
	}
	if width > imageUpscaleMaxSourceDimension || height > imageUpscaleMaxSourceDimension ||
		int64(width)*int64(height) > imageUpscaleMaxSourcePixels {
		return 0, 0, "", imageUpscaleError("SOURCE_LIMIT_EXCEEDED", 0, false,
			fmt.Errorf("source image dimensions %dx%d exceed the 1K tier limit", width, height))
	}
	return width, height, mimeType, nil
}

func imageUpscaleLifecycleContext(ctx context.Context, cfg config.ImageUpscaleConfig) (context.Context, context.CancelFunc) {
	jobTimeout := time.Duration(cfg.JobTimeoutSeconds) * time.Second
	if jobTimeout <= 0 {
		jobTimeout = 15 * time.Minute
	}
	return context.WithTimeout(ctx, jobTimeout)
}

func (s *ImageUpscaleService) acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	default:
	}
	maxQueue := int64(s.cfg.MaxQueue)
	if maxQueue <= 0 || s.waiting.Add(1) > maxQueue {
		if maxQueue > 0 {
			s.waiting.Add(-1)
		}
		return imageUpscaleError("BACKPRESSURE", http.StatusTooManyRequests, true, nil)
	}
	defer s.waiting.Add(-1)
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return imageUpscaleError("QUEUE_TIMEOUT", 0, true, ctx.Err())
	}
}

func (s *ImageUpscaleService) submit(ctx context.Context, source []byte, scale int, apiKey []byte) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= s.cfg.RetryMax; attempt++ {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, createErr := writer.CreateFormFile("image", "source"+upscaleImageExtension(source))
		if createErr != nil {
			return "", imageUpscaleError("REQUEST_BUILD_FAILED", 0, false, createErr)
		}
		if _, createErr = part.Write(source); createErr == nil {
			createErr = writer.WriteField("scale", strconv.Itoa(scale))
		}
		if createErr == nil {
			createErr = writer.WriteField("preset", "faithful")
		}
		if closeErr := writer.Close(); createErr == nil {
			createErr = closeErr
		}
		if createErr != nil {
			return "", imageUpscaleError("REQUEST_BUILD_FAILED", 0, false, createErr)
		}
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint("/v1/upscale"), &body)
		if requestErr != nil {
			return "", imageUpscaleError("REQUEST_BUILD_FAILED", 0, false, requestErr)
		}
		req.Header.Set("Authorization", "Bearer "+string(apiKey))
		req.Header.Set("Content-Type", writer.FormDataContentType())
		resp, requestErr := s.httpClient.Do(req)
		if requestErr != nil {
			return "", imageUpscaleError("SUBMIT_TRANSPORT_FAILED", 0, true, requestErr)
		}
		responseBody, readErr := readBoundedResponse(resp, 64*1024)
		if readErr != nil {
			return "", imageUpscaleError("SUBMIT_RESPONSE_INVALID", imageUpscaleRemoteErrorStatus(resp.StatusCode), false, readErr)
		}
		if resp.StatusCode == http.StatusAccepted {
			var envelope struct {
				Job struct {
					ID string `json:"id"`
				} `json:"job"`
			}
			if err := json.Unmarshal(responseBody, &envelope); err != nil || strings.TrimSpace(envelope.Job.ID) == "" {
				return "", imageUpscaleError("SUBMIT_RESPONSE_INVALID", 0, false, err)
			}
			return strings.TrimSpace(envelope.Job.ID), nil
		}
		lastErr = imageUpscaleError(upscaleHTTPErrorCode("SUBMIT", resp.StatusCode), resp.StatusCode, resp.StatusCode == 429 || resp.StatusCode >= 500, nil)
		if resp.StatusCode != http.StatusTooManyRequests || attempt == s.cfg.RetryMax {
			return "", lastErr
		}
		if err := waitUpscaleRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil {
			return "", imageUpscaleError("SUBMIT_RETRY_TIMEOUT", resp.StatusCode, true, err)
		}
	}
	return "", lastErr
}

func (s *ImageUpscaleService) waitForCompletion(ctx context.Context, jobID string, apiKey []byte) error {
	pollInterval := time.Duration(s.cfg.PollIntervalMillis) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	for {
		var lastErr error
		for attempt := 0; attempt <= s.cfg.RetryMax; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint("/v1/jobs/"+url.PathEscape(jobID)), nil)
			if err != nil {
				return imageUpscaleError("POLL_REQUEST_FAILED", 0, false, err)
			}
			req.Header.Set("Authorization", "Bearer "+string(apiKey))
			resp, err := s.httpClient.Do(req)
			if err != nil {
				lastErr = imageUpscaleError("POLL_TRANSPORT_FAILED", 0, true, err)
			} else {
				body, readErr := readBoundedResponse(resp, 64*1024)
				if readErr != nil {
					return imageUpscaleError("POLL_RESPONSE_INVALID", imageUpscaleRemoteErrorStatus(resp.StatusCode), false, readErr)
				}
				if resp.StatusCode == http.StatusOK {
					var envelope struct {
						Job struct {
							State string `json:"state"`
						} `json:"job"`
					}
					if err := json.Unmarshal(body, &envelope); err != nil {
						return imageUpscaleError("POLL_RESPONSE_INVALID", 0, false, err)
					}
					switch strings.ToLower(strings.TrimSpace(envelope.Job.State)) {
					case "completed":
						return nil
					case "failed":
						return imageUpscaleError("REMOTE_JOB_FAILED", 0, false, nil)
					case "queued", "running":
						lastErr = nil
					default:
						return imageUpscaleError("POLL_RESPONSE_INVALID", 0, false, nil)
					}
					break
				}
				lastErr = imageUpscaleError(upscaleHTTPErrorCode("POLL", resp.StatusCode), resp.StatusCode, resp.StatusCode == 429 || resp.StatusCode >= 500, nil)
			}
			if lastErr == nil || attempt == s.cfg.RetryMax {
				break
			}
			var upscaleErr *ImageUpscaleError
			if !errors.As(lastErr, &upscaleErr) || !upscaleErr.Temporary {
				return lastErr
			}
			if err := waitUpscaleRetry(ctx, "", attempt); err != nil {
				return imageUpscaleError("POLL_RETRY_TIMEOUT", 0, true, err)
			}
		}
		if lastErr != nil {
			return lastErr
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return imageUpscaleError("JOB_TIMEOUT", 0, true, ctx.Err())
		case <-timer.C:
		}
	}
}

func (s *ImageUpscaleService) downloadResult(ctx context.Context, jobID string, apiKey []byte) ([]byte, string, error) {
	var lastErr error
	for attempt := 0; attempt <= s.cfg.RetryMax; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint("/v1/jobs/"+url.PathEscape(jobID)+"/result"), nil)
		if err != nil {
			return nil, "", imageUpscaleError("RESULT_REQUEST_FAILED", 0, false, err)
		}
		req.Header.Set("Authorization", "Bearer "+string(apiKey))
		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = imageUpscaleError("RESULT_TRANSPORT_FAILED", 0, true, err)
		} else if resp.StatusCode == http.StatusOK {
			limit := s.cfg.MaxResultBytes
			if limit <= 0 {
				limit = 128 * 1024 * 1024
			}
			body, readErr := readBoundedResponse(resp, limit)
			if readErr != nil {
				return nil, "", imageUpscaleError("RESULT_TOO_LARGE", 0, false, readErr)
			}
			return body, normalizeUpscaleMime(resp.Header.Get("Content-Type")), nil
		} else {
			_, _ = readBoundedResponse(resp, 64*1024)
			lastErr = imageUpscaleError(upscaleHTTPErrorCode("RESULT", resp.StatusCode), resp.StatusCode, resp.StatusCode == 429 || resp.StatusCode >= 500, nil)
		}
		var upscaleErr *ImageUpscaleError
		if attempt == s.cfg.RetryMax || !errors.As(lastErr, &upscaleErr) || !upscaleErr.Temporary {
			return nil, "", lastErr
		}
		if err := waitUpscaleRetry(ctx, "", attempt); err != nil {
			return nil, "", imageUpscaleError("RESULT_RETRY_TIMEOUT", 0, true, err)
		}
	}
	return nil, "", lastErr
}

func (s *ImageUpscaleService) endpoint(path string) string {
	return strings.TrimRight(strings.TrimSpace(s.cfg.BaseURL), "/") + path
}

func newImageUpscaleVaultLoader(socketPath, rawReference string) imageUpscaleAPIKeyLoader {
	socketPath = strings.TrimSpace(socketPath)
	rawReference = strings.TrimSpace(rawReference)
	if socketPath != imageUpscaleVaultSocket || !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath {
		return nil
	}
	path, field, ok := parseImageUpscaleVaultReference(rawReference)
	if !ok {
		return nil
	}
	return func(ctx context.Context) ([]byte, error) {
		transport := &http.Transport{
			Proxy: nil,
			DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(dialCtx, "unix", socketPath)
			},
			DisableCompression: true,
		}
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer client.CloseIdleConnections()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vault/v1/"+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := readBoundedResponse(resp, 4*1024)
		if err != nil || resp.StatusCode != http.StatusOK {
			return nil, errors.New("image upscale vault agent unavailable")
		}
		var envelope struct {
			Data struct {
				Data map[string]string `json:"data"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data.Data) != 1 {
			return nil, errors.New("image upscale vault response invalid")
		}
		value, exists := envelope.Data.Data[field]
		if !exists || value == "" || strings.TrimSpace(value) != value || len(value) > 512 {
			return nil, errors.New("image upscale vault field invalid")
		}
		return []byte(value), nil
	}
}

func parseImageUpscaleVaultReference(raw string) (string, string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || !strings.HasPrefix(raw, "vault://") || strings.Count(raw, "#") != 1 {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, "vault://"), "#", 2)
	if parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[0], "/") || strings.HasSuffix(parts[0], "/") || strings.Contains(parts[0], "//") {
		return "", "", false
	}
	for _, token := range append(strings.Split(parts[0], "/"), parts[1]) {
		if token == "." || token == ".." || !validImageUpscaleVaultToken(token) {
			return "", "", false
		}
	}
	return parts[0], parts[1], true
}

func validImageUpscaleVaultToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func decodeUpscaleImageConfig(data []byte) (int, int, string, error) {
	if len(data) == 0 {
		return 0, 0, "", errors.New("empty image")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, "", errors.New("unsupported or corrupt image")
	}
	mimeType := normalizeUpscaleMime("image/" + format)
	if mimeType == "" {
		return 0, 0, "", errors.New("unsupported image MIME")
	}
	return cfg.Width, cfg.Height, mimeType, nil
}

func normalizeUpscaleMime(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	switch value {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/webp":
		return "image/webp"
	default:
		return ""
	}
}

func upscaleImageExtension(data []byte) string {
	_, _, mimeType, _ := decodeUpscaleImageConfig(data)
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func readBoundedResponse(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		clearBytes(body)
		return nil, errors.New("response body exceeds limit")
	}
	return body, nil
}

func waitUpscaleRetry(ctx context.Context, retryAfter string, attempt int) error {
	delay := time.Duration(1<<min(attempt, 4)) * 250 * time.Millisecond
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds > 0 && seconds <= 30 {
		delay = time.Duration(seconds) * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func upscaleHTTPErrorCode(operation string, status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return operation + "_AUTH_FAILED"
	case http.StatusTooManyRequests:
		return operation + "_RATE_LIMITED"
	case http.StatusRequestEntityTooLarge:
		return operation + "_TOO_LARGE"
	case http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return operation + "_INVALID_INPUT"
	default:
		if status >= 500 {
			return operation + "_UPSTREAM_UNAVAILABLE"
		}
		return operation + "_HTTP_ERROR"
	}
}

func imageUpscaleRemoteErrorStatus(status int) int {
	if status >= http.StatusBadRequest {
		return status
	}
	return 0
}

func imageUpscaleError(code string, status int, temporary bool, cause error) error {
	return &ImageUpscaleError{Code: code, StatusCode: status, Temporary: temporary, Cause: cause}
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
