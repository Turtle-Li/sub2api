//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func workBuddyTestAccount(base, model string) *Account {
	a := museTestAccount(base)
	a.Credentials["model_mapping"] = map[string]any{model: model}
	a.Extra = map[string]any{workBuddyImageReadyKey: true}
	return a
}

func TestWorkBuddySequentialSingleAttempt(t *testing.T) {
	for _, readinessKey := range []string{workBuddyImageReadyKey, workBuddyImageNativeRetryAcceptedKey} {
		t.Run(readinessKey, func(t *testing.T) {
			for _, mode := range []string{"generate", "edit", "failure", "private-url", "lost-response"} {
				t.Run(mode, func(t *testing.T) {
					model := "cn:hunyuan-image-alpha"
					refs := []BatchImageReference{}
					if mode == "edit" {
						model = "cn:hunyuan-image-alpha-edit"
						refs = append(refs, BatchImageReference{MimeType: "image/png", Data: []byte("fixture")})
					}
					posts := 0
					encoded := museTestPNG(t)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						posts++
						require.Equal(t, "POST", r.Method)
						require.Equal(t, "Bearer test-only", r.Header.Get("Authorization"))
						var body map[string]any
						require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						require.Equal(t, model, body["model"])
						require.Equal(t, float64(1), body["n"])
						require.NotContains(t, body, "size")
						if mode == "edit" {
							require.Equal(t, "/v1/images/edits", r.URL.Path)
							require.Len(t, body["image"], 1)
						} else {
							require.Equal(t, "/v1/images/generations", r.URL.Path)
						}
						if mode == "lost-response" {
							conn, _, err := w.(http.Hijacker).Hijack()
							require.NoError(t, err)
							require.NoError(t, conn.Close())
							return
						}
						if mode == "failure" {
							w.WriteHeader(500)
							return
						}
						if mode == "private-url" {
							_, _ = w.Write([]byte(`{"data":[{"url":"http://127.0.0.1:1/image.png"}]}`))
							return
						}
						require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": encoded}}}))
					}))
					defer server.Close()
					store := newFakeOpenAIImageBatchStore()
					gateway := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(&museSlotCache{}), httpUpstream: &museTestUpstream{}}
					p := newWorkBuddyImagesProvider(store, testOpenAIImageBatchObjectPrefix, gateway)
					a := workBuddyTestAccount(server.URL, model)
					a.Extra = map[string]any{readinessKey: true}
					job := &BatchImageJob{BatchID: "imgbatch_workbuddy", Provider: BatchImageProviderWorkBuddy, Model: model, ItemCount: 1}
					submitted, err := p.Submit(t.Context(), job, a, BatchImageInput{BatchID: job.BatchID, Model: model, Items: []BatchImageInputItem{{CustomID: "one", Prompt: "image", ReferenceImages: refs}}})
					require.NoError(t, err)
					job.ProviderJobName = &submitted.ProviderJobName
					job.ProviderInputRef = &submitted.ProviderInputRef
					var status *BatchProviderStatus
					for i := 0; i < 3; i++ {
						p = newWorkBuddyImagesProvider(store, testOpenAIImageBatchObjectPrefix, gateway)
						status, err = p.Advance(t.Context(), job, a)
						require.NoError(t, err)
					}
					require.True(t, status.Done)
					job.ProviderOutputRef = &status.ProviderOutputRef
					result, _, err := p.OpenResult(t.Context(), job, a)
					require.NoError(t, err)
					defer result.Close()
					output, err := io.ReadAll(result)
					require.NoError(t, err)
					if mode == "generate" || mode == "edit" {
						require.Contains(t, string(output), encoded)
						require.NotContains(t, string(output), `"error"`)
					} else {
						require.Contains(t, string(output), "PROVIDER_ITEM_FAILED")
					}
					require.Equal(t, 1, posts)
				})
			}
		})
	}
}

func TestWorkBuddyBeforeAttemptAdmission(t *testing.T) {
	cache := &museSlotCache{}
	upstream := &museTestUpstream{}
	e := &workBuddyImageExecutor{gateway: &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache), httpUpstream: upstream}}
	a := workBuddyTestAccount("http://unused", "cn:hunyuan-image-alpha")
	a.Concurrency = 1
	request := OpenAIImageBatchItemRequest{Model: "cn:hunyuan-image-alpha", Prompt: "image"}
	hooks := 0
	before := func() error { hooks++; return nil }
	cache.active.Store(1)
	_, err := e.Execute(t.Context(), a, request, before)
	require.ErrorIs(t, err, errBatchImageOpenAIAccountBusy)
	cache.active.Store(0)
	proxyID := int64(77)
	a.ProxyID = &proxyID
	_, err = e.Execute(t.Context(), a, request, before)
	require.Error(t, err)
	require.Zero(t, hooks)
	require.Zero(t, upstream.calls.Load())
}

func TestWorkBuddyAdmission(t *testing.T) {
	a := workBuddyTestAccount("http://unused", "cn:hunyuan-image-alpha")
	require.True(t, batchImageAccountSupportsProviderModel(BatchImageProviderWorkBuddy, a, "cn:hunyuan-image-alpha"))
	delete(a.Extra, workBuddyImageReadyKey)
	require.False(t, batchImageAccountSupportsProviderModel(BatchImageProviderWorkBuddy, a, "cn:hunyuan-image-alpha"))
	require.False(t, isWorkBuddyImageModel("global:gpt-image-2.5-sunburst"))
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	for _, model := range []string{"cn:hunyuan-image-alpha", "cn:hunyuan-image-alpha-edit", "global:hy-image-3.5"} {
		r := BatchImageSubmitRequest{Model: model, ImageSize: "1K", Items: []BatchImageSubmitItem{{Prompt: "image"}}}
		if workBuddyImageReferenceCount(model) == 1 {
			_, err := svc.validateSubmitRequest(r)
			require.Error(t, err)
			r.Items[0].ReferenceImages = []BatchImageReferenceInput{{MimeType: "image/png", Data: []byte("image")}}
		}
		_, err := svc.validateSubmitRequest(r)
		require.NoError(t, err)
		r.Provider = BatchImageProviderMuse
		_, err = svc.validateSubmitRequest(r)
		require.Error(t, err)
		r.Provider = BatchImageProviderWorkBuddy
		svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0}
		_, err = svc.resolvePricingSnapshot(context.Background(), BatchImageOwner{UserID: 11}, r, BatchImageProviderWorkBuddy, a)
		require.Error(t, err)
		svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.25}
		_, err = svc.resolvePricingSnapshot(context.Background(), BatchImageOwner{UserID: 11}, r, BatchImageProviderWorkBuddy, a)
		require.NoError(t, err)
	}
}

func TestWorkBuddyAccountProxy(t *testing.T) {
	var direct, proxied atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct.Add(1) }))
	defer target.Close()
	encoded := museTestPNG(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		require.True(t, r.URL.IsAbs())
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": encoded}}}))
	}))
	defer proxy.Close()
	parsed, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)
	a := workBuddyTestAccount(target.URL, "global:hy-image-3.5")
	id := int64(7)
	a.ProxyID = &id
	a.Proxy = &Proxy{ID: id, Status: StatusActive, Protocol: "http", Host: parsed.Hostname(), Port: port}
	cache := &museSlotCache{}
	e := &workBuddyImageExecutor{gateway: &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache), httpUpstream: &museTestUpstream{}}}
	result, err := e.Execute(t.Context(), a, OpenAIImageBatchItemRequest{Model: "global:hy-image-3.5", Prompt: "image"}, nil)
	require.NoError(t, err)
	require.Equal(t, encoded, result.Base64Data)
	require.Equal(t, int64(1), proxied.Load())
	require.Zero(t, direct.Load())
	require.Zero(t, cache.active.Load())
}
