//go:build unit

package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fakeBatchImageDeliveryProvider struct {
	*fakeProcessorProvider
	sources []BatchImageSignedResultSource
}

func (p *fakeBatchImageDeliveryProvider) SignedResultSources(context.Context, *BatchImageJob, *Account, time.Duration) ([]BatchImageSignedResultSource, error) {
	return append([]BatchImageSignedResultSource(nil), p.sources...), nil
}

type fakeBatchImageDeliveryStore struct {
	headSize     int64
	headType     string
	presignedGet string
	putKeys      []string
	getKeys      []string
	headKeys     []string
	deletedKeys  []string
}

func (s *fakeBatchImageDeliveryStore) PresignPut(_ context.Context, key string, _ time.Duration) (string, error) {
	s.putKeys = append(s.putKeys, key)
	return "https://image-1309919944.cos.ap-shanghai.myqcloud.com/" + key + "?q-signature=secret", nil
}

func (s *fakeBatchImageDeliveryStore) PresignGet(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	s.getKeys = append(s.getKeys, key)
	return s.presignedGet, nil
}

func (s *fakeBatchImageDeliveryStore) Head(_ context.Context, key string) (int64, string, error) {
	s.headKeys = append(s.headKeys, key)
	return s.headSize, s.headType, nil
}

func (s *fakeBatchImageDeliveryStore) Delete(_ context.Context, keys []string) error {
	s.deletedKeys = append(s.deletedKeys, keys...)
	return nil
}

func TestBatchImagePumpHMACMatchesWorkerCanonicalRequest(t *testing.T) {
	signature := signBatchImagePumpRequest(
		"0123456789abcdef0123456789abcdef",
		http.MethodPost,
		"/v1/jobs",
		"1700000000",
		"0123456789abcdef",
		[]byte(`{"version":1}`),
	)
	require.Equal(t, "9f014133b3f1c34e21c1f34683ff4f4418da0fd00bf8ab7d6362e21a6c0cc76a", signature)
}

func TestGCSV4SignedURLHasVerifiableExactObjectSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Date(2026, 7, 23, 10, 11, 12, 0, time.UTC)
	signed, err := signGCSV4GetURL(
		"safe-bucket",
		"batch-image/prod/imgbatch_0123456789abcdef0123456789abcdef/output/predictions_1.jsonl",
		"svc@example.iam.gserviceaccount.com",
		privateKey,
		now,
		time.Hour,
	)
	require.NoError(t, err)
	parsed, err := url.Parse(signed)
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, gcsSignedURLHost, parsed.Host)
	require.Equal(t, "3600", parsed.Query().Get("X-Goog-Expires"))

	query := parsed.Query()
	signature, err := hex.DecodeString(query.Get("X-Goog-Signature"))
	require.NoError(t, err)
	query.Del("X-Goog-Signature")
	scope := "20260723/auto/storage/goog4_request"
	canonical := strings.Join([]string{
		http.MethodGet,
		parsed.EscapedPath(),
		query.Encode(),
		"host:" + gcsSignedURLHost + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	canonicalHash := sha256.Sum256([]byte(canonical))
	stringToSign := strings.Join([]string{
		gcsV4Algorithm,
		"20260723T101112Z",
		scope,
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	digest := sha256.Sum256([]byte(stringToSign))
	require.NoError(t, rsa.VerifyPKCS1v15(&privateKey.PublicKey, crypto.SHA256, digest[:], signature))
}

func TestBatchImageDeliveryArchivesRawJSONLAndUsesVertexCompletionStats(t *testing.T) {
	const (
		batchID  = "imgbatch_0123456789abcdef0123456789abcdef"
		customID = "cover"
		secret   = "0123456789abcdef0123456789abcdef"
	)
	success, failed, incomplete := 1, 0, 0
	repo := newFakeBatchImageRepository()
	repo.jobs[batchID] = &BatchImageJob{
		BatchID:   batchID,
		Provider:  BatchImageProviderVertex,
		Status:    BatchImageJobStatusIndexing,
		ItemCount: 1,
	}
	repo.items[batchID] = []CreateBatchImageItemParams{{
		JobID: batchID, CustomID: customID, Status: BatchImageItemStatusPending,
	}}
	store := &fakeBatchImageDeliveryStore{
		headSize: 128,
		headType: batchImageCOSArchiveContentType,
	}

	var observedRequest batchImagePumpRequest
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		expected := signBatchImagePumpRequest(
			secret,
			request.Method,
			request.URL.Path,
			request.Header.Get(batchImagePumpTimestampHeader),
			request.Header.Get(batchImagePumpNonceHeader),
			body,
		)
		require.Equal(t, expected, request.Header.Get(batchImagePumpSignatureHeader))
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"code":"NOT_FOUND"}`))
			return
		}
		require.NoError(t, json.Unmarshal(body, &observedRequest))
		require.Len(t, observedRequest.Files, 1)
		output := batchImagePumpOutput{
			Version:     2,
			RunID:       observedRequest.RunID,
			JobID:       batchID,
			Status:      "completed",
			SourceBytes: 128,
			Files: []batchImagePumpArchivedFile{{
				Index:       0,
				SourceID:    observedRequest.Files[0].SourceID,
				ObjectKey:   observedRequest.Files[0].ObjectKey,
				Size:        128,
				ContentType: batchImageCOSArchiveContentType,
				ETag:        "archive-etag",
			}},
		}
		writer.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(writer).Encode(batchImagePumpStatus{
			RunID:  observedRequest.RunID,
			Status: "complete",
			Output: &output,
		})
	}))
	defer server.Close()

	cfg := &config.Config{BatchImage: config.BatchImageConfig{
		DeliveryEnabled:             true,
		DeliveryWorkerURL:           server.URL,
		DeliverySharedSecret:        secret,
		DeliverySourceURLTTLSeconds: 3600,
		DeliveryUploadURLTTLSeconds: 3600,
		DeliveryPollSeconds:         10,
		DeliveryCOSPrefix:           "sub2-batch-image/prod/",
	}}
	delivery := NewBatchImageDeliveryService(repo, store, cfg)
	delivery.HTTPClient = server.Client()
	provider := &fakeBatchImageDeliveryProvider{
		fakeProcessorProvider: &fakeProcessorProvider{status: &BatchProviderStatus{
			InternalState:   BatchProviderStateSucceeded,
			SuccessfulCount: &success,
			FailedCount:     &failed,
			IncompleteCount: &incomplete,
		}},
		sources: []BatchImageSignedResultSource{{
			ID:  "result-001.jsonl",
			URL: "https://storage.googleapis.com/safe-bucket/output.jsonl?X-Goog-Signature=secret",
		}},
	}
	result, requeue, err := delivery.Process(context.Background(), repo.jobs[batchID], provider, &Account{})
	require.NoError(t, err)
	require.Zero(t, requeue)
	require.Equal(t, 1, result.SuccessCount)
	require.Len(t, store.putKeys, 1)
	require.Len(t, store.headKeys, 1)
	require.Equal(t, BatchImageItemStatusResultAvailable, repo.items[batchID][0].Status)
	fileCount, ok := batchImageCOSArchiveFileCount(&BatchImageItem{
		ProviderSourceObject: repo.items[batchID][0].ProviderSourceObject,
	})
	require.True(t, ok)
	require.Equal(t, 1, fileCount)
}

func TestArchivedResultReturnsShortLivedFileCapabilitiesAndRequiresClientDecode(t *testing.T) {
	const batchID = "imgbatch_0123456789abcdef0123456789abcdef"
	apiKeyID := int64(2)
	marker := batchImageCOSArchiveMarker(1)
	repo := newFakeBatchImageRepository()
	repo.jobs[batchID] = &BatchImageJob{
		BatchID:      batchID,
		UserID:       1,
		APIKeyID:     &apiKeyID,
		Provider:     BatchImageProviderVertex,
		Status:       BatchImageJobStatusCompleted,
		SuccessCount: 1,
		ItemCount:    1,
	}
	repo.items[batchID] = []CreateBatchImageItemParams{{
		JobID:                batchID,
		CustomID:             "cover",
		Status:               BatchImageItemStatusResultAvailable,
		ProviderSourceObject: &marker,
		ImageCount:           1,
	}}
	store := &fakeBatchImageDeliveryStore{
		headSize:     128,
		headType:     batchImageCOSArchiveContentType,
		presignedGet: "https://image-1309919944.cos.ap-shanghai.myqcloud.com/object?q-signature=short-lived",
	}
	cfg := &config.Config{BatchImage: config.BatchImageConfig{
		DeliveryEnabled:            true,
		DeliveryDownloadTTLSeconds: 300,
		DeliveryCOSPrefix:          "sub2-batch-image/prod/",
	}}
	service := &BatchImageDownloadService{Repo: repo, DeliveryStore: store, Config: cfg}
	owner := BatchImageOwner{UserID: 1, APIKeyID: apiKeyID}

	files, err := service.ResultFiles(context.Background(), owner, batchID)
	require.NoError(t, err)
	require.Len(t, files.Data, 1)
	require.Contains(t, files.Data[0].URL, "q-signature=short-lived")
	require.Equal(t, int64(128), files.Data[0].Size)

	_, err = service.OpenItemContent(context.Background(), owner, batchID, "cover", 0)
	require.ErrorIs(t, err, ErrBatchImageArchiveClientRequired)

	_, err = service.StreamZip(context.Background(), owner, batchID, BatchImageZipOptions{}, io.Discard)
	require.ErrorIs(t, err, ErrBatchImageArchiveClientRequired)
}
