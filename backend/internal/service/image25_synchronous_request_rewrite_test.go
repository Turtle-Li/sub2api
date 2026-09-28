package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func image25RewriteTestResponse(source string) string {
	return `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + source + `"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`
}

func image25RewriteTestOAuthSSE(source string) string {
	return `data: {"response":` + image25RewriteTestResponse(source) + "}\n\ndata: [DONE]\n\n"
}

func TestGeminiForwardNative_Image25APIKeyRewritesProviderSizePreservesBillingSize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upscaler, _, source := newSynchronousImageUpscaleTestService(t)
	upstream := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(image25RewriteTestResponse(source))),
		},
	}
	svc := &GeminiMessagesCompatService{
		httpUpstream:  upstream,
		cfg:           &config.Config{},
		imageUpscaler: upscaler,
	}
	account := &Account{
		ID:          201,
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "gemini-api-key"},
	}
	body := []byte(`{"generationConfig":{"imageConfig":{"imageSize":"2K","aspectRatio":"16:9"}}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash-image:generateContent", bytes.NewReader(body))

	result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash-image", "generateContent", false, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "2K", result.ImageInputSize)

	require.NotNil(t, upstream.lastReq)
	posted, err := io.ReadAll(upstream.lastReq.Body)
	require.NoError(t, err)
	require.Equal(t, "1K", gjson.GetBytes(posted, "generationConfig.imageConfig.imageSize").String())
	require.Equal(t, "16:9", gjson.GetBytes(posted, "generationConfig.imageConfig.aspectRatio").String())
	require.Equal(t, "image/png", gjson.GetBytes(recorder.Body.Bytes(), "candidates.0.content.parts.0.inlineData.mimeType").String())
}

func TestGeminiForwardNative_Image25OAuthCodeAssistRewritesInnerRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upscaler, _, source := newSynchronousImageUpscaleTestService(t)
	upstream := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(image25RewriteTestOAuthSSE(source))),
		},
	}
	svc := &GeminiMessagesCompatService{
		httpUpstream:  upstream,
		tokenProvider: &GeminiTokenProvider{},
		cfg:           &config.Config{},
		imageUpscaler: upscaler,
	}
	account := &Account{
		ID:          202,
		Platform:    PlatformGemini,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "oauth-token",
			"project_id":   "project-202",
		},
	}
	body := []byte(`{"generationConfig":{"imageConfig":{"imageSize":"2K","aspectRatio":"9:16"}}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash-image:generateContent", bytes.NewReader(body))

	result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash-image", "generateContent", false, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "2K", result.ImageInputSize)

	require.NotNil(t, upstream.lastReq)
	posted, err := io.ReadAll(upstream.lastReq.Body)
	require.NoError(t, err)
	var wrapped map[string]any
	require.NoError(t, json.Unmarshal(posted, &wrapped))
	inner, ok := wrapped["request"].(map[string]any)
	require.True(t, ok)
	innerJSON := mustMarshalJSON(t, inner)
	require.Equal(t, "1K", gjson.GetBytes(innerJSON, "generationConfig.imageConfig.imageSize").String())
	require.Equal(t, "9:16", gjson.GetBytes(innerJSON, "generationConfig.imageConfig.aspectRatio").String())
	require.Contains(t, upstream.lastReq.URL.String(), "streamGenerateContent?alt=sse")
}

func TestAntigravityForwardGemini_Image25RewritesWrappedRequestPreservesBillingSize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upscaler, _, source := newSynchronousImageUpscaleTestService(t)
	upstream := &queuedHTTPUpstreamStub{
		responses: []*http.Response{{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(image25RewriteTestOAuthSSE(source))),
		}},
	}
	svc := &AntigravityGatewayService{
		settingService: &SettingService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}},
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream:   upstream,
		imageUpscaler:  upscaler,
	}
	account := &Account{
		ID:          203,
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "antigravity-token",
			"project_id":   "project-203",
		},
	}
	body := []byte(`{"generationConfig":{"imageConfig":{"imageSize":"2K","aspectRatio":"3:2"}}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash-image:generateContent", bytes.NewReader(body))

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-2.5-flash-image", "generateContent", false, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "2K", result.ImageInputSize)

	require.Len(t, upstream.requestBodies, 1)
	var wrapped map[string]any
	require.NoError(t, json.Unmarshal(upstream.requestBodies[0], &wrapped))
	inner, ok := wrapped["request"].(map[string]any)
	require.True(t, ok)
	innerJSON := mustMarshalJSON(t, inner)
	require.Equal(t, "1K", gjson.GetBytes(innerJSON, "generationConfig.imageConfig.imageSize").String())
	require.Equal(t, "3:2", gjson.GetBytes(innerJSON, "generationConfig.imageConfig.aspectRatio").String())
	require.Equal(t, "project-203", wrapped["project"])
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return b
}
