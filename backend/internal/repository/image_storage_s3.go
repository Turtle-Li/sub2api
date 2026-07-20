package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// S3ImageStorage 用 S3 兼容对象存储实现 service.ImageStorage。
type S3ImageStorage struct {
	client        *s3.Client
	bucket        string
	publicBaseURL string
	presignExpiry time.Duration
}

var _ service.ImageStorage = (*S3ImageStorage)(nil)

// NewS3ImageStorage 依据配置构造 S3 图片存储（调用方应先确认 cfg.Active()）。
func NewS3ImageStorage(ctx context.Context, cfg *config.ImageStorageConfig) (*S3ImageStorage, error) {
	client, err := newS3Client(ctx, s3ClientParams{
		Endpoint:        cfg.Endpoint,
		Region:          cfg.Region,
		AccessKeyID:     cfg.AccessKeyID,
		SecretAccessKey: cfg.SecretAccessKey,
		ForcePathStyle:  cfg.ForcePathStyle,
	})
	if err != nil {
		return nil, err
	}

	expiry := time.Duration(cfg.PresignExpiry) * time.Hour
	if expiry <= 0 {
		expiry = 24 * time.Hour
	}
	return newS3ImageStorage(client, cfg.Bucket, cfg.PublicBaseURL, expiry), nil
}

func newS3ImageStorage(client *s3.Client, bucket, publicBaseURL string, presignExpiry time.Duration) *S3ImageStorage {
	if presignExpiry <= 0 {
		presignExpiry = 24 * time.Hour
	}
	return &S3ImageStorage{
		client:        client,
		bucket:        bucket,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		presignExpiry: presignExpiry,
	}
}

// Save 上传图片字节，返回可访问 URL：配了 public_base_url 则返回公开直链，否则返回 presigned 临时链接。
func (s *S3ImageStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: &contentType,
	})
	finish()
	if err != nil {
		return "", fmt.Errorf("S3 PutObject: %w", err)
	}

	return s.objectURL(ctx, key)
}

// Ensure reuses an existing deterministic object key when possible. This is
// used by Attachment Gateway hash caching so a process restart does not force
// the same optimized bytes through the server uplink again.
func (s *S3ImageStorage) Ensure(ctx context.Context, key, contentType string, data []byte) (string, bool, error) {
	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	finish()
	if err == nil {
		url, urlErr := s.objectURL(ctx, key)
		return url, false, urlErr
	}
	if !isS3ObjectNotFound(err) {
		return "", false, fmt.Errorf("S3 HeadObject: %w", err)
	}

	url, saveErr := s.Save(ctx, key, contentType, data)
	if saveErr != nil {
		return "", false, saveErr
	}
	return url, true, nil
}

// Probe verifies the exact permissions Attachment Gateway needs. It writes,
// reads and removes one tiny caller-named object; a best-effort detached
// cleanup also runs when the request context is cancelled midway through.
func (s *S3ImageStorage) Probe(ctx context.Context, key string) error {
	const contentType = "text/plain"
	payload := []byte("sub2api-attachment-r2-probe")
	uploaded := false
	defer func() {
		if !uploaded {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = s.client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	}()

	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(payload),
		ContentType: ptrTo(contentType),
	})
	finish()
	if err != nil {
		return fmt.Errorf("S3 PutObject probe: %w", err)
	}
	uploaded = true

	probeURL, err := s.objectURL(ctx, key)
	if err != nil {
		return fmt.Errorf("presign S3 probe object: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return fmt.Errorf("create signed S3 probe request: %w", err)
	}
	finish = servertiming.ObserveDependency(ctx, "s3")
	result, err := http.DefaultClient.Do(request)
	finish()
	if err != nil {
		// url.Error includes the full presigned URL (credential + signature).
		// Keep it out of API responses and logs.
		return errors.New("fetch signed S3 probe object failed")
	}
	read, readErr := io.ReadAll(io.LimitReader(result.Body, int64(len(payload)+1)))
	closeErr := result.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read S3 probe object: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close S3 probe object: %w", closeErr)
	}
	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("signed S3 probe returned HTTP %d", result.StatusCode)
	}
	if !bytes.Equal(read, payload) {
		return errors.New("S3 probe object content mismatch")
	}

	finish = servertiming.ObserveDependency(ctx, "s3")
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	finish()
	if err != nil {
		return fmt.Errorf("S3 DeleteObject probe: %w", err)
	}
	uploaded = false
	return nil
}

func (s *S3ImageStorage) objectURL(ctx context.Context, key string) (string, error) {
	if s.publicBaseURL != "" {
		return s.publicBaseURL + "/" + strings.TrimLeft(key, "/"), nil
	}

	presignClient := s3.NewPresignClient(s.client)
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}, s3.WithPresignExpires(s.presignExpiry))
	if err != nil {
		return "", fmt.Errorf("presign url: %w", err)
	}
	return result.URL, nil
}

func isS3ObjectNotFound(err error) bool {
	var responseErr *smithyhttp.ResponseError
	return errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusNotFound
}

func ptrTo[T any](value T) *T {
	return &value
}
