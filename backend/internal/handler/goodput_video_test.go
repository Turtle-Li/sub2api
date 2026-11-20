//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type goodPutVideoTestJobs struct {
	service.UsageBillingRepository
	job        service.DurableVideoJob
	reserveErr error
}

func (r *goodPutVideoTestJobs) ReserveVideoJob(_ context.Context, job service.DurableVideoJob, _ bool) error {
	if r.reserveErr != nil {
		return r.reserveErr
	}
	r.job = job
	return nil
}
func (r *goodPutVideoTestJobs) AttachVideoReceipt(_ context.Context, id, publicID, nativeID string) error {
	if id != r.job.ID {
		return errors.New("unexpected reservation id")
	}
	r.job.PublicID, r.job.NativeID = publicID, nativeID
	return nil
}
func (r *goodPutVideoTestJobs) FindVideoJob(_ context.Context, id string, userID, keyID, groupID int64) (*service.DurableVideoJob, error) {
	if id == r.job.PublicID && userID == r.job.UserID && keyID == r.job.APIKeyID && groupID == r.job.GroupID {
		return &r.job, nil
	}
	return nil, nil
}
func (*goodPutVideoTestJobs) ClaimVideoJob(context.Context) (*service.DurableVideoJob, error) {
	return nil, nil
}
func (*goodPutVideoTestJobs) FinishVideoJob(context.Context, string, string) (bool, error) {
	return true, nil
}

func goodPutVideoTestContext(t *testing.T, model, taskID string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, w := grokMediaSlotContext(t.Context(), taskID == "")
	key, ok := middleware.GetAPIKeyFromContext(c)
	require.True(t, ok)
	key.Group.Platform = service.PlatformOpenAI
	key.Group.IsExclusive = true
	key.Group.SubscriptionType = service.SubscriptionTypeStandard
	key.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{model}}
	price := 0.15
	key.Group.ModelPricing = []service.ChannelModelPricing{{
		Models: []string{model}, BillingMode: service.BillingModeVideo, Currency: "USD", PerRequestPrice: &price,
	}}
	key.Group.RateMultiplier = 1
	if taskID != "" {
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+taskID, nil)
		c.Params = gin.Params{{Key: "request_id", Value: taskID}}
	} else {
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(goodPutVideoTestPayload(model, `"5"`)))
		c.Request.Header.Set("Content-Type", "application/json")
	}
	return c, w
}

func goodPutVideoTestPayload(model, seconds string) string {
	return `{"model":"` + model + `","prompt":"Sunrise over a mountain","seconds":` + seconds + `,"size":"1344x768","media":[{"type":"first_frame","url":"https://media.example/start.png"},{"type":"last_frame","url":"https://media.example/end.png"}]}`
}

func goodPutVideoTestCatalog(model, price string) string {
	return `{"models":[{"public_model":"` + model + `","price":{"mode":"meter","currency":"CNY","per_video_second":"` + price + `","meter_prices":[{"meter":"video_seconds","unit":"second","price_per_unit":"` + price + `"}]}}]}`
}

func TestGoodPutVideoOwnerBoundLifecycle(t *testing.T) {
	for _, tc := range []struct {
		model   string
		seconds int
	}{
		{service.GoodPutH3Model, 15},
		{service.GoodPutH3FastModel, 10},
	} {
		model, seconds := tc.model, strconv.Itoa(tc.seconds)
		t.Run(model, func(t *testing.T) {
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, model)
			nativeID := "video_" + strings.Repeat("provider_metadata_", 12)
			const mp4 = "\x00\x00\x00\x18ftypmp42test-video"
			var submits, statusCalls, downloads int
			state := "in_progress"
			upstream.call = func(req *http.Request, accountID int64) (*http.Response, error) {
				require.Equal(t, int64(1), accountID)
				require.True(t, service.HTTPUpstreamRedirectsDisabled(req.Context()))
				response := func(body string) *http.Response {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
				}
				switch req.URL.Path {
				case "/api/v1/public/models":
					require.Equal(t, "goodputai.cn", req.URL.Host)
					require.Empty(t, req.Header.Get("Authorization"))
					return response(goodPutVideoTestCatalog(model, "0.090000")), nil
				case "/v1/videos":
					submits++
					require.Equal(t, http.MethodPost, req.Method)
					require.Equal(t, "api.goodputai.cn", req.URL.Host)
					require.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
					require.Nil(t, req.GetBody)
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.JSONEq(t, goodPutVideoTestPayload(model, seconds), string(body))
					return response(`{"id":"` + nativeID + `","status":"queued"}`), nil
				case "/v1/videos/" + nativeID:
					statusCalls++
					return response(`{"id":"` + nativeID + `","status":"` + state + `","usage":{"duration_seconds":` + seconds + `},"video_url":"https://private-storage.example/signed"}`), nil
				case "/v1/videos/" + nativeID + "/content":
					downloads++
					resp := response(mp4)
					resp.Header.Set("Content-Type", "video/mp4")
					return resp, nil
				default:
					t.Fatalf("unexpected upstream path %q", req.URL.Path)
					return nil, nil
				}
			}
			c, w := goodPutVideoTestContext(t, model, "")
			c.Request.Body = io.NopCloser(strings.NewReader(goodPutVideoTestPayload(model, `"`+seconds+`"`)))
			c.Request.ContentLength = int64(len(goodPutVideoTestPayload(model, `"`+seconds+`"`)))
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.IsExclusive = false
			h.GoodPutVideoGeneration(c)
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			var receipt struct{ ID string }
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &receipt))
			require.True(t, service.IsGoodPutVideoTaskID(receipt.ID))
			require.NotContains(t, w.Body.String(), nativeID)
			require.Zero(t, len(bindings.usage))

			c, w = goodPutVideoTestContext(t, model, receipt.ID)
			key, _ = middleware.GetAPIKeyFromContext(c)
			key.ID++
			key.Group.IsExclusive = false
			h.GoodPutVideoStatus(c)
			require.Equal(t, http.StatusNotFound, w.Code)
			require.Zero(t, statusCalls)

			c, w = goodPutVideoTestContext(t, model, receipt.ID)
			h.GoodPutVideoContent(c)
			require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
			require.Zero(t, downloads)
			require.Zero(t, len(bindings.usage))

			state = "completed"
			c, w = goodPutVideoTestContext(t, model, receipt.ID)
			key, _ = middleware.GetAPIKeyFromContext(c)
			key.Group.IsExclusive = false
			h.GoodPutVideoStatus(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "/v1/videos/"+receipt.ID+"/content")
			require.NotContains(t, w.Body.String(), "private-storage")
			c, w = goodPutVideoTestContext(t, model, receipt.ID)
			key, _ = middleware.GetAPIKeyFromContext(c)
			key.Group.IsExclusive = false
			h.GoodPutVideoContent(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, mp4, w.Body.String())
			require.Equal(t, 1, submits)
			require.Equal(t, 1, downloads)
			// Reads must not charge a durable job; only the worker settles it.
			require.Empty(t, bindings.usage)
			require.Equal(t, receipt.ID, bindings.goodPut.job.PublicID)
			require.Equal(t, model, bindings.goodPut.job.Model)
			require.Equal(t, tc.seconds, bindings.goodPut.job.Seconds)
			require.InDelta(t, 0.15*float64(tc.seconds), bindings.goodPut.job.HoldAmount, 0.000001)
			slots.assertReleased(t)
		})
	}
}

func TestGoodPutVideoRejectsPriceIncreaseAndDoesNotReplay(t *testing.T) {
	for _, tt := range []struct {
		name, price string
		status, n   int
	}{
		{"promotion ended", "0.50", http.StatusServiceUnavailable, 1},
		{"ambiguous POST failure", "0.09", http.StatusBadGateway, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const model = service.GoodPutH3Model
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, model)
			upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
				if req.URL.Path == "/api/v1/public/models" {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(goodPutVideoTestCatalog(model, tt.price)))}, nil
				}
				require.Equal(t, http.MethodPost, req.Method)
				return nil, errors.New("response lost")
			}
			c, w := goodPutVideoTestContext(t, model, "")
			h.GoodPutVideoGeneration(c)
			require.Equal(t, tt.status, w.Code, w.Body.String())
			require.Equal(t, tt.n, upstream.calls)
			require.Empty(t, bindings.pending)
			slots.assertReleased(t)
		})
	}
}

func TestGoodPutVideoInsufficientFundsStopsBeforePost(t *testing.T) {
	const model = service.GoodPutH3Model
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, model)
	bindings.goodPut.reserveErr = service.ErrGoodPutVideoFunds
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(goodPutVideoTestCatalog(model, "0.09")))}, nil
	}
	c, w := goodPutVideoTestContext(t, model, "")
	h.GoodPutVideoGeneration(c)
	require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
	require.Equal(t, 1, upstream.calls)
	slots.assertReleased(t)
}
