//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestImage26AliyunFailoverDelivery(t *testing.T) {
	source := batchImageUpscaleTestPNG(1024, 512)
	final := batchImageUpscaleTestPNG(2048, 1024)
	for _, tc := range []struct {
		name     string
		status   int
		poll     string
		holdSlot bool
	}{
		{"mini_ok", 202, "completed", false}, {"mini_429", 429, "", false}, {"mini_503", 503, "", false}, {"accepted_then_failed", 202, "failed", false}, {"accepted_then_timed_out", 202, "running", false}, {"mini_admission_full", 0, "", true}, {"mini_connection_refused", -1, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submits, cloud atomic.Int32
			mini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/upscale":
					submits.Add(1)
					imageUpscaleTestWriteJSON(w, tc.status, `{"job":{"id":"mini"}}`)
				case "/v1/jobs/mini":
					fmt.Fprintf(w, `{"job":{"state":%q}}`, tc.poll)
				case "/v1/jobs/mini/result":
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(final)
				default:
					t.Errorf("unexpected Mini path: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(mini.Close)
			if tc.status == -1 {
				mini.Close()
			}
			cfg, s := newBatchImageUpscaleTestService(mini.URL, "batch-image/delivery")
			s.cfg.RetryMax = 1
			s.cfg.MaxQueue = 0
			s.cloudSlots = make(chan struct{}, 1)
			s.cloudUpscale = func(ctx context.Context, b []byte, scale int) (*ImageUpscaleResult, error) {
				cloud.Add(1)
				require.NoError(t, ctx.Err())
				require.Equal(t, 2, scale)
				require.Equal(t, source, b)
				return &ImageUpscaleResult{Data: append([]byte(nil), final...), Width: 2048, Height: 1024, MimeType: "image/png", Scale: 2}, nil
			}
			if tc.holdSlot {
				require.NoError(t, s.acquire(t.Context()))
				defer s.release()
			}
			timed := func() (context.Context, context.CancelFunc) {
				if tc.poll == "running" {
					return context.WithTimeout(t.Context(), 1500*time.Millisecond)
				}
				return context.WithCancel(t.Context())
			}
			ctx, cancel := timed()
			defer cancel()
			job := &BatchImageJob{BatchID: "imgbatch_failover", Model: "gpt-image-2.6-sunburst", Provider: BatchImageProviderOpenAI, ImageSize: "2K"}
			repo := newFakeBatchImageRepository()
			store := newBatchImageUpscaleTestStore()
			provider := &publicBatchImageProvider{name: BatchImageProviderOpenAI, result: string(batchImageUpscaleTestResultLine("one", source))}
			index, err := (&BatchImageResultIndexer{Repo: repo, Config: cfg, Upscaler: s, UpscaleStore: store}).Index(ctx, job, provider, &Account{})
			require.NoError(t, err)
			require.Equal(t, 1, index.SuccessCount)
			require.Zero(t, index.FailCount)
			require.Len(t, store.objects, 1)
			require.Len(t, store.puts, 1)
			require.Equal(t, final, store.objects[store.puts[0]].data)
			require.Equal(t, 1, repo.items[job.BatchID][0].ImageCount)
			ctx2, cancel2 := timed()
			defer cancel2()
			body := fmt.Appendf(nil, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(source))
			out, err := (&OpenAIGatewayService{imageUpscaler: s}).upscaleOpenAIImagesResponse(ctx2, nil, &OpenAIImagesRequest{Model: job.Model, Size: "2K", N: 1}, body)
			require.NoError(t, err)
			require.Equal(t, base64.StdEncoding.EncodeToString(final), gjson.GetBytes(out, "data.0.b64_json").String())
			if tc.poll == "completed" {
				require.Zero(t, cloud.Load())
			} else {
				require.EqualValues(t, 2, cloud.Load())
			}
			if tc.status > 0 {
				require.EqualValues(t, 2, submits.Load(), "never replay generation or a Mini POST when failover is armed")
			} else {
				require.Zero(t, submits.Load())
			}
		})
	}
}

func TestImageUpscaleCloudQueueBoundAndCancellation(t *testing.T) {
	_, s := newBatchImageUpscaleTestService("https://unused.invalid", "batch-image/delivery")
	s.cfg.MaxQueue = 0
	require.NoError(t, s.acquire(t.Context()))
	defer s.release()
	s.cloudSlots = make(chan struct{}, 1)
	s.cloudSlots <- struct{}{}
	s.cloudUpscale = func(context.Context, []byte, int) (*ImageUpscaleResult, error) {
		t.Fatal("saturated fallback must not submit")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, imageUpscaleCloudMaxQueue)
	for range imageUpscaleCloudMaxQueue {
		go func() { finished <- s.acquireCloud(ctx, nil) }()
	}
	require.Eventually(t, func() bool { return s.cloudWaiting.Load() == imageUpscaleCloudMaxQueue }, time.Second, time.Millisecond)
	task := WithAsyncImageTaskStorage(t.Context(), "queue-bound")
	_, err := s.upscaleBase64FromLoader(task, 2, func(context.Context) (string, error) { t.Fatal("must not load overflow source"); return "", nil })
	requireImageUpscaleError(t, err, "ALIYUN_BACKPRESSURE")
	require.True(t, AsyncImageTaskReplayPrevented(task))
	cancel()
	for range imageUpscaleCloudMaxQueue {
		require.ErrorIs(t, <-finished, context.Canceled)
	}
	require.Zero(t, s.cloudWaiting.Load())
	<-s.cloudSlots
	require.NoError(t, s.acquireCloud(t.Context(), nil))
	<-s.cloudSlots
}
