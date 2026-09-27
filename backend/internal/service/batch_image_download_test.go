//go:build unit

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBatchImageDownloadService_OpenItemContent(t *testing.T) {
	ctx := context.Background()

	t.Run("streams image bytes with safe headers data", func(t *testing.T) {
		svc, _, limiter := newTestBatchImageDownloadService()

		stream, err := svc.OpenItemContent(ctx, testBatchImageOwner(), "imgbatch_download", "cover/../001", 1)
		require.NoError(t, err)
		defer stream.Reader.Close()

		body, err := io.ReadAll(stream.Reader)
		require.NoError(t, err)
		require.Equal(t, []byte("second"), body)
		require.Equal(t, "image/jpeg", stream.ContentType)
		require.Equal(t, "cover___001.jpg", stream.Filename)
		require.Equal(t, 1, limiter.acquireCount)
		require.Zero(t, limiter.releaseCount)
		require.NoError(t, stream.Reader.Close())
		require.Equal(t, 1, limiter.releaseCount)
	})

	t.Run("streams marked upscaled image from its deterministic object", func(t *testing.T) {
		svc, repo, limiter, store := newTestBatchImageUpscaledDownloadService()
		job := repo.jobs["imgbatch_download"]
		marker := batchImageUpscaleMarker
		mime := "image/png"
		extension := "png"
		repo.items[job.BatchID] = []CreateBatchImageItemParams{{
			JobID:                job.BatchID,
			CustomID:             "cover/../001",
			Status:               BatchImageItemStatusSuccess,
			ProviderSourceObject: &marker,
			MimeType:             &mime,
			FileExtension:        &extension,
			ImageCount:           1,
		}}

		key, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "cover/../001", 0, extension)
		require.NoError(t, err)
		store.setObject(key, mime, []byte("upscaled-image"))

		stream, err := svc.OpenItemContent(ctx, testBatchImageOwner(), job.BatchID, "cover/../001", 0)
		require.NoError(t, err)
		t.Cleanup(func() { _ = stream.Reader.Close() })

		body, err := io.ReadAll(stream.Reader)
		require.NoError(t, err)
		require.Equal(t, []byte("upscaled-image"), body)
		require.Equal(t, []string{key}, store.opened)
		require.Equal(t, "image/png", stream.ContentType)
		require.Equal(t, "cover___001.png", stream.Filename)
		require.NotNil(t, stream.ContentLength)
		require.EqualValues(t, len(body), *stream.ContentLength)
		require.Zero(t, store.readerCloseCount)
		require.Zero(t, limiter.releaseCount)

		require.NoError(t, stream.Reader.Close())
		require.Equal(t, 1, store.readerCloseCount)
		require.Equal(t, 1, limiter.releaseCount)
		require.NoError(t, stream.Reader.Close())
		require.Equal(t, 1, store.readerCloseCount)
		require.Equal(t, 1, limiter.releaseCount)
	})

	t.Run("bounds a blocking upscaled object open", func(t *testing.T) {
		svc, repo, limiter, store := newTestBatchImageUpscaledDownloadService()
		job := repo.jobs["imgbatch_download"]
		marker := batchImageUpscaleMarker
		mime := "image/png"
		extension := "png"
		repo.items[job.BatchID] = []CreateBatchImageItemParams{{
			JobID: job.BatchID, CustomID: "blocked", Status: BatchImageItemStatusSuccess,
			ProviderSourceObject: &marker, MimeType: &mime, FileExtension: &extension, ImageCount: 1,
		}}
		store.blockOpen = true
		requestCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := svc.OpenItemContent(requestCtx, testBatchImageOwner(), job.BatchID, "blocked", 0)
		require.ErrorIs(t, err, ErrBatchImageResultMissing)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 1, limiter.acquireCount)
		require.Equal(t, 1, limiter.releaseCount)
	})

	tests := []struct {
		name   string
		mutate func(*fakeBatchImageRepository)
		id     string
		item   string
		index  int
		want   error
	}{
		{name: "non_owner", id: "imgbatch_download", item: "cover/../001", mutate: func(r *fakeBatchImageRepository) {
			v := int64(999)
			r.jobs["imgbatch_download"].APIKeyID = &v
		}, want: ErrBatchImageJobNotFound},
		{name: "not_completed", id: "imgbatch_download", item: "cover/../001", mutate: func(r *fakeBatchImageRepository) {
			r.jobs["imgbatch_download"].Status = BatchImageJobStatusRunning
		}, want: ErrBatchImageNotReady},
		{name: "output_deleted", id: "imgbatch_download", item: "cover/../001", mutate: func(r *fakeBatchImageRepository) {
			r.jobs["imgbatch_download"].Status = BatchImageJobStatusOutputDeleted
		}, want: ErrBatchImageOutputDeleted},
		{name: "missing_item", id: "imgbatch_download", item: "missing", want: ErrBatchImageItemNotFound},
		{name: "failed_item", id: "imgbatch_download", item: "bad", want: ErrBatchImageItemFailed},
		{name: "out_of_range", id: "imgbatch_download", item: "cover/../001", index: 2, want: ErrBatchImageItemImageIndexOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo, _ := newTestBatchImageDownloadService()
			if tt.mutate != nil {
				tt.mutate(repo)
			}

			got, err := svc.OpenItemContent(ctx, testBatchImageOwner(), tt.id, tt.item, tt.index)
			require.Nil(t, got)
			require.ErrorIs(t, err, tt.want)
			require.NotContains(t, err.Error(), batchImageDownloadTestBase64)
			require.NotContains(t, err.Error(), "providers/")
			require.NotContains(t, err.Error(), "gs://")
		})
	}
}

func TestBatchImageUpscaleRollbackDisabledStillReadsAndCleansObjects(t *testing.T) {
	svc, repo, _, store := newTestBatchImageUpscaledDownloadService()
	require.False(t, svc.Config.ImageUpscale.Enabled)
	job := repo.jobs["imgbatch_download"]
	finishedAt := time.Now().Add(-BatchImageUpscaleCleanupGrace - time.Minute)
	job.FinishedAt = &finishedAt
	marker := batchImageUpscaleMarker
	mime := "image/png"
	extension := "png"
	customID := "rollback-readable"
	repo.items[job.BatchID] = []CreateBatchImageItemParams{{
		JobID: job.BatchID, CustomID: customID, Status: BatchImageItemStatusSuccess,
		ProviderSourceObject: &marker, MimeType: &mime, FileExtension: &extension, ImageCount: 1,
	}}
	key, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, customID, 0, extension)
	require.NoError(t, err)
	store.setObject(key, mime, []byte("durable-after-rollback"))

	stream, err := svc.OpenItemContent(context.Background(), testBatchImageOwner(), job.BatchID, customID, 0)
	require.NoError(t, err)
	body, err := io.ReadAll(stream.Reader)
	require.NoError(t, err)
	require.NoError(t, stream.Reader.Close())
	require.Equal(t, []byte("durable-after-rollback"), body)

	cleanup := &BatchImageCleanupService{
		Repo: repo, ProviderRegistry: svc.ProviderRegistry, AccountResolver: svc.AccountResolver,
		DeliveryStore: store, Config: svc.Config,
	}
	err = cleanup.CleanupOutput(context.Background(), job.BatchID, "rollback")
	require.NoError(t, err)
	require.NotContains(t, store.objects, key)
	require.NotNil(t, job.OutputDeletedAt)
}

func TestBatchImageDownloadService_StreamZip(t *testing.T) {
	ctx := context.Background()

	t.Run("streams zip with images manifest and errors", func(t *testing.T) {
		svc, _, limiter := newTestBatchImageDownloadService()
		var buf bytes.Buffer

		result, err := svc.StreamZip(ctx, testBatchImageOwner(), "imgbatch_download", BatchImageZipOptions{}, &buf)
		require.NoError(t, err)
		require.Equal(t, 3, result.FileCount)
		require.Equal(t, 1, limiter.acquireCount)
		require.Equal(t, 1, limiter.releaseCount)

		files := readZipFiles(t, buf.Bytes())
		require.Equal(t, []byte("first"), files["images/cover___001.png"])
		require.Equal(t, []byte("second"), files["images/cover___001_2.jpg"])
		require.Equal(t, []byte("third"), files["images/ok_2.webp"])
		require.Contains(t, files, "manifest.json")
		require.Contains(t, files, "errors.json")

		zipText := string(bytes.Join(mapValues(files), []byte("\n")))
		require.NotContains(t, zipText, batchImageDownloadTestBase64)
		require.NotContains(t, zipText, "provider_job_name")
		require.NotContains(t, zipText, "provider_input_ref")
		require.NotContains(t, zipText, "gcs_output_uri")
		require.NotContains(t, zipText, "account_id")
		require.NotContains(t, zipText, "providers/")
		require.NotContains(t, zipText, "gs://")

		var manifest struct {
			Files []struct {
				CustomID   string `json:"custom_id"`
				Filename   string `json:"filename"`
				MimeType   string `json:"mime_type"`
				ImageIndex int    `json:"image_index"`
			} `json:"files"`
		}
		require.NoError(t, json.Unmarshal(files["manifest.json"], &manifest))
		require.Len(t, manifest.Files, 3)
		require.Equal(t, "images/cover___001_2.jpg", manifest.Files[1].Filename)
		require.Equal(t, 1, manifest.Files[1].ImageIndex)

		var errorsJSON []map[string]string
		require.NoError(t, json.Unmarshal(files["errors.json"], &errorsJSON))
		require.Len(t, errorsJSON, 1)
		require.Equal(t, "bad", errorsJSON[0]["custom_id"])
		require.Equal(t, "SAFETY_BLOCKED", errorsJSON[0]["code"])
	})

	t.Run("streams marked upscaled objects and manifest", func(t *testing.T) {
		svc, repo, limiter, store := newTestBatchImageUpscaledDownloadService()
		job := repo.jobs["imgbatch_download"]
		marker := batchImageUpscaleMarker
		png := "image/png"
		pngExtension := "png"
		webp := "image/webp"
		webpExtension := "webp"
		repo.items[job.BatchID] = []CreateBatchImageItemParams{
			{
				JobID:                job.BatchID,
				CustomID:             "cover/../001",
				Status:               BatchImageItemStatusSuccess,
				ProviderSourceObject: &marker,
				MimeType:             &png,
				FileExtension:        &pngExtension,
				ImageCount:           2,
			},
			{
				JobID:                job.BatchID,
				CustomID:             "artwork-02",
				Status:               BatchImageItemStatusSuccess,
				ProviderSourceObject: &marker,
				MimeType:             &webp,
				FileExtension:        &webpExtension,
				ImageCount:           1,
			},
		}
		job.ItemCount = 2
		job.SuccessCount = 2
		job.FailCount = 0

		coverFirstKey, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "cover/../001", 0, pngExtension)
		require.NoError(t, err)
		coverSecondKey, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "cover/../001", 1, pngExtension)
		require.NoError(t, err)
		artworkKey, err := batchImageUpscaleObjectKey(svc.Config, job.BatchID, "artwork-02", 0, webpExtension)
		require.NoError(t, err)
		store.setObject(coverFirstKey, png, []byte("upscaled-first"))
		store.setObject(coverSecondKey, png, []byte("upscaled-second"))
		store.setObject(artworkKey, webp, []byte("upscaled-third"))

		var buf bytes.Buffer
		result, err := svc.StreamZip(ctx, testBatchImageOwner(), job.BatchID, BatchImageZipOptions{}, &buf)
		require.NoError(t, err)
		require.Equal(t, 3, result.FileCount)
		require.Zero(t, result.ErrorCount)
		require.Equal(t, 1, limiter.acquireCount)
		require.Equal(t, 1, limiter.releaseCount)
		require.Equal(t, []string{coverFirstKey, coverSecondKey, artworkKey}, store.opened)
		require.Equal(t, 3, store.readerCloseCount)

		files := readZipFiles(t, buf.Bytes())
		require.Equal(t, []byte("upscaled-first"), files["images/cover___001.png"])
		require.Equal(t, []byte("upscaled-second"), files["images/cover___001_2.png"])
		require.Equal(t, []byte("upscaled-third"), files["images/artwork-02.webp"])

		var manifest struct {
			Files []struct {
				CustomID   string `json:"custom_id"`
				Filename   string `json:"filename"`
				MimeType   string `json:"mime_type"`
				ImageIndex int    `json:"image_index"`
			} `json:"files"`
		}
		require.NoError(t, json.Unmarshal(files["manifest.json"], &manifest))
		require.Len(t, manifest.Files, 3)
		require.Equal(t, "cover/../001", manifest.Files[0].CustomID)
		require.Equal(t, "images/cover___001.png", manifest.Files[0].Filename)
		require.Equal(t, "image/png", manifest.Files[0].MimeType)
		require.Equal(t, 0, manifest.Files[0].ImageIndex)
		require.Equal(t, "images/cover___001_2.png", manifest.Files[1].Filename)
		require.Equal(t, 1, manifest.Files[1].ImageIndex)
		require.Equal(t, "artwork-02", manifest.Files[2].CustomID)
		require.Equal(t, "images/artwork-02.webp", manifest.Files[2].Filename)
		require.Equal(t, "image/webp", manifest.Files[2].MimeType)
		require.Equal(t, 0, manifest.Files[2].ImageIndex)
	})

	t.Run("limiter denial returns public limit error", func(t *testing.T) {
		svc, _, limiter := newTestBatchImageDownloadService()
		limiter.deny = true
		var buf bytes.Buffer

		result, err := svc.StreamZip(ctx, testBatchImageOwner(), "imgbatch_download", BatchImageZipOptions{}, &buf)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrBatchImageDownloadLimited)
		require.Empty(t, buf.Bytes())
	})

	t.Run("rejects too many zip items before opening output", func(t *testing.T) {
		svc, repo, _ := newTestBatchImageDownloadService()
		repo.jobs["imgbatch_download"].SuccessCount = 3
		svc.Config.BatchImage.MaxDownloadItemsZip = 1
		var buf bytes.Buffer

		result, err := svc.StreamZip(ctx, testBatchImageOwner(), "imgbatch_download", BatchImageZipOptions{}, &buf)
		require.Nil(t, result)
		require.ErrorIs(t, err, ErrBatchImageZipTooManyItems)
		require.Empty(t, buf.Bytes())
	})
}

func TestExtractBatchImagePartsFromResultLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantID    string
		wantMime  string
		wantError string
	}{
		{name: "inlineData_mimeType_response", line: `{"key":"a","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + batchImageDownloadTestBase64 + `"}}]}}]}}`, wantID: "a", wantMime: "image/png"},
		{name: "inline_data_mime_type_top_level", line: `{"custom_id":"b","candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/jpeg","data":"` + batchImageDownloadTestBase64 + `"}}]}}]}`, wantID: "b", wantMime: "image/jpeg"},
		{name: "status_failure", line: `{"key":"c","status":{"code":"INVALID_ARGUMENT","message":"bad prompt"}}`, wantID: "c", wantError: "INVALID_ARGUMENT"},
		{name: "error_failure", line: `{"key":"d","error":{"code":"SAFETY","message":"blocked"}}`, wantID: "d", wantError: "SAFETY_BLOCKED"},
		{name: "empty_output", line: `{"key":"e","response":{"candidates":[{"content":{"parts":[{"text":"none"}]}}]}}`, wantID: "e", wantError: "EMPTY_IMAGE_OUTPUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractBatchImagePartsFromResultLine([]byte(tt.line))
			require.NoError(t, err)
			require.Equal(t, tt.wantID, got.CustomID)
			if tt.wantMime != "" {
				require.Len(t, got.Images, 1)
				require.Equal(t, tt.wantMime, got.Images[0].MimeType)
				require.NotEmpty(t, got.Images[0].Base64Data)
			}
			if tt.wantError != "" {
				require.Equal(t, tt.wantError, got.ErrorCode)
			}
		})
	}

	_, err := ExtractBatchImagePartsFromResultLine([]byte(`{"response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + batchImageDownloadTestBase64 + `"}}]}}]}}`))
	require.Error(t, err)
	require.NotContains(t, err.Error(), batchImageDownloadTestBase64)
}

func TestBatchImageDownloadFilenames(t *testing.T) {
	require.Equal(t, "___secret_name.png", BatchImageSafeDownloadFilename("../../secret\nname", "png"))
	require.Equal(t, `attachment; filename="cover_001.png"`, BatchImageContentDispositionAttachment(`cover"001.png`))
}

func newTestBatchImageDownloadService() (*BatchImageDownloadService, *fakeBatchImageRepository, *fakeBatchImageDownloadLimiter) {
	repo := newFakeBatchImageRepository()
	apiKeyID := int64(22)
	accountID := int64(101)
	repo.jobs["imgbatch_download"] = &BatchImageJob{
		BatchID:           "imgbatch_download",
		UserID:            11,
		APIKeyID:          &apiKeyID,
		AccountID:         &accountID,
		Provider:          BatchImageProviderGeminiAPI,
		Model:             "gemini-2.5-flash-image",
		Status:            BatchImageJobStatusCompleted,
		ProviderJobName:   batchImageStringPtr("providers/internal/job"),
		ProviderOutputRef: batchImageStringPtr("gs://bucket/internal/output.jsonl"),
		ItemCount:         3,
		SuccessCount:      2,
		FailCount:         1,
		CreatedAt:         time.Now(),
	}
	mime := "image/png"
	ext := "png"
	webp := "image/webp"
	webpExt := "webp"
	code := "SAFETY_BLOCKED"
	msg := "blocked in gs://bucket/internal/output.jsonl"
	repo.items["imgbatch_download"] = []CreateBatchImageItemParams{
		{JobID: "imgbatch_download", CustomID: "cover/../001", Status: BatchImageItemStatusSuccess, MimeType: &mime, FileExtension: &ext, ImageCount: 2},
		{JobID: "imgbatch_download", CustomID: "bad", Status: BatchImageItemStatusFailed, ErrorCode: &code, ErrorMessage: &msg},
		{JobID: "imgbatch_download", CustomID: "ok_2", Status: BatchImageItemStatusSuccess, MimeType: &webp, FileExtension: &webpExt, ImageCount: 1},
	}
	provider := &publicBatchImageProvider{name: BatchImageProviderGeminiAPI, result: batchImageDownloadResultJSONL()}
	limiter := &fakeBatchImageDownloadLimiter{}
	svc := &BatchImageDownloadService{
		Repo:             repo,
		ProviderRegistry: NewBatchImageProviderRegistry(provider),
		AccountResolver:  &fakeBatchImageAccountResolver{account: &Account{ID: accountID, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}},
		Limiter:          limiter,
		Config:           &config.Config{BatchImage: config.BatchImageConfig{MaxDownloadItemsZip: 10, MaxDownloadDurationSeconds: 60}},
	}
	return svc, repo, limiter
}

func newTestBatchImageUpscaledDownloadService() (*BatchImageDownloadService, *fakeBatchImageRepository, *fakeBatchImageDownloadLimiter, *batchImageUpscaleObjectStoreTest) {
	svc, repo, limiter := newTestBatchImageDownloadService()
	repo.jobs["imgbatch_download"].ImageSize = "2K"
	svc.Config.BatchImage.DeliveryCOSPrefix = "batch-image/delivery"
	store := newBatchImageUpscaleObjectStoreTest()
	svc.DeliveryStore = store
	return svc, repo, limiter, store
}

const batchImageDownloadTestBase64 = "Zmlyc3Q="

func batchImageDownloadResultJSONL() string {
	return strings.Join([]string{
		`{"key":"cover/../001","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"Zmlyc3Q="}},{"inlineData":{"mimeType":"image/jpeg","data":"c2Vjb25k"}}]}}]}}`,
		`{"key":"bad","error":{"code":"SAFETY","message":"blocked"}}`,
		`{"key":"ok_2","candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/webp","data":"dGhpcmQ="}}]}}]}`,
	}, "\n") + "\n"
}

func readZipFiles(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	out := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		rc, err := file.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		out[file.Name] = body
	}
	return out
}

func mapValues(in map[string][]byte) [][]byte {
	out := make([][]byte, 0, len(in))
	for _, value := range in {
		out = append(out, value)
	}
	return out
}

type fakeBatchImageDownloadLimiter struct {
	acquireCount int
	releaseCount int
	deny         bool
}

func (l *fakeBatchImageDownloadLimiter) Acquire(context.Context, string, string) (BatchImageDownloadPermit, error) {
	l.acquireCount++
	if l.deny {
		return nil, ErrBatchImageDownloadLimited
	}
	return &fakeBatchImageDownloadPermit{release: func() { l.releaseCount++ }}, nil
}

type fakeBatchImageDownloadPermit struct {
	once    bool
	release func()
}

func (p *fakeBatchImageDownloadPermit) Release(context.Context) error {
	if p.once {
		return nil
	}
	p.once = true
	if p.release != nil {
		p.release()
	}
	return nil
}

type batchImageUpscaleObjectStoreTestObject struct {
	data        []byte
	contentType string
}

// batchImageUpscaleObjectStoreTest intentionally supports only exact-object operations.
type batchImageUpscaleObjectStoreTest struct {
	objects          map[string]batchImageUpscaleObjectStoreTestObject
	opened           []string
	deletes          [][]string
	deleteErr        error
	deleteCalls      int
	blockOpen        bool
	readerCloseCount int
}

func newBatchImageUpscaleObjectStoreTest() *batchImageUpscaleObjectStoreTest {
	return &batchImageUpscaleObjectStoreTest{objects: make(map[string]batchImageUpscaleObjectStoreTestObject)}
}

func (s *batchImageUpscaleObjectStoreTest) setObject(key, contentType string, data []byte) {
	s.objects[key] = batchImageUpscaleObjectStoreTestObject{data: append([]byte(nil), data...), contentType: contentType}
}

func (s *batchImageUpscaleObjectStoreTest) Put(_ context.Context, key, contentType string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return io.ErrShortBuffer
	}
	s.setObject(key, contentType, data)
	return nil
}

func (s *batchImageUpscaleObjectStoreTest) Open(ctx context.Context, key string) (io.ReadCloser, int64, string, error) {
	s.opened = append(s.opened, key)
	if s.blockOpen {
		<-ctx.Done()
		return nil, 0, "", ctx.Err()
	}
	object, ok := s.objects[key]
	if !ok {
		return nil, 0, "", io.EOF
	}
	reader := &batchImageUpscaleObjectStoreTestReader{
		Reader: bytes.NewReader(append([]byte(nil), object.data...)),
		onClose: func() {
			s.readerCloseCount++
		},
	}
	return reader, int64(len(object.data)), object.contentType, nil
}

func (s *batchImageUpscaleObjectStoreTest) PresignPut(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (s *batchImageUpscaleObjectStoreTest) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}

func (s *batchImageUpscaleObjectStoreTest) Head(_ context.Context, key string) (int64, string, error) {
	object, ok := s.objects[key]
	if !ok {
		return 0, "", io.EOF
	}
	return int64(len(object.data)), object.contentType, nil
}

func (s *batchImageUpscaleObjectStoreTest) Delete(_ context.Context, keys []string) error {
	deleted := append([]string(nil), keys...)
	s.deletes = append(s.deletes, deleted)
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	for _, key := range keys {
		delete(s.objects, key)
	}
	return nil
}

type batchImageUpscaleObjectStoreTestReader struct {
	*bytes.Reader
	onClose func()
	closed  bool
}

func (r *batchImageUpscaleObjectStoreTestReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}

var _ BatchImageDownloadLimiter = (*fakeBatchImageDownloadLimiter)(nil)
var _ BatchImageDownloadPermit = (*fakeBatchImageDownloadPermit)(nil)
var _ BatchImageDeliveryObjectStore = (*batchImageUpscaleObjectStoreTest)(nil)
var _ BatchImageUpscaleObjectStore = (*batchImageUpscaleObjectStoreTest)(nil)
