package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	batchImageCOSDeleteChunkSize     = 1000
	batchImageCOSVaultAgentSocket    = "/run/sub2api-upscale-vault/public.sock"
	batchImageCOSVaultAgentTimeout   = 3 * time.Second
	batchImageCOSVaultAgentMaxBody   = 4 * 1024
	batchImageCOSStartupProbeTimeout = 5 * time.Second
	batchImageCOSVaultAccessKeyField = "access_key_id"
	batchImageCOSVaultSecretKeyField = "secret_access_key"
)

type batchImageCOSForbidOverwriteMiddleware struct{}

func (batchImageCOSForbidOverwriteMiddleware) ID() string {
	return "BatchImageCOSForbidOverwrite"
}

func (batchImageCOSForbidOverwriteMiddleware) HandleBuild(
	ctx context.Context,
	in middleware.BuildInput,
	next middleware.BuildHandler,
) (middleware.BuildOutput, middleware.Metadata, error) {
	request, ok := in.Request.(*smithyhttp.Request)
	if !ok {
		return middleware.BuildOutput{}, middleware.Metadata{}, errors.New("unexpected COS request type")
	}
	request.Header.Set("x-cos-forbid-overwrite", "true")
	in.Request = request
	return next.HandleBuild(ctx, in)
}

type batchImageCOSDeliveryStore struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	initErr error
}

var _ service.BatchImageDeliveryObjectStore = (*batchImageCOSDeliveryStore)(nil)
var _ service.BatchImageProviderObjectStore = (*batchImageCOSDeliveryStore)(nil)

// ProvideBatchImageDeliveryObjectStore builds the private COS control-plane
// client. A complete COS configuration remains active while batch processing
// is enabled so a feature rollback can still read and clean durable 2K/4K
// objects. Legacy deployments without COS credentials still receive nil.
func ProvideBatchImageDeliveryObjectStore(cfg *config.Config) (service.BatchImageDeliveryObjectStore, error) {
	return provideBatchImageDeliveryObjectStore(cfg, nil)
}

func provideBatchImageDeliveryObjectStore(cfg *config.Config, vaultClient *http.Client) (service.BatchImageDeliveryObjectStore, error) {
	if cfg == nil || (!cfg.BatchImage.DeliveryEnabled && (!cfg.BatchImage.Enabled || !batchImageCOSConfigured(cfg))) {
		return nil, nil
	}
	credentials, err := loadBatchImageCOSVaultCredentials(
		context.Background(),
		cfg.BatchImage.DeliveryCOSVaultAgentSocket,
		cfg.BatchImage.DeliveryCOSAccessKeyVaultRef,
		cfg.BatchImage.DeliveryCOSSecretAccessKeyVaultRef,
		vaultClient,
	)
	if err != nil {
		return nil, errors.New("initialize COS delivery credentials from Vault agent failed")
	}
	defer credentials.clear()
	client, err := newS3Client(context.Background(), s3ClientParams{
		Endpoint:        strings.TrimSpace(cfg.BatchImage.DeliveryCOSEndpoint),
		Region:          strings.TrimSpace(cfg.BatchImage.DeliveryCOSRegion),
		AccessKeyID:     string(credentials.accessKeyID),
		SecretAccessKey: string(credentials.secretAccessKey),
		ForcePathStyle:  cfg.BatchImage.DeliveryCOSForcePathStyle,
	})
	if err != nil {
		return nil, errors.New("initialize COS delivery client failed")
	}
	store := &batchImageCOSDeliveryStore{
		client: client,
		bucket: strings.TrimSpace(cfg.BatchImage.DeliveryCOSBucket),
	}
	if client != nil {
		store.presign = s3.NewPresignClient(client)
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), batchImageCOSStartupProbeTimeout)
	probeErr := store.requireNeverVersionedBucket(probeCtx)
	cancelProbe()
	if probeErr != nil {
		return nil, errors.New("COS delivery atomic-create prerequisite check failed")
	}
	return store, nil
}

func batchImageCOSConfigured(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for _, value := range []string{
		cfg.BatchImage.DeliveryCOSEndpoint,
		cfg.BatchImage.DeliveryCOSRegion,
		cfg.BatchImage.DeliveryCOSBucket,
		cfg.BatchImage.DeliveryCOSAccessKeyVaultRef,
		cfg.BatchImage.DeliveryCOSSecretAccessKeyVaultRef,
		cfg.BatchImage.DeliveryCOSVaultAgentSocket,
	} {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

type batchImageCOSVaultCredentials struct {
	accessKeyID     []byte
	secretAccessKey []byte
}

func (c *batchImageCOSVaultCredentials) clear() {
	if c == nil {
		return
	}
	clear(c.accessKeyID)
	clear(c.secretAccessKey)
}

func loadBatchImageCOSVaultCredentials(
	ctx context.Context,
	socketPath, accessKeyReference, secretKeyReference string,
	injected *http.Client,
) (*batchImageCOSVaultCredentials, error) {
	if ctx == nil || ctx.Err() != nil || socketPath != batchImageCOSVaultAgentSocket ||
		!filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath {
		return nil, errors.New("invalid COS Vault agent configuration")
	}
	accessPath, accessField, accessOK := parseBatchImageCOSVaultReference(accessKeyReference)
	secretPath, secretField, secretOK := parseBatchImageCOSVaultReference(secretKeyReference)
	if !accessOK || !secretOK || accessPath != secretPath ||
		accessField != batchImageCOSVaultAccessKeyField || secretField != batchImageCOSVaultSecretKeyField {
		return nil, errors.New("invalid COS Vault references")
	}

	client := hardenedBatchImageCOSVaultClient(socketPath, injected)
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vault/v1/"+accessPath, nil)
	if err != nil {
		return nil, errors.New("create COS Vault agent request failed")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil || response == nil || response.Body == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, errors.New("COS Vault agent unavailable")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, batchImageCOSVaultAgentMaxBody+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || len(body) > batchImageCOSVaultAgentMaxBody {
		clear(body)
		return nil, errors.New("COS Vault agent response invalid")
	}
	defer clear(body)
	var envelope struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(&struct{}{}) != io.EOF || len(envelope.Data.Data) != 2 {
		return nil, errors.New("COS Vault agent payload invalid")
	}
	accessKeyID, accessExists := envelope.Data.Data[batchImageCOSVaultAccessKeyField]
	secretAccessKey, secretExists := envelope.Data.Data[batchImageCOSVaultSecretKeyField]
	if !accessExists || !secretExists || !validBatchImageCOSVaultValue(accessKeyID, 256) || !validBatchImageCOSVaultValue(secretAccessKey, 512) {
		return nil, errors.New("COS Vault agent fields invalid")
	}
	return &batchImageCOSVaultCredentials{
		accessKeyID:     []byte(accessKeyID),
		secretAccessKey: []byte(secretAccessKey),
	}, nil
}

func hardenedBatchImageCOSVaultClient(socketPath string, injected *http.Client) *http.Client {
	client := http.Client{Timeout: batchImageCOSVaultAgentTimeout}
	if injected != nil {
		client = *injected
		if client.Timeout <= 0 || client.Timeout > batchImageCOSVaultAgentTimeout {
			client.Timeout = batchImageCOSVaultAgentTimeout
		}
	} else {
		client.Transport = &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: batchImageCOSVaultAgentTimeout}).DialContext(ctx, "unix", socketPath)
			},
			DisableCompression: true,
		}
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func parseBatchImageCOSVaultReference(raw string) (string, string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || !strings.HasPrefix(raw, "vault://") || strings.Count(raw, "#") != 1 {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, "vault://"), "#", 2)
	if parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[0], "/") || strings.HasSuffix(parts[0], "/") || strings.Contains(parts[0], "//") {
		return "", "", false
	}
	for _, token := range append(strings.Split(parts[0], "/"), parts[1]) {
		if token == "." || token == ".." || !validBatchImageCOSVaultToken(token) {
			return "", "", false
		}
	}
	return parts[0], parts[1], true
}

func validBatchImageCOSVaultToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validBatchImageCOSVaultValue(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func (s *batchImageCOSDeliveryStore) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" || body == nil || size <= 0 {
		return errors.New("invalid COS delivery object")
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &key,
		Body:          body,
		ContentLength: &size,
		ContentType:   &contentType,
	})
	if err != nil {
		return errors.New("put COS delivery object failed")
	}
	return nil
}

func (s *batchImageCOSDeliveryStore) PutIfAbsent(ctx context.Context, key, contentType string, body io.Reader, size int64) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if strings.TrimSpace(key) == "" || body == nil || size <= 0 {
		return false, errors.New("invalid COS delivery object")
	}
	if err := s.requireNeverVersionedBucket(ctx); err != nil {
		return false, err
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &key,
		Body:          body,
		ContentLength: &size,
		ContentType:   &contentType,
	}, s3.WithAPIOptions(func(stack *middleware.Stack) error {
		return stack.Build.Add(batchImageCOSForbidOverwriteMiddleware{}, middleware.After)
	}))
	if err == nil {
		return true, nil
	}
	var responseErr *smithyhttp.ResponseError
	var apiErr smithy.APIError
	if errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusConflict &&
		errors.As(err, &apiErr) && apiErr.ErrorCode() == "FileAlreadyExists" {
		return false, nil
	}
	// The caller must treat an error as an uncertain claim and must never issue
	// the upstream request from this attempt.
	return false, errors.New("conditionally put COS delivery object failed")
}

func (s *batchImageCOSDeliveryStore) requireNeverVersionedBucket(ctx context.Context) error {
	result, err := s.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &s.bucket})
	if err != nil || result == nil {
		return errors.New("verify COS bucket versioning failed")
	}
	// Tencent COS ignores x-cos-forbid-overwrite after versioning has been
	// enabled. Suspended buckets are rejected too: they still have versioning
	// history and must not be treated as a never-versioned atomic claim store.
	if strings.TrimSpace(string(result.Status)) != "" {
		return errors.New("COS bucket versioning is incompatible with atomic create")
	}
	return nil
}

func (s *batchImageCOSDeliveryStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, string, error) {
	if err := s.ready(); err != nil {
		return nil, 0, "", err
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil || result == nil || result.Body == nil {
		return nil, 0, "", errors.New("open COS delivery object failed")
	}
	size := int64(0)
	if result.ContentLength != nil {
		size = *result.ContentLength
	}
	return result.Body, size, strings.TrimSpace(stringValue(result.ContentType)), nil
}

func (s *batchImageCOSDeliveryStore) ready() error {
	if s == nil {
		return errors.New("COS delivery store is disabled")
	}
	if s.initErr != nil {
		return errors.New("COS delivery store initialization failed")
	}
	if s.client == nil || s.presign == nil || s.bucket == "" {
		return errors.New("COS delivery store is not configured")
	}
	return nil
}

func (s *batchImageCOSDeliveryStore) PresignPut(ctx context.Context, key string, expires time.Duration) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	result, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expires))
	if err != nil {
		// SDK errors can include the full signed request. Never return them to a
		// caller that may log the error.
		return "", errors.New("presign COS upload failed")
	}
	return result.URL, nil
}

func (s *batchImageCOSDeliveryStore) PresignGet(ctx context.Context, key, filename string, expires time.Duration) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	disposition := service.BatchImageContentDispositionAttachment(filename)
	result, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     &s.bucket,
		Key:                        &key,
		ResponseContentDisposition: &disposition,
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", errors.New("presign COS download failed")
	}
	return result.URL, nil
}

func (s *batchImageCOSDeliveryStore) Head(ctx context.Context, key string) (int64, string, error) {
	if err := s.ready(); err != nil {
		return 0, "", err
	}
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		return 0, "", errors.New("head COS delivery object failed")
	}
	size := int64(0)
	if result.ContentLength != nil {
		size = *result.ContentLength
	}
	return size, strings.TrimSpace(stringValue(result.ContentType)), nil
}

func (s *batchImageCOSDeliveryStore) Exists(ctx context.Context, key string) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err == nil {
		return true, nil
	}
	if isS3ObjectNotFound(err) {
		return false, nil
	}
	return false, errors.New("check COS delivery object failed")
}

func (s *batchImageCOSDeliveryStore) Delete(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := s.ready(); err != nil {
		return err
	}
	for start := 0; start < len(keys); start += batchImageCOSDeleteChunkSize {
		end := start + batchImageCOSDeleteChunkSize
		if end > len(keys) {
			end = len(keys)
		}
		objects := make([]s3types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			objects = append(objects, s3types.ObjectIdentifier{Key: &key})
		}
		if len(objects) == 0 {
			continue
		}
		result, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &s.bucket,
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   boolPointer(true),
			},
		})
		if err != nil {
			return errors.New("delete COS delivery objects failed")
		}
		if len(result.Errors) > 0 {
			return fmt.Errorf("delete COS delivery objects returned %d object errors", len(result.Errors))
		}
	}
	return nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolPointer(value bool) *bool {
	return &value
}
