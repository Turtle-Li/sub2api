package service

import (
	"context"
	"encoding/base64"
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

func directImagesTestAccount() *Account {
	return &Account{ID: 35, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
}

func forwardCodexDirectImagesForTest(
	t *testing.T,
	svc *OpenAIGatewayService,
	c *gin.Context,
	parsed *OpenAIImagesRequest,
) (*OpenAIForwardResult, error) {
	t.Helper()
	return svc.forwardOpenAIImagesOAuthDirect(
		context.Background(),
		c,
		directImagesTestAccount(),
		parsed,
		"",
		parsed.Model,
		parsed.Model,
		time.Now(),
		context.Background(),
		"test-token",
	)
}

func TestCodexDirectImagesRouting(t *testing.T) {
	for _, model := range []string{"gpt-image-1.5", "gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2.5-sunburst-2026-09-08"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"  原样保留 prompt  ","quality":"max","size":"auto","response_format":"url","extra":{"preserve":true}}`, model))
			c, rec := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result, err := svc.ForwardImages(ctx, c, directImagesTestAccount(), body, parsed, "")
			require.NoError(t, err)
			require.Equal(t, 1, result.ImageCount)
			require.Equal(t, 20, result.Usage.ImageOutputTokens)
			require.Equal(t, model, result.UpstreamModel)
			require.Equal(t, "/backend-api/codex/images/generations", result.UpstreamEndpoint)
			require.Equal(t, "https://chatgpt.com/backend-api/codex/images/generations", upstream.lastReq.URL.String())
			require.NoError(t, upstream.lastReq.Context().Err())
			require.Equal(t, "Bearer test-token", upstream.lastReq.Header.Get("Authorization"))
			require.Equal(t, "application/json", upstream.lastReq.Header.Get("Accept"))
			require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
			require.NotEmpty(t, upstream.lastReq.Header.Get("Originator"))
			require.NotEmpty(t, upstream.lastReq.Header.Get("User-Agent"))
			require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, "  原样保留 prompt  ", gjson.GetBytes(upstream.lastBody, "prompt").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "extra").Exists())
			for _, key := range []string{"tools", "reasoning", "instructions", "response_format", "stream"} {
				require.False(t, gjson.GetBytes(upstream.lastBody, key).Exists(), key)
			}
			require.Equal(t, "data:image/png;base64,aGVsbG8=", gjson.GetBytes(rec.Body.Bytes(), "data.0.url").String())
		})
	}
}

func TestCodexDirectImagesSingleRequestTrimsUpstreamOverdelivery(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw one portrait","n":1}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	response := `{"data":[{"b64_json":"aW1hZ2UtMQ=="},{"b64_json":"aW1hZ2UtMg=="}],"usage":{"input_tokens":10,"output_tokens":40}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response)),
	}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 1, result.BillableImageCount)
	require.Equal(t, 1, result.ImageOverdeliveryCount)
	require.Equal(t, 1, result.ImageOverdeliveryEvents)
	require.Equal(t, OpenAIUsage{InputTokens: 10, OutputTokens: 40, ImageOutputTokens: 40}, result.Usage)
	require.NotNil(t, result.BillingUsage)
	require.Equal(t, OpenAIUsage{InputTokens: 10, OutputTokens: 20, ImageOutputTokens: 20}, *result.BillingUsage)
	require.Len(t, gjson.GetBytes(rec.Body.Bytes(), "data").Array(), 1)
	require.Equal(t, "aW1hZ2UtMQ==", gjson.GetBytes(rec.Body.Bytes(), "data.0.b64_json").String())
	require.NotContains(t, rec.Body.String(), "aW1hZ2UtMg==")
}

func TestCodexDirectImagesMappingBeforeRouting(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-1","prompt":"draw"}`)
			c, _ := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			account := directImagesTestAccount()
			account.Type = accountType
			account.Credentials["model_mapping"] = map[string]any{"gpt-image-2": "gpt-image-2.5-flare"}
			result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "gpt-image-2")
			require.NoError(t, err)
			require.Equal(t, "gpt-image-2.5-flare", result.UpstreamModel)
			require.Equal(t, "gpt-image-2", result.Model)
			require.Equal(t, "gpt-image-2.5-flare", gjson.GetBytes(upstream.lastBody, "model").String())
		})
	}
	for _, model := range []string{"gpt-image-1", "gpt-image-future"} {
		body, target, err := buildOpenAIImagesOAuthPayload(&OpenAIImagesRequest{Prompt: "draw"}, model)
		require.NoError(t, err)
		require.Equal(t, chatgptCodexURL, target)
		require.Equal(t, "gpt-5.6-luna", gjson.GetBytes(body, "model").String())
		require.Equal(t, model, gjson.GetBytes(body, "tools.0.model").String())
	}
}

func TestCodexDirectImagesHTTPErrorFallbacksOnlyWhenEndpointUnavailable(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500, 502, 503, 404, 405} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				if calls == 1 {
					require.Equal(t, "/backend-api/codex/images/generations", req.URL.Path)
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"server_error","message":"image request rejected"}}`))}, nil
				}
				require.Contains(t, req.URL.Path, "/backend-api/codex/responses")
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\"}]}}\n\n"))}, nil
			}}
			body := []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"draw"}`)
			c, _ := newOpenAIImagesTestContext(t, body)
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
			if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 2, calls)
			} else {
				require.Error(t, err)
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestCodexDirectImagesStreamRejectsPlainJSON(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw","stream":true}`)
	c, _ := newOpenAIImagesTestContext(t, body)
	svc := newOpenAIImagesTestService(&httpUpstreamRecorder{resp: openAIImagesJSONResponse()})
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
	require.Error(t, err, "未收到 SSE 完成事件，不能把未转发的 JSON 当作成功")
	require.Nil(t, result)
}

func TestCodexDirectImagesMultipleOutputs(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt-image-2.5-flare","prompt":"draw","n":2,"stream":%t}`, stream))
			c, _ := newOpenAIImagesTestContext(t, body)
			response := `{"data":[{"b64_json":"AA=="},{"b64_json":"AQ=="}],"usage":{"input_tokens":10,"output_tokens":40}}`
			if stream {
				response = "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"AA==\"}\n\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"AQ==\",\"usage\":{\"input_tokens\":10,\"output_tokens\":40}}\n\n"
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := forwardCodexDirectImagesForTest(t, svc, c, parsed)
			require.NoError(t, err)
			require.Equal(t, 2, result.ImageCount)
			require.Equal(t, 40, result.Usage.ImageOutputTokens)
		})
	}
}

func TestCodexDirectImagesMultipleOutputsUpscaleByActualDimensions(t *testing.T) {
	source := testUpscalePNG(t, 1024, 1536)
	resultImage := testUpscalePNG(t, 2048, 3072)
	var upscaleCalls atomic.Int32
	mini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/upscale":
			upscaleCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job":{"id":"job-direct-multi"}}`))
		case "/v1/jobs/job-direct-multi":
			_, _ = w.Write([]byte(`{"job":{"state":"completed"}}`))
		case "/v1/jobs/job-direct-multi/result":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(resultImage)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mini.Close()

	encoded := base64.StdEncoding.EncodeToString(source)
	response := fmt.Sprintf(`{"data":[{"b64_json":%q},{"b64_json":%q}],"usage":{"input_tokens":10,"output_tokens":40}}`, encoded, encoded)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}}
	svc := newOpenAIImagesTestService(upstream)
	svc.imageUpscaler = &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: mini.URL, APIKeyVaultRef: "vault://test/key#api_key", VaultAgentSocket: imageUpscaleVaultSocket,
			RequestTimeoutSeconds: 2, JobTimeoutSeconds: 30, PollIntervalMillis: 1, MaxConcurrent: 1, MaxQueue: 4, MaxResultBytes: 1024 * 1024,
		},
		httpClient: mini.Client(),
		loadAPIKey: func(context.Context) ([]byte, error) { return []byte("test-token"), nil },
		slots:      make(chan struct{}, 1),
	}
	body := []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"draw two models","n":2,"size":"2K"}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	forwarded, err := forwardCodexDirectImagesForTest(t, svc, c, parsed)
	require.NoError(t, err)
	require.Equal(t, int32(2), upscaleCalls.Load())
	require.Equal(t, 2, forwarded.ImageCount)
	require.Equal(t, []string{"2048x3072", "2048x3072"}, forwarded.ImageOutputSizes)
	require.Equal(t, "2K", gjson.GetBytes(upstream.lastBody, "size").String(), "OpenAI request remains provider-native; response dimensions decide Mini use")
	for i := 0; i < 2; i++ {
		decoded, decodeErr := base64.StdEncoding.DecodeString(gjson.GetBytes(rec.Body.Bytes(), fmt.Sprintf("data.%d.b64_json", i)).String())
		require.NoError(t, decodeErr)
		width, height, _, decodeErr := decodeUpscaleImageConfig(decoded)
		require.NoError(t, decodeErr)
		require.Equal(t, 2048, width)
		require.Equal(t, 3072, height)
	}
}

func TestCodexDirectImagesMultipleOutputsRemainAtomicWhenSecondUpscaleFails(t *testing.T) {
	source := testUpscalePNG(t, 2, 3)
	resultImage := testUpscalePNG(t, 4, 6)
	var upscaleCalls atomic.Int32
	mini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/upscale":
			call := upscaleCalls.Add(1)
			if call == 2 {
				http.Error(w, "upstream overloaded", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job":{"id":"job-direct-atomic"}}`))
		case "/v1/jobs/job-direct-atomic":
			_, _ = w.Write([]byte(`{"job":{"state":"completed"}}`))
		case "/v1/jobs/job-direct-atomic/result":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(resultImage)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mini.Close()

	encoded := base64.StdEncoding.EncodeToString(source)
	response := fmt.Sprintf(`{"data":[{"b64_json":%q},{"b64_json":%q}],"usage":{"input_tokens":10,"output_tokens":40}}`, encoded, encoded)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}}
	svc := newOpenAIImagesTestService(upstream)
	svc.imageUpscaler = &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: mini.URL, APIKeyVaultRef: "vault://test/key#api_key", VaultAgentSocket: imageUpscaleVaultSocket,
			RequestTimeoutSeconds: 2, JobTimeoutSeconds: 30, PollIntervalMillis: 1, MaxConcurrent: 1, MaxQueue: 4, MaxResultBytes: 1024 * 1024,
		},
		httpClient: mini.Client(),
		loadAPIKey: func(context.Context) ([]byte, error) { return []byte("test-token"), nil },
		slots:      make(chan struct{}, 1),
	}
	body := []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"draw two models","n":2,"size":"2K"}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	forwarded, err := forwardCodexDirectImagesForTest(t, svc, c, parsed)
	require.Error(t, err)
	require.Nil(t, forwarded)
	require.Equal(t, int32(2), upscaleCalls.Load())
	require.Zero(t, rec.Body.Len(), "no partial image response may be written before every output succeeds")
}

func TestCodexDirectImagesHighResolutionRejectsMissingOutputBeforeUpscale(t *testing.T) {
	source := base64.StdEncoding.EncodeToString(testUpscalePNG(t, 2, 3))
	var upscaleCalls atomic.Int32
	mini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upscaleCalls.Add(1)
		http.Error(w, "unexpected upscale", http.StatusInternalServerError)
	}))
	defer mini.Close()

	response := fmt.Sprintf(`{"data":[{}, {"b64_json":%q}],"usage":{"input_tokens":10,"output_tokens":40}}`, source)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}}
	svc := newOpenAIImagesTestService(upstream)
	svc.imageUpscaler = &ImageUpscaleService{
		cfg: config.ImageUpscaleConfig{
			Enabled: true, BaseURL: mini.URL, APIKeyVaultRef: "vault://test/key#api_key", VaultAgentSocket: imageUpscaleVaultSocket,
			RequestTimeoutSeconds: 2, JobTimeoutSeconds: 30, PollIntervalMillis: 1, MaxConcurrent: 1, MaxQueue: 4, MaxResultBytes: 1024 * 1024,
		},
		httpClient: mini.Client(),
		loadAPIKey: func(context.Context) ([]byte, error) { return []byte("test-token"), nil },
		slots:      make(chan struct{}, 1),
	}
	body := []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"draw two models","n":2,"size":"2K"}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	forwarded, err := forwardCodexDirectImagesForTest(t, svc, c, parsed)
	require.Error(t, err)
	require.Nil(t, forwarded)
	require.Zero(t, upscaleCalls.Load(), "cardinality validation must run before Mini")
	require.Zero(t, rec.Body.Len())
}

func TestCodexDirectImagesStreaming(t *testing.T) {
	for _, test := range []struct {
		name, events          string
		wantCount             int
		wantError, disconnect bool
	}{
		{"completed", "complete", 1, false, false},
		{"duplicate", "complete,complete", 1, false, false},
		{"disconnect", "partial,complete", 1, false, true},
		{"truncated", "partial", 0, true, false},
		{"partial_requested_multiple", "complete", 1, false, false},
		{"error", "error", 0, true, false},
		{"partial_success", "complete,error", 1, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2","prompt":"edit","images":[{"image_url":"data:image/png;base64,AA=="}],"stream":true,"response_format":"url"}`)
			if test.name == "partial_requested_multiple" {
				body = []byte(`{"model":"gpt-image-2","prompt":"edit","n":2,"images":[{"image_url":"data:image/png;base64,AA=="}],"stream":true,"response_format":"url"}`)
			}
			c, rec := newOpenAIImagesTestContext(t, body)
			c.Request.URL.Path = "/v1/images/edits"
			if test.disconnect {
				c.Writer = &failingOpenAIImageWriter{ResponseWriter: c.Writer, failAfter: 1}
			}
			frames := map[string]string{
				"partial":  `{"type":"image_generation.partial_image","b64_json":"AA==","partial_image_index":0}`,
				"complete": `{"type":"image_generation.completed","model":"gpt-image-2-codex","size":"1024x1024","b64_json":"aGVsbG8=","output_format":"webp","usage":{"input_tokens":10,"output_tokens":20}}`,
				"error":    `{"type":"error","error":{"type":"server_error","message":"overloaded"}}`,
			}
			var stream strings.Builder
			for _, event := range strings.Split(test.events, ",") {
				fmt.Fprintf(&stream, "data: %s\n\n", frames[event])
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String()))}}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if test.wantCount > 0 {
				require.NotNil(t, result)
				require.Equal(t, test.wantCount, result.ImageCount)
				require.Equal(t, 20, result.Usage.ImageOutputTokens)
				if !test.wantError {
					require.Equal(t, "gpt-image-2-codex", result.UpstreamResponseModel)
				}
			}
			require.Equal(t, "/backend-api/codex/images/edits", upstream.lastReq.URL.Path)
			require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
			if !test.disconnect && test.wantCount > 0 {
				require.Contains(t, rec.Body.String(), "event: image_edit.completed")
				require.Contains(t, rec.Body.String(), "data:image/webp;base64,aGVsbG8=")
			}
		})
	}
}

func TestCodexDirectImagesEmptyResponseFails(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"b64_json":""}]}`, `{}`, `{"error":{"message":"overloaded","type":"server_error"}}`, `not json`} {
		_, err := parseCodexDirectImagesResponse([]byte(body))
		require.Error(t, err)
	}
}

func TestCodexDirectImagesAccountTestAndWhitelist(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"draw"}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
	svc := &AccountTestService{httpUpstream: upstream}
	require.NoError(t, svc.testOpenAIImageOAuth(c, context.Background(), directImagesTestAccount(), "gpt-image-2.5-sunburst", "draw"))
	require.Equal(t, "/backend-api/codex/images/generations", upstream.lastReq.URL.Path)
	require.Contains(t, rec.Body.String(), `"success":true`)
	newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"gpt-5.6-luna"}]}`)
	svc.openaiGatewayService = &OpenAIGatewayService{}
	account := newCodexModelsTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"gpt-image-2.5-flare": "gpt-image-2.5-flare"}
	models, err := svc.FetchOpenAIAccountModels(context.Background(), account)
	require.NoError(t, err)
	for _, model := range models {
		require.NotEqual(t, "gpt-image-2.5-sunburst", model.ID)
	}
}
