//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func museTestAccount(base string) *Account {
	return &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": base, "api_key": "test-only", "model_mapping": map[string]any{"muse-image": "muse-image"}}}
}
func museTestPNG(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func TestMuseBatchResume(t *testing.T) {
	for _, scenario := range []string{"completed", "completed-webp", "failed", "unknown-submit"} {
		t.Run(scenario, func(t *testing.T) {
			terminal := scenario
			if scenario == "completed-webp" {
				terminal = "completed"
			}
			ctx := context.Background()
			store := newFakeOpenAIImageBatchStore()
			posts, gets := 0, 0
			encoded := museTestPNG(t)
			mime := "image/png"
			if scenario == "completed-webp" {
				encoded = base64.StdEncoding.EncodeToString(museTestWebP(t))
				mime = "image/webp"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer test-only", r.Header.Get("Authorization"))
				if r.Method == http.MethodPost {
					posts++
					require.Equal(t, "/v1/images/tasks", r.URL.Path)
					require.Len(t, r.Header.Get("Idempotency-Key"), 64)
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					require.Equal(t, "b64_json", payload["response_format"])
					require.Equal(t, float64(1), payload["n"])
					if terminal == "unknown-submit" {
						w.WriteHeader(502)
						return
					}
					w.WriteHeader(202)
					_, _ = w.Write([]byte(`{"task_id":"task-42","status":"queued"}`))
					return
				}
				gets++
				require.Equal(t, "/v1/images/tasks/task-42", r.URL.Path)
				status := "queued"
				if gets > 1 {
					status = terminal
				}
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "task-42", "kind": "image", "status": status, "created": 1794000000, "data": []any{map[string]any{"b64_json": encoded, "revised_prompt": "image", "size": "auto", "kind": "image"}}}))
			}))
			defer server.Close()
			a := museTestAccount(server.URL + "/v1")
			p := newTestMuseProvider(store, testOpenAIImageBatchObjectPrefix)
			job := &BatchImageJob{BatchID: "imgbatch_muse", Provider: BatchImageProviderMuse, Model: "muse-image", ItemCount: 1}
			submitted, err := p.Submit(ctx, job, a, BatchImageInput{BatchID: job.BatchID, Model: job.Model, Items: []BatchImageInputItem{{CustomID: "one", Prompt: "image"}}})
			require.NoError(t, err)
			job.ProviderJobName = &submitted.ProviderJobName
			job.ProviderInputRef = &submitted.ProviderInputRef
			status, err := p.Advance(ctx, job, a)
			require.NoError(t, err)
			require.False(t, status.Done)
			// New provider instance shares only durable objects, simulating worker restart.
			p = newTestMuseProvider(store, testOpenAIImageBatchObjectPrefix)
			for i := 0; i < 4; i++ {
				status, err = p.Advance(ctx, job, a)
				require.NoError(t, err)
				if status.Done {
					break
				}
			}
			require.True(t, status.Done)
			require.Equal(t, 1, posts)
			job.ProviderOutputRef = &status.ProviderOutputRef
			result, _, err := p.OpenResult(ctx, job, a)
			require.NoError(t, err)
			defer result.Close()
			var output bytes.Buffer
			_, err = output.ReadFrom(result)
			require.NoError(t, err)
			if terminal == "completed" {
				require.Contains(t, output.String(), encoded)
				require.Contains(t, output.String(), `"mimeType":"`+mime+`"`)
				require.NotContains(t, output.String(), `"error"`)
				require.Equal(t, 2, gets)
			} else {
				require.Contains(t, output.String(), "PROVIDER_ITEM_FAILED")
			}
			exists, err := store.Exists(ctx, p.itemAttemptKey(job.BatchID, 0)+".upstream")
			require.NoError(t, err)
			require.False(t, exists)
		})
	}
}
func TestMuseOutputValidation(t *testing.T) {
	good, err := decodeMuseImage(museTestPNG(t))
	require.NoError(t, err)
	require.Equal(t, "image/png", good.MimeType)
	for _, encoded := range []string{"!!!", base64.StdEncoding.EncodeToString([]byte("<html>bad</html>")), strings.Repeat("A", base64.StdEncoding.EncodedLen(museImageMaxBytes)+4)} {
		_, err := decodeMuseImage(encoded)
		require.Error(t, err)
	}
}
func TestMuseAdmission(t *testing.T) {
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	valid := BatchImageSubmitRequest{Model: "muse-image", ResponseMimeType: "image/png", ImageSize: "1K", Items: []BatchImageSubmitItem{{Prompt: "image"}}}
	_, err := svc.validateSubmitRequest(valid)
	require.NoError(t, err)
	for _, edit := range []func(*BatchImageSubmitRequest){
		func(r *BatchImageSubmitRequest) { r.Provider = BatchImageProviderOpenAI },
		func(r *BatchImageSubmitRequest) { r.Model = "muse-image-alias" },
		func(r *BatchImageSubmitRequest) { r.AspectRatio = "1:1" },
		func(r *BatchImageSubmitRequest) { r.ResponseMimeType = "image/jpeg" },
		func(r *BatchImageSubmitRequest) { r.ImageSize = "2K" },
		func(r *BatchImageSubmitRequest) { r.Items = []BatchImageSubmitItem{{Prompt: "image", OutputCount: 2}} },
		func(r *BatchImageSubmitRequest) {
			r.Items = []BatchImageSubmitItem{{Prompt: "image", ReferenceImages: make([]BatchImageReferenceInput, 2)}}
		},
	} {
		req := valid
		edit(&req)
		_, err := svc.validateSubmitRequest(req)
		require.Error(t, err)
	}
	a := museTestAccount("http://localhost")
	require.True(t, batchImageAccountSupportsProviderModel(BatchImageProviderMuse, a, "muse-image"))
	a.Type = AccountTypeOAuth
	require.False(t, batchImageAccountSupportsProviderModel(BatchImageProviderMuse, a, "muse-image"))
	a.Type = AccountTypeAPIKey
	a.Credentials["model_mapping"] = map[string]any{"muse-image": "gpt-image-2"}
	require.False(t, batchImageAccountSupportsProviderModel(BatchImageProviderMuse, a, "muse-image"))
	require.Equal(t, []string{BatchImageProviderMuse}, batchImageProviderSelectionOrder("", "muse-image"))
}

func TestMuseMissingReceiptDoesNotSubmit(t *testing.T) {
	store := newFakeOpenAIImageBatchStore()
	e := &museImageBatchExecutor{store: store}
	_, err := e.Resume(context.Background(), museTestAccount("http://unused"), OpenAIImageBatchItemRequest{StateKey: "missing"})
	require.ErrorContains(t, err, "outcome unknown")
}
func TestMusePricingAndSettlement(t *testing.T) {
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	req := BatchImageSubmitRequest{Model: "muse-image", ImageSize: "1K", Items: []BatchImageSubmitItem{{Prompt: "image"}}}
	svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0}
	_, err := svc.resolvePricingSnapshot(context.Background(), BatchImageOwner{UserID: 11}, req, BatchImageProviderMuse, museTestAccount("http://unused"))
	require.Error(t, err)
	svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.25}
	snapshot, err := svc.resolvePricingSnapshot(context.Background(), BatchImageOwner{UserID: 11}, req, BatchImageProviderMuse, museTestAccount("http://unused"))
	require.NoError(t, err)
	require.Equal(t, 1.0, snapshot.BatchDiscountMultiplier)
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_muse_settle")
	job.Provider = BatchImageProviderMuse
	job.Model = "muse-image"
	job.SuccessCount = 1
	job.FailCount = 1
	job.ItemCount = 2
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	settle := &BatchImageSettlementService{Repo: repo, BillingRepo: billing, Pricing: &fakeBatchImagePricingResolver{unitPrice: 0.25}}
	result, err := settle.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.Equal(t, 0.25, result.ActualCost)
	result, err = settle.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.True(t, result.AlreadySettled)
	require.Len(t, billing.captures, 1)
}
func TestMuseRejectsRedirect(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	p := newTestMuseProvider(newFakeOpenAIImageBatchStore(), testOpenAIImageBatchObjectPrefix)
	_, err := p.executor.(*museImageBatchExecutor).call(context.Background(), museTestAccount(redirect.URL), http.MethodPost, "", nil, "test", nil)
	require.Error(t, err)
	require.False(t, leaked)
}

func museTestWebP(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/muse/native-output.webp")
	require.NoError(t, err)
	return data
}
func TestMuseWebPValidation(t *testing.T) {
	data := museTestWebP(t)
	result, err := decodeMuseImage(base64.StdEncoding.EncodeToString(data))
	require.NoError(t, err)
	require.Equal(t, "image/webp", result.MimeType)
	require.Equal(t, base64.StdEncoding.EncodeToString(data), result.Base64Data)

	t.Run("corrupt payload with intact dimensions", func(t *testing.T) {
		corrupt := data[:len(data)-4]
		_, format, err := image.DecodeConfig(bytes.NewReader(corrupt))
		require.NoError(t, err)
		require.Equal(t, "webp", format)
		_, err = decodeMuseImage(base64.StdEncoding.EncodeToString(corrupt))
		require.ErrorContains(t, err, "corrupt image")
	})
	t.Run("oversized decoded bytes", func(t *testing.T) {
		oversized := make([]byte, museImageMaxBytes+1)
		copy(oversized, data)
		encoded := base64.StdEncoding.EncodeToString(oversized)
		require.Equal(t, base64.StdEncoding.EncodedLen(museImageMaxBytes), len(encoded))
		_, err := decodeMuseImage(encoded)
		require.ErrorContains(t, err, "invalid image bytes")
	})
	t.Run("oversized pixel dimensions", func(t *testing.T) {
		oversized := append([]byte(nil), data...)
		require.Equal(t, "VP8L", string(oversized[12:16]))
		require.Equal(t, byte(0x2f), oversized[20])
		binary.LittleEndian.PutUint32(oversized[21:25], 16383|16383<<14)
		cfg, format, err := image.DecodeConfig(bytes.NewReader(oversized))
		require.NoError(t, err)
		require.Equal(t, "webp", format)
		require.Equal(t, 16384, cfg.Width)
		require.Equal(t, 16384, cfg.Height)
		_, err = decodeMuseImage(base64.StdEncoding.EncodeToString(oversized))
		require.ErrorContains(t, err, "unsupported image content")
	})
}
