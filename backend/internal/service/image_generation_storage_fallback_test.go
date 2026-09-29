package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type failNthImageStorage struct {
	calls  int
	failAt int
	saved  []savedImage
}

func (s *failNthImageStorage) Save(_ context.Context, key, contentType string, data []byte) (string, error) {
	s.calls++
	if s.failAt > 0 && s.calls == s.failAt {
		return "", errors.New("temporary object storage outage")
	}
	s.saved = append(s.saved, savedImage{key: key, contentType: contentType, data: append([]byte(nil), data...)})
	return "https://cdn.test/" + key, nil
}

func newSynchronousImageUpscaleTestService(t *testing.T) (*ImageUpscaleService, []byte, string) {
	t.Helper()
	source := imageUpscaleTestPNG(t, 2, 3)
	result := imageUpscaleTestPNG(t, 4, 6)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/upscale":
			imageUpscaleTestWriteJSON(w, http.StatusAccepted, `{"job":{"id":"job-storage-fallback"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-storage-fallback":
			imageUpscaleTestWriteJSON(w, http.StatusOK, `{"job":{"state":"completed"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-storage-fallback/result":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(result)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return newImageUpscaleTestService(imageUpscaleTestConfig(server.URL), server.Client(), imageUpscaleTestStaticLoader), result, base64.StdEncoding.EncodeToString(source)
}

func TestHandleOpenAIImagesHighResStorageFailureFallsBackToInline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upscaler, result, source := newSynchronousImageUpscaleTestService(t)
	storage := &failNthImageStorage{failAt: 2}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		imageUpscaler: upscaler,
		imageStorageResolver: func() (*ImageResultUploader, bool) {
			return uploader, true
		},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"data":[{"b64_json":"` + source + `"},{"b64_json":"` + source + `"}]}`,
		)),
	}

	_, count, _, err := svc.handleOpenAIImagesNonStreamingResponse(
		context.Background(), response, c, &Account{},
		&OpenAIImagesRequest{N: 2, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"},
		"gemini-2.5-flash-image", true,
	)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Equal(t, 2, storage.calls)
	require.Len(t, storage.saved, 1, "the first upload may remain orphaned after a later upload fails")
	for i := 0; i < 2; i++ {
		prefix := fmt.Sprintf("data.%d", i)
		require.False(t, gjson.GetBytes(recorder.Body.Bytes(), prefix+".url").Exists())
		decoded, decodeErr := base64.StdEncoding.DecodeString(gjson.GetBytes(recorder.Body.Bytes(), prefix+".b64_json").String())
		require.NoError(t, decodeErr)
		require.Equal(t, result, decoded)
	}
}

func TestHandleOpenAIImagesHighResStorageSuccessReturnsURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upscaler, _, source := newSynchronousImageUpscaleTestService(t)
	storage := &failNthImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		imageUpscaler: upscaler,
		imageStorageResolver: func() (*ImageResultUploader, bool) {
			return uploader, true
		},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"` + source + `"},{"b64_json":"` + source + `"}]}`)),
	}

	_, _, _, err := svc.handleOpenAIImagesNonStreamingResponse(
		context.Background(), response, c, &Account{},
		&OpenAIImagesRequest{N: 2, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"},
		"gemini-2.5-flash-image", true,
	)
	require.NoError(t, err)
	require.Len(t, storage.saved, 2)
	for i := 0; i < 2; i++ {
		prefix := fmt.Sprintf("data.%d", i)
		require.NotEmpty(t, gjson.GetBytes(recorder.Body.Bytes(), prefix+".url").String())
		require.False(t, gjson.GetBytes(recorder.Body.Bytes(), prefix+".b64_json").Exists())
	}
}

func TestHandleOpenAIImagesAsyncHighResStoresOnceUnderTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upscaler, _, source := newSynchronousImageUpscaleTestService(t)
	storage := &failNthImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		imageUpscaler: upscaler,
		imageStorageResolver: func() (*ImageResultUploader, bool) {
			return uploader, true
		},
	}
	store := &imageTaskMemoryStore{}
	tasks := NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)
	created, err := tasks.Create(context.Background(), ImageTaskOwner{UserID: 7, APIKeyID: 9})
	require.NoError(t, err)

	ctx := WithAsyncImageTaskStorage(context.Background(), created.ID)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"data":[{"b64_json":"` + source + `"},{"b64_json":"` + source + `"}]}`,
		)),
	}

	_, count, _, err := svc.handleOpenAIImagesNonStreamingResponse(
		ctx, response, c, &Account{},
		&OpenAIImagesRequest{N: 2, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"},
		"gemini-2.5-flash-image", true,
	)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.True(t, AsyncImageTaskStorageComplete(ctx))
	require.Len(t, storage.saved, 2)
	require.Equal(t, "images/"+created.ID+"-0.png", storage.saved[0].key)
	require.Equal(t, "images/"+created.ID+"-1.png", storage.saved[1].key)

	require.NoError(t, tasks.CompleteStored(context.Background(), created.ID, http.StatusOK, recorder.Body.Bytes()))
	require.Len(t, storage.saved, 2, "task completion must not upload an already-stored result again")
	completed, err := tasks.Get(context.Background(), ImageTaskOwner{UserID: 7, APIKeyID: 9}, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, completed.Status)
	require.NotContains(t, string(completed.Result), "b64_json")
}

func TestHandleOpenAIImagesAsyncHighResStorageFailureIsTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upscaler, _, source := newSynchronousImageUpscaleTestService(t)
	storage := &failNthImageStorage{failAt: 2}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		imageUpscaler: upscaler,
		imageStorageResolver: func() (*ImageResultUploader, bool) {
			return uploader, true
		},
	}
	ctx := WithAsyncImageTaskStorage(context.Background(), "imgtask_storage_failure")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"data":[{"b64_json":"` + source + `"},{"b64_json":"` + source + `"}]}`,
		)),
	}

	_, _, _, err := svc.handleOpenAIImagesNonStreamingResponse(
		ctx, response, c, &Account{},
		&OpenAIImagesRequest{N: 2, Size: "2K", SizeTier: "2K", ResponseFormat: "b64_json"},
		"gemini-2.5-flash-image", true,
	)
	upscaleErr := requireImageUpscaleError(t, err, "STORAGE_FAILED")
	require.Equal(t, http.StatusBadGateway, upscaleErr.StatusCode)
	require.False(t, AsyncImageTaskStorageComplete(ctx))
	require.Zero(t, recorder.Body.Len(), "a required async storage failure must not expose a success response")
}

func TestProcessGeminiImageGenerationResponseStorageFailureUsesOnlyInlineData(t *testing.T) {
	upscaler, result, source := newSynchronousImageUpscaleTestService(t)
	storage := &failNthImageStorage{failAt: 2}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	body := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + source + `"}},{"inline_data":{"mime_type":"image/png","data":"` + source + `"}}]}}]}`)

	out, err := processGeminiImageGenerationResponse(context.Background(), upscaler, func() (*ImageResultUploader, bool) {
		return uploader, true
	}, body, 2, "gemini_native")
	require.NoError(t, err)
	require.Equal(t, 2, storage.calls)
	require.Len(t, storage.saved, 1)
	require.False(t, gjson.GetBytes(out, "candidates.0.content.parts.0.fileData").Exists())
	require.False(t, gjson.GetBytes(out, "candidates.0.content.parts.1.file_data").Exists())
	for _, path := range []string{"candidates.0.content.parts.0.inlineData.data", "candidates.0.content.parts.1.inline_data.data"} {
		decoded, decodeErr := base64.StdEncoding.DecodeString(gjson.GetBytes(out, path).String())
		require.NoError(t, decodeErr)
		require.Equal(t, result, decoded)
	}
}

func TestProcessGeminiImageGenerationResponseStorageSuccessUsesOnlyFileData(t *testing.T) {
	upscaler, _, source := newSynchronousImageUpscaleTestService(t)
	storage := &failNthImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	body := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + source + `"}},{"inline_data":{"mime_type":"image/png","data":"` + source + `"}}]}}]}`)

	out, err := processGeminiImageGenerationResponse(context.Background(), upscaler, func() (*ImageResultUploader, bool) {
		return uploader, true
	}, body, 2, "antigravity")
	require.NoError(t, err)
	require.Len(t, storage.saved, 2)
	require.False(t, gjson.GetBytes(out, "candidates.0.content.parts.0.inlineData").Exists())
	require.False(t, gjson.GetBytes(out, "candidates.0.content.parts.1.inline_data").Exists())
	require.NotEmpty(t, gjson.GetBytes(out, "candidates.0.content.parts.0.fileData.fileUri").String())
	require.NotEmpty(t, gjson.GetBytes(out, "candidates.0.content.parts.1.file_data.file_uri").String())
}

func TestProcessGeminiImageGenerationResponseUsesOneLifecycleDeadlineForAllImages(t *testing.T) {
	result := imageUpscaleTestPNG(t, 4, 6)
	var resultCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/upscale":
			imageUpscaleTestWriteJSON(w, http.StatusAccepted, `{"job":{"id":"job-gemini-shared-deadline"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-gemini-shared-deadline":
			imageUpscaleTestWriteJSON(w, http.StatusOK, `{"job":{"state":"completed"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-gemini-shared-deadline/result":
			resultCalls.Add(1)
			time.Sleep(600 * time.Millisecond)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(result)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	cfg := imageUpscaleTestConfig(server.URL)
	cfg.JobTimeoutSeconds = 1
	upscaler := newImageUpscaleTestService(cfg, server.Client(), imageUpscaleTestStaticLoader)
	source := base64.StdEncoding.EncodeToString(imageUpscaleTestPNG(t, 2, 3))
	body := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + source + `"}},{"inlineData":{"mimeType":"image/png","data":"` + source + `"}}]}}]}`)
	started := time.Now()

	_, err := processGeminiImageGenerationResponse(context.Background(), upscaler, nil, body, 2, "gemini_native")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 1500*time.Millisecond)
	require.Equal(t, int32(2), resultCalls.Load())
}
