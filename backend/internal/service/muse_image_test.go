//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type museImageLedgerCache struct {
	GatewayCache
	pending map[string][]byte
	claims  map[string]bool
}

type museImageSettledHistoryStub struct {
	openAIRecordUsageBillingRepoStub
	applied bool
}

func (s *museImageSettledHistoryStub) HasAppliedMuseImageUsage(_ context.Context, id string, keyID int64) (bool, error) {
	return s.applied && id == "muse-image:task_0123456789abcdef0123" && keyID == 20, nil
}

func (c *museImageLedgerCache) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	return c.pending[key], nil
}

func (c *museImageLedgerCache) SetGrokVideoPendingBilling(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.pending[key] = value
	return nil
}

func (c *museImageLedgerCache) ClaimGrokVideoBilled(_ context.Context, key string, _ time.Duration) (bool, error) {
	if c.claims[key] {
		return false, nil
	}
	c.claims[key] = true
	return true, nil
}

func TestMuseImageTaskResponseAndBoundDownload(t *testing.T) {
	const nativeID = "task_0123456789abcdef0123"
	const publicID = "muse_image_" + nativeID
	const file = "0123456789abcdef0123456789abcdef.png"
	png, err := base64.StdEncoding.DecodeString(museTestPNG(t))
	require.NoError(t, err)
	taskJSON := fmt.Sprintf(`{"id":%q,"status":"completed","kind":"image","created_at":1,"size":"2K","created":2,
		"account":"private","api_prompt":"internal prompt","result":{"filename":%q},
		"data":[{"url":"http://untrusted.example/%s","revised_prompt":"customer prompt","bytes":%d,"size":"2K","kind":"image"}]}`,
		nativeID, file, file, len(png))
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
		grokMediaContentStatusResponse(taskJSON),
		{StatusCode: http.StatusOK, ContentLength: int64(len(png)), Body: io.NopCloser(strings.NewReader(string(png)))},
	}}
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://muse.example/v1", "api_key": "native-test-key",
		"model_mapping": map[string]any{MuseImageModel: MuseImageModel},
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/images/tasks/"+publicID, nil)
	task, err := svc.ForwardMuseImage(t.Context(), c, a, publicID, "", nil)
	require.NoError(t, err)
	out := task.TaskResponse(c, true)
	require.Equal(t, "/v1/images/tasks/"+publicID+"/content", task.ImageResponse(c)["data"].([]gin.H)[0]["url"])
	require.NotContains(t, fmt.Sprint(out), "untrusted.example")
	require.NotContains(t, fmt.Sprint(out), "internal prompt")
	require.Equal(t, "muse-image:"+nativeID, task.UsageResult().RequestID)
	require.Equal(t, 1, task.UsageResult().ImageCount)
	require.NoError(t, svc.WriteMuseImageContent(t.Context(), c, a, task))
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Equal(t, png, w.Body.Bytes())
	require.Equal(t, "https://muse.example/v1/media/"+file, upstream.requests[1].URL.String())
	require.Empty(t, upstream.requests[1].Header.Get("Authorization"))
}

func TestMuseImageNativeTaskIdempotencyIsScopedAndPostCannotBeReplayed(t *testing.T) {
	gid := int64(27)
	key := &APIKey{ID: 20, UserID: 7, GroupID: &gid}
	otherKey := &APIKey{ID: 21, UserID: 7, GroupID: &gid}
	a, err := MuseImageIdempotencyKey(key, MuseImageCreate, "same-header")
	require.NoError(t, err)
	b, err := MuseImageIdempotencyKey(otherKey, MuseImageCreate, "same-header")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.NotContains(t, a, "same-header")
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": "https://muse.example/v1", "api_key": "native-test-key",
		"model_mapping": map[string]any{MuseImageModel: MuseImageModel},
	}}
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task_0123456789abcdef0123","status":"queued"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/images/tasks", nil)
	_, err = svc.ForwardMuseImage(t.Context(), c, account, "", a, []byte(`{"model":"muse-image","prompt":"x"}`))
	require.NoError(t, err)
	require.Equal(t, a, upstream.request.Header.Get("Idempotency-Key"))
	require.Nil(t, upstream.request.GetBody)
	require.False(t, w.Flushed)
}

func TestMuseImageUsesTieredNativeQuoteForAdmission(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	billing.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 7})
	price1K, price2K := 0.10, 0.18
	group := &Group{ID: 27, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.5,
		ModelPricing: []ChannelModelPricing{{Models: []string{MuseImageModel}, Currency: PricingCurrencyCNY, BillingMode: BillingModeImage,
			Intervals: []PricingInterval{{TierLabel: ImageBillingSize1K, PerRequestPrice: &price1K}, {TierLabel: ImageBillingSize2K, PerRequestPrice: &price2K}},
		}},
	}
	key := &APIKey{GroupID: &group.ID, Group: group, User: &User{ID: 10, Balance: 1}}
	svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	for _, test := range []struct {
		size string
		cost float64
	}{{ImageBillingSize2K, 0.09}, {ImageBillingSize4K, 0.05}} {
		require.True(t, svc.HasMuseImagePrice(t.Context(), key, test.size))
		reserve, ok := svc.EstimateInflightReservation(t.Context(), key, InflightEstimateRequest{Model: MuseImageModel, Kind: InflightEstimateImage, ImageSize: test.size})
		require.True(t, ok)
		require.InDelta(t, test.cost, reserve, 1e-10)
	}
}

func TestMuseImageSettlesOneWalletDebitAcrossStatusAndContentReads(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	ledger := &museImageSettledHistoryStub{}
	svc := newOpenAIRecordUsageServiceForTest(logs, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.usageBillingRepo = ledger
	svc.cache = &museImageLedgerCache{pending: map[string][]byte{}, claims: map[string]bool{}}
	svc.billingService.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 7})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	price, legacyPrice := 0.10, 0.08
	group := &Group{ID: 27, Platform: PlatformOpenAI, RateMultiplier: 1, SubscriptionType: SubscriptionTypeStandard, ImagePrice1K: &legacyPrice,
		ModelPricing: []ChannelModelPricing{{Models: []string{MuseImageModel}, Currency: PricingCurrencyCNY, BillingMode: BillingModeImage,
			Intervals: []PricingInterval{{TierLabel: ImageBillingSize1K, PerRequestPrice: &price}},
		}},
	}
	user := &User{ID: 249, Balance: 1}
	key := &APIKey{ID: 20, UserID: user.ID, GroupID: &group.ID, Group: group, User: user}
	account := &Account{ID: 227, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	task := &MuseImageTask{NativeID: "task_0123456789abcdef0123", CreatedAt: time.Now().Unix(), Size: "2K"}
	require.True(t, svc.HasMuseImagePrice(t.Context(), key, ImageBillingSize2K))
	for i, requestID := range []string{"status-poll", "repeat-poll", "content"} {
		if i == 2 {
			// Simulate cache loss after an already-paid task's price changes.
			svc.cache = &museImageLedgerCache{pending: map[string][]byte{}, claims: map[string]bool{}}
			price = 0.12
		}
		ctx := context.WithValue(t.Context(), ctxkey.ClientRequestID, requestID)
		require.NoError(t, svc.SettleMuseImage(ctx, &OpenAIRecordUsageInput{
			Result: task.UsageResult(), APIKey: key, Account: account, User: user,
			RequestPayloadHash: HashUsageRequestPayload([]byte(task.ID())),
		}))
		ledger.applied = true
	}
	require.Equal(t, 1, ledger.calls)
	require.Equal(t, 1, logs.calls)
	require.Equal(t, "muse-image:"+task.NativeID, ledger.lastCmd.RequestID)
	require.Equal(t, 1, ledger.lastCmd.ImageCount)
	require.NotNil(t, logs.lastLog.ImageSize)
	require.Equal(t, ImageBillingSize1K, *logs.lastLog.ImageSize)
	require.Equal(t, "CNY", logs.lastLog.Currency)
	require.InDelta(t, 0.10, ledger.lastCmd.BalanceCost, 1e-10)
}
