//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestParseMuseVideoReferenceImage(t *testing.T) {
	var jpegImage bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegImage, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil))
	for _, imageURL := range []string{
		"data:image/png;base64," + museTestPNG(t),
		"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegImage.Bytes()),
		"data:image/webp;base64," + base64.StdEncoding.EncodeToString(museTestWebP(t)),
	} {
		body, err := json.Marshal(map[string]any{"model": MuseVideoModel, "prompt": "Animate the object", "reference_image": imageURL})
		require.NoError(t, err)
		req, err := ParseMuseMediaRequest(MuseVideoCreate, "application/json", body)
		require.NoError(t, err)
		images, err := req.PrepareReferences(t.Context())
		require.NoError(t, err)
		nativeBody, err := json.Marshal(req)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"muse-video","prompt":"Animate the object","duration":6,"images":["`+imageURL+`"]}`, string(nativeBody))
		info := req.Info()
		info.InputImageURLs = images
		require.Equal(t, VideoBillingResolution720P, info.Resolution)
		moderation := ExtractContentModerationInput(ContentModerationProtocolOpenAIImages, info.ModerationBody())
		require.Equal(t, "Animate the object", moderation.Text)
		require.Equal(t, []string{imageURL}, moderation.Images)
	}
}

func TestParseMuseVideoTextRequestOmitsReferenceImage(t *testing.T) {
	req, err := ParseMuseMediaRequest(MuseVideoCreate, "application/json", []byte(`{"model":"muse-video","prompt":"waves","size":"9:16"}`))
	require.NoError(t, err)
	images, err := req.PrepareReferences(t.Context())
	require.NoError(t, err)
	require.Empty(t, images)
	nativeBody, err := json.Marshal(req)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"muse-video","prompt":"waves","duration":6,"size":"9:16"}`, string(nativeBody))
}

func TestParseMuseVideoRejectsInvalidReferenceImages(t *testing.T) {
	for _, test := range []struct {
		name, field string
		value       any
		message     string
	}{
		{"metadata URL", "reference_image", "http://169.254.169.254/latest/meta-data", "public address"},
		{"local file", "reference_image", "/etc/passwd", "local paths are not supported"},
		{"invalid field type", "reference_image", []string{"first", "second"}, "invalid Muse request fields"},
		{"unsupported field", "last_frame", "second", "invalid Muse request fields"},
		{"unsupported format", "reference_image", "data:image/svg+xml;base64,PHN2Zz4=", "unsupported Muse reference format"},
		{"invalid base64", "reference_image", "data:image/png;base64,@@@=", "valid Base64"},
		{"mismatched MIME", "reference_image", "data:image/jpeg;base64," + museTestPNG(t), "declared image format"},
		{"image mislabeled as audio", "audios", []string{"data:audio/wav;base64," + museTestPNG(t)}, "declared image format"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"model": MuseVideoModel, "prompt": "Animate the object", test.field: test.value})
			require.NoError(t, err)
			req, err := ParseMuseMediaRequest(MuseVideoCreate, "application/json", body)
			if err == nil {
				_, err = req.PrepareReferences(t.Context())
			}
			require.ErrorContains(t, err, test.message)
		})
	}
}

func TestMuseVideoExplicitAccountAndNativeSubmission(t *testing.T) {
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://muse.example/v1", "api_key": "native-test-key",
	}}
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseVideo))
	a.Credentials["model_mapping"] = map[string]any{"muse-video": "muse-video"}
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMuseVideo))

	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task_0123456789abcdef0123","status":"queued"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
	result, receipt, err := svc.ForwardMuseVideo(context.Background(), c, a, MuseVideoCreate, "", []byte(`{"model":"muse-video","prompt":"waves"}`))
	require.NoError(t, err)
	require.Equal(t, "muse_task_0123456789abcdef0123", result.ResponseID)
	require.Equal(t, result.ResponseID, receipt["task_id"])
	require.False(t, w.Flushed)
	require.Empty(t, w.Body.String(), "the handler persists ownership before returning the receipt")
	require.Equal(t, "https://muse.example/v1/videos", upstream.request.URL.String())
	require.Equal(t, "Bearer native-test-key", upstream.request.Header.Get("Authorization"))
}

func TestMuseVideoStatusExposesOnlyPublicDownload(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{
		"id":"task_0123456789abcdef0123","status":"completed","progress":100,
		"account":"internal","prompt":"secret prompt",
		"url":"http://private.example/v1/media/0123456789abcdef0123456789abcdef.mp4",
		"result":{"filename":"0123456789abcdef0123456789abcdef.mp4"}
	}`)}
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://muse.example", "api_key": "native-test-key",
		"model_mapping": map[string]any{"muse-video": "muse-video"},
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/muse_task_0123456789abcdef0123", nil)
	result, _, err := svc.ForwardMuseVideo(context.Background(), c, a, MuseVideoStatus, "muse_task_0123456789abcdef0123", nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.VideoCount)
	require.Contains(t, w.Body.String(), "/v1/videos/muse_task_0123456789abcdef0123/content")
	require.NotContains(t, w.Body.String(), "private.example")
	require.NotContains(t, w.Body.String(), "secret prompt")
	require.NotContains(t, w.Body.String(), "internal")
}

func TestMuseVideoDownloadUsesOnlyBoundOriginAndSendsNoCredential(t *testing.T) {
	const file = "0123456789abcdef0123456789abcdef.mp4"
	const payload = "\x00\x00\x00\x18ftypmp42example"
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
		grokMediaContentStatusResponse(`{"id":"task_0123456789abcdef0123","status":"completed","url":"https://untrusted.example/video.mp4","result":{"filename":"` + file + `"}}`),
		{StatusCode: http.StatusOK, ContentLength: int64(len(payload)), Body: io.NopCloser(strings.NewReader(payload))},
	}}
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://muse.example/v1", "api_key": "native-test-key",
		"model_mapping": map[string]any{"muse-video": "muse-video"},
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/muse_task_0123456789abcdef0123/content", nil)
	result, _, err := svc.ForwardMuseVideo(t.Context(), c, a, MuseVideoContent, "muse_task_0123456789abcdef0123", nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, payload, w.Body.String())
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://muse.example/v1/media/"+file, upstream.requests[1].URL.String())
	require.Empty(t, upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
}

func TestMuseWalletPricesWithZeroTextTokensAndFiveSecondVideo(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	billing.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 6.75})
	const groupID = 27
	textPrice := 0.005
	group := &Group{
		ID: groupID, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard,
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformOpenAI, Models: []string{"muse-spark"},
			Currency: PricingCurrencyUSD, BillingMode: BillingModePerRequest, PerRequestPrice: &textPrice,
		}},
		VideoModelPrices: map[string]map[string]float64{MuseVideoModel: {VideoBillingResolution720P: 0.06}},
	}
	key := &APIKey{Group: group}
	svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	text, err := svc.calculateOpenAIRecordUsageTokenCost(t.Context(), key, "muse-spark", 0.5, time.Time{}, UsageTokens{}, "", "", nil, false)
	require.NoError(t, err)
	finalizeUsageCurrency(text, key, false)
	require.Equal(t, "CNY", costCurrency(text))
	require.InDelta(t, 0.0025, text.ActualCost, 1e-10)

	video := svc.calculateOpenAIVideoCost(t.Context(), MuseVideoModel, key, &OpenAIForwardResult{
		VideoCount: 1, VideoDurationSeconds: 5, VideoResolution: VideoBillingResolution720P,
	}, 0.5)
	finalizeUsageCurrency(video, key, false)
	require.Equal(t, "CNY", costCurrency(video))
	require.InDelta(t, 0.15, video.ActualCost, 1e-10)
}
