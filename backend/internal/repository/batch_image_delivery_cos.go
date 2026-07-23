package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const batchImageCOSDeleteChunkSize = 1000

type batchImageCOSDeliveryStore struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	initErr error
}

var _ service.BatchImageDeliveryObjectStore = (*batchImageCOSDeliveryStore)(nil)

// ProvideBatchImageDeliveryObjectStore builds the private COS control-plane
// client. Disabled delivery intentionally returns nil so legacy deployments do
// not need COS credentials.
func ProvideBatchImageDeliveryObjectStore(cfg *config.Config) service.BatchImageDeliveryObjectStore {
	if cfg == nil || !cfg.BatchImage.DeliveryEnabled {
		return nil
	}
	client, err := newS3Client(context.Background(), s3ClientParams{
		Endpoint:        strings.TrimSpace(cfg.BatchImage.DeliveryCOSEndpoint),
		Region:          strings.TrimSpace(cfg.BatchImage.DeliveryCOSRegion),
		AccessKeyID:     strings.TrimSpace(cfg.BatchImage.DeliveryCOSAccessKeyID),
		SecretAccessKey: strings.TrimSpace(cfg.BatchImage.DeliveryCOSSecretAccessKey),
		ForcePathStyle:  cfg.BatchImage.DeliveryCOSForcePathStyle,
	})
	store := &batchImageCOSDeliveryStore{
		client:  client,
		bucket:  strings.TrimSpace(cfg.BatchImage.DeliveryCOSBucket),
		initErr: err,
	}
	if client != nil {
		store.presign = s3.NewPresignClient(client)
	}
	return store
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
