//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/shanghaidelivery"
	"github.com/stretchr/testify/require"
)

const shanghaiTestMiniID = "aaee866b-1475-4980-aafb-5fc6351d3e18"

type shanghaiTestTransport func(*http.Request) (*http.Response, error)

func (f shanghaiTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type shanghaiTestStore struct {
	*batchImageUpscaleTestStore
	signedHost string
}

func (s *shanghaiTestStore) sign(key string) string {
	return "https://" + s.signedHost + "/" + key + "?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Expires=60&X-Amz-Signature=fake"
}

func (s *shanghaiTestStore) PresignPut(_ context.Context, key string, _ time.Duration) (string, error) {
	return s.sign(key), nil
}

func (s *shanghaiTestStore) PresignHead(_ context.Context, key string, _ time.Duration) (string, error) {
	return s.sign(key), nil
}

type shanghaiTestAcks struct {
	job                 *BatchImageJob
	customID            string
	confirmFails, marks int
}

func (s *shanghaiTestAcks) ListBatchImageShanghaiAcknowledgements(context.Context, int) ([]BatchImageShanghaiAcknowledgement, error) {
	if s.job.Status == BatchImageJobStatusCompleted && batchImageUsesShanghai(s.job) {
		return []BatchImageShanghaiAcknowledgement{{BatchID: s.job.BatchID, CustomID: s.customID}}, nil
	}
	return nil, nil
}

func (s *shanghaiTestAcks) ConfirmBatchImageShanghaiAcknowledgement(_ context.Context, batchID string) error {
	if s.confirmFails > 0 {
		s.confirmFails--
		return errors.New("DB commit failed")
	}
	if batchID != s.job.BatchID {
		return errors.New("wrong job identity")
	}
	s.marks++
	s.job.ProviderOutputRef = batchImageStringPtr(BatchImageShanghaiAckedRef)
	return nil
}

type shanghaiTestFixture struct {
	t                                                 *testing.T
	cfg                                               *config.Config
	upscaler                                          *ImageUpscaleService
	spool                                             *shanghaidelivery.Server
	store                                             *shanghaiTestStore
	repo                                              *checkpointIndexerRepo
	job                                               *BatchImageJob
	bitmap                                            []byte
	key, digest, miniAnswer                           string
	puts, uploads, downloads, cloudCalls, stages      int
	loseCOSReply, rejectMini, failDownload, failCloud bool
	loseMiniReply                                     bool
}

func newShanghaiTestFixture(t *testing.T) *shanghaiTestFixture {
	f := &shanghaiTestFixture{t: t, bitmap: batchImageUpscaleTestPNG(64, 96), miniAnswer: `{"state":"completed","result_uploaded":true,"result_ready":true}`}
	f.cfg, f.upscaler = newBatchImageUpscaleTestService("https://mini.example", "studio/prod")
	f.cfg.BatchImage.ShanghaiDeliveryURL = "http://100.64.0.3:18486"
	f.cfg.BatchImage.ShanghaiDeliveryCanaryUserID = 101
	f.cfg.BatchImage.OutputRetentionAfterTerminalHours = 360
	f.upscaler.cfg.RetryMax = 2
	f.upscaler.httpClient = &http.Client{Transport: shanghaiTestTransport(f.mini)}
	f.upscaler.cloudSlots = make(chan struct{}, 1)
	f.upscaler.cloudUpscale = func(_ context.Context, _ []byte, _ int, beforePaid func() error) (*ImageUpscaleResult, error) {
		if err := beforePaid(); err != nil {
			return nil, err
		}
		f.cloudCalls++
		if f.failCloud {
			return nil, errors.New("response lost after accepted paid request")
		}
		return &ImageUpscaleResult{Data: bytes.Clone(f.bitmap), MimeType: "image/png", Width: 64, Height: 96}, nil
	}
	f.store = &shanghaiTestStore{batchImageUpscaleTestStore: newBatchImageUpscaleTestStore(), signedHost: "cos.example"}
	f.spool = &shanghaidelivery.Server{
		Root: t.TempDir(), Controller: netip.MustParseAddr("100.64.0.2"), COSHost: f.store.signedHost,
		ObjectPrefix: f.cfg.BatchImage.DeliveryCOSPrefix, IngressURL: "https://studio.test.ts.net:18488",
		HTTP: &http.Client{Transport: shanghaiTestTransport(f.cos)},
	}
	previousHTTP := batchImageShanghaiHTTP
	batchImageShanghaiHTTP = &http.Client{Transport: shanghaiTestTransport(func(r *http.Request) (*http.Response, error) {
		r.RemoteAddr = "100.64.0.2:5000"
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/data") {
			f.stages++
		}
		w := httptest.NewRecorder()
		f.spool.Control().ServeHTTP(w, r)
		return w.Result(), nil
	})}
	t.Cleanup(func() { batchImageShanghaiHTTP = previousHTTP })
	wallet, group, account, hold := ImageStudioWalletOwnerSlot, int64(45), int64(249), 0.25
	f.job = &BatchImageJob{BatchID: "imgbatch_shanghai_qa", UserID: 101, APIKeyID: &wallet, StudioGroupID: &group,
		Provider: BatchImageProviderOpenAI, Model: "gpt-image-2.6-sunburst", ImageSize: ImageBillingSize2K, ItemCount: 1,
		Status: BatchImageJobStatusIndexing, ProviderOutputRef: batchImageStringPtr(BatchImageShanghaiSourceRef), HoldAmount: &hold, AccountID: &account}
	f.repo = &checkpointIndexerRepo{newFakeBatchImageRepository(), newBatchImageCheckpointMemory()}
	f.repo.jobs[f.job.BatchID] = f.job
	f.repo.items[f.job.BatchID] = []CreateBatchImageItemParams{{JobID: f.job.BatchID, CustomID: "image-1", Status: BatchImageItemStatusPending}}
	_, err := f.repo.SaveBatchImageCheckpoint(t.Context(), f.job.BatchID, "image-1", BatchImageCheckpointSource, BatchImageResultCheckpoint{
		Data: batchImageUpscaleTestResultLine("image-1", batchImageUpscaleTestPNG(32, 48)), MimeType: "application/x-ndjson",
	})
	require.NoError(t, err)
	f.key, err = batchImageUpscaleObjectKey(f.cfg, f.job.BatchID, "image-1", 0, "png")
	require.NoError(t, err)
	return f
}

func (f *shanghaiTestFixture) mini(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	switch r.URL.Path {
	case "/health":
		_, _ = io.WriteString(w, `{"direct_upload_recovery":true,"comfy_up":true}`)
	case "/v1/upscale":
		f.uploads++
		if f.loseMiniReply {
			return nil, errors.New("Mini accepted submission but its reply did not arrive")
		}
		if f.rejectMini {
			w.WriteHeader(http.StatusTooManyRequests)
			break
		}
		require.NoError(f.t, r.ParseMultipartForm(1<<20))
		require.Equal(f.t, "2", r.FormValue("scale"))
		if strings.Contains(f.miniAnswer, `"result_uploaded":true`) {
			incoming := httptest.NewRequest(http.MethodPut, r.FormValue("result_upload_url"), bytes.NewReader(f.bitmap))
			staged := httptest.NewRecorder()
			f.spool.Ingress().ServeHTTP(staged, incoming)
			require.Equal(f.t, http.StatusOK, staged.Code)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"job":{"id":%q}}`, shanghaiTestMiniID)
	case "/v1/jobs/" + shanghaiTestMiniID:
		_, _ = fmt.Fprintf(w, `{"job":%s}`, f.miniAnswer)
	case "/v1/jobs/" + shanghaiTestMiniID + "/result":
		f.downloads++
		if f.failDownload {
			w.WriteHeader(http.StatusConflict)
			break
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(f.bitmap)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	return w.Result(), nil
}

func (f *shanghaiTestFixture) cos(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	require.Equal(f.t, "/"+f.key, r.URL.Path)
	switch r.Method {
	case http.MethodHead:
		if f.digest == "" {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(f.bitmap)))
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("X-Cos-Meta-Sub2-Sha256", f.digest)
		}
	case http.MethodPut:
		f.puts++
		payload, err := io.ReadAll(r.Body)
		require.NoError(f.t, err)
		require.Equal(f.t, f.bitmap, payload)
		f.digest = r.Header.Get("X-Cos-Meta-Sub2-Sha256")
		if f.loseCOSReply {
			f.loseCOSReply = false
			return nil, errors.New("COS committed but the acknowledgement was lost")
		}
	}
	return w.Result(), nil
}

func (f *shanghaiTestFixture) index() (*BatchImageIndexResult, error) {
	indexer := &BatchImageResultIndexer{Repo: f.repo, Config: f.cfg, Upscaler: f.upscaler, UpscaleStore: f.store}
	return indexer.Index(f.t.Context(), f.job, nil, nil)
}

func TestShanghaiCanaryUsesWalletSlotAndBypassesMergedArchive(t *testing.T) {
	f := newShanghaiTestFixture(t)
	job := *f.job
	job.ProviderJobName = batchImageStringPtr(batchImageOpenAIProviderJobPrefix + job.BatchID)
	job.ProviderOutputRef = nil
	provider := NewOpenAIImagesBatchProvider(newFakeOpenAIImageBatchStore(), nil, testOpenAIImageBatchObjectPrefix)
	provider.config, provider.checkpoints = f.cfg, f.repo
	status, err := provider.Advance(t.Context(), &job, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.NoError(t, err)
	require.Equal(t, BatchImageShanghaiSourceRef, status.ProviderOutputRef)
	apiKey := int64(3)
	for _, different := range []func(*BatchImageJob){
		func(j *BatchImageJob) { j.APIKeyID = nil }, func(j *BatchImageJob) { j.APIKeyID = &apiKey },
		func(j *BatchImageJob) { j.UserID++ }, func(j *BatchImageJob) { j.ItemCount++ },
		func(j *BatchImageJob) { j.ImageSize = ImageBillingSize1K }, func(j *BatchImageJob) { j.Model = "gpt-image-2.5-flare" },
	} {
		outside := job
		different(&outside)
		require.False(t, batchImageShanghaiCanary(f.cfg, &outside))
	}
}

func TestShanghaiReconcilesCOSAndAcknowledgesAfterWalletSettlement(t *testing.T) {
	f := newShanghaiTestFixture(t)
	f.loseCOSReply = true
	_, err := f.index()
	require.ErrorContains(t, err, "SHANGHAI_COS_RECEIPT_UNCONFIRMED")
	require.Zero(t, f.repo.replaceCalls)
	// Lose the original source and rebuild the processes. Published bytes
	// already staged on the Shanghai disk must be sufficient to finish.
	old := f.spool
	f.spool = &shanghaidelivery.Server{Root: old.Root, Controller: old.Controller, IngressURL: old.IngressURL,
		COSHost: old.COSHost, ObjectPrefix: old.ObjectPrefix, HTTP: old.HTTP}
	f.repo.batchImageCheckpointMemory = newBatchImageCheckpointMemory()
	result, err := f.index()
	require.NoError(t, err)
	require.Equal(t, 1, result.SuccessCount)
	require.Equal(t, 1, f.uploads)
	require.Equal(t, 1, f.puts)
	require.Zero(t, f.downloads+f.cloudCalls+f.stages)
	require.Empty(t, f.store.puts, "AWS must not upload any originals")
	acks := &shanghaiTestAcks{job: f.job, customID: "image-1", confirmFails: 1}
	slot, err := batchImageShanghaiSlotFor(f.cfg, f.job.BatchID, "image-1")
	require.NoError(t, err)
	reconcileBatchImageShanghaiAcknowledgements(t.Context(), f.cfg, acks, nil)
	state, err := slot.status(t.Context())
	require.NoError(t, err)
	require.True(t, state.Published)
	require.NoError(t, f.repo.TransitionBatchImageJobStatus(t.Context(), f.job.BatchID, BatchImageJobStatusSettling, BatchImageTransitionOptions{}))
	billing := &fakeBatchImageBillingRepo{}
	settlement := &BatchImageSettlementService{Repo: f.repo, BillingRepo: billing, Pricing: &fakeBatchImagePricingResolver{unitPrice: 0.25}, Config: f.cfg}
	_, err = settlement.Settle(t.Context(), f.job.BatchID)
	require.NoError(t, err)
	_, err = settlement.Settle(t.Context(), f.job.BatchID)
	require.NoError(t, err)
	require.Len(t, billing.captures, 1)
	require.Equal(t, 360*time.Hour, f.job.OutputExpiresAt.Sub(*f.job.SettledAt))
	reconcileBatchImageShanghaiAcknowledgements(t.Context(), f.cfg, acks, nil)
	require.Zero(t, acks.marks)
	state, err = slot.status(t.Context())
	require.NoError(t, err)
	require.Nil(t, state.Image)
	reconcileBatchImageShanghaiAcknowledgements(t.Context(), f.cfg, acks, nil)
	require.Equal(t, 1, acks.marks)
}

func TestShanghaiMiniRecoveryAndCloudAdmission(t *testing.T) {
	t.Run("same Mini result after straight upload fails", func(t *testing.T) {
		f := newShanghaiTestFixture(t)
		f.miniAnswer = `{"state":"failed","error":"direct_upload_failed","result_ready":true}`
		f.failDownload = true
		_, err := f.index()
		require.ErrorContains(t, err, "SHANGHAI_MINI_RESULT_RECOVERY_FAILED")
		require.Zero(t, f.cloudCalls)
		f.failDownload = false
		_, err = f.index()
		require.NoError(t, err)
		require.Equal(t, 1, f.uploads)
		require.Equal(t, 2, f.downloads)
		require.Equal(t, 1, f.stages)
		require.Equal(t, 1, f.puts)
		require.Zero(t, f.cloudCalls)
	})
	t.Run("Mini rate limit allows exactly one cloud fallback", func(t *testing.T) {
		f := newShanghaiTestFixture(t)
		f.rejectMini = true
		f.loseCOSReply = true
		_, err := f.index()
		require.ErrorContains(t, err, "SHANGHAI_COS_RECEIPT_UNCONFIRMED")
		_, err = f.index()
		require.NoError(t, err)
		require.Equal(t, 1, f.uploads)
		require.Equal(t, 1, f.cloudCalls)
		require.Equal(t, 1, f.stages)
		require.Equal(t, 1, f.puts)
	})
	t.Run("unknown cloud receipt never authorizes a replay", func(t *testing.T) {
		f := newShanghaiTestFixture(t)
		f.rejectMini, f.failCloud = true, true
		_, err := f.index()
		require.Error(t, err)
		_, err = f.index()
		require.ErrorContains(t, err, "SHANGHAI_ALIYUN_RESULT_UNCONFIRMED")
		require.Equal(t, 1, f.uploads)
		require.Equal(t, 1, f.cloudCalls)
		require.Zero(t, f.puts)
	})
	t.Run("unknown Mini receipt does not trigger new work", func(t *testing.T) {
		f := newShanghaiTestFixture(t)
		f.loseMiniReply = true
		for range 2 {
			_, err := f.index()
			require.ErrorContains(t, err, "SHANGHAI_MINI_SUBMIT_UNCONFIRMED")
		}
		require.Equal(t, 1, f.uploads)
		require.Zero(t, f.cloudCalls+f.puts)
	})
}
