//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type hiAPIVideoTestJobs struct{ *goodPutVideoTestJobs }

func (r *hiAPIVideoTestJobs) HiAPIVideoJobs() service.DurableVideoJobRepository { return r }
func (r *hiAPIVideoTestJobs) AttachVideoReceipt(ctx context.Context, id, public, native string) error {
	if err := r.goodPutVideoTestJobs.AttachVideoReceipt(ctx, id, public, native); err != nil {
		return err
	}
	r.job.State = "pending"
	return nil
}

func TestHiAPIVideoGatewayOwnsReceiptAndKeepsCDNAnonymous(t *testing.T) {
	const (
		model   = service.HiAPISeedanceMiniModel
		native  = "tk-hiapi-01HZTQ8BX2N3GM3YFK4Z9D7VQR"
		payload = `{"model":"seedance-2.0-mini","prompt":"Connect the two pictures","duration":4,"resolution":"480p","aspect_ratio":"16:9","generate_audio":false,"first_frame_url":"https://assets.example/start.jpg","last_frame_url":"https://assets.example/end.jpg"}`
		mp4     = "\x00\x00\x00\x18ftypmp42test-video"
	)
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, model)
	var submit, query, download int
	upstream.call = func(req *http.Request, accountID int64) (*http.Response, error) {
		require.Equal(t, int64(1), accountID)
		require.True(t, service.HTTPUpstreamRedirectsDisabled(req.Context()))
		respond := func(body, contentType string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
		}
		switch req.URL.Host + req.URL.Path {
		case "api.hiapi.ai/v1/tasks":
			submit++
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
			require.NotEmpty(t, req.Header.Get("Idempotency-Key"))
			require.Nil(t, req.GetBody)
			return respond(`{"code":200,"data":{"taskId":`+strconv.Quote(native)+`}}`, "application/json")
		case "api.hiapi.ai/v1/tasks/" + native:
			query++
			require.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
			return respond(`{"code":200,"data":{"taskId":`+strconv.Quote(native)+`,"model":"seedance-2.0-mini","status":"success","output":[{"type":"video","url":"https://cdn.hiapi.ai/example.mp4"}]}}`, "application/json")
		case "cdn.hiapi.ai/example.mp4":
			download++
			require.Empty(t, req.Header.Get("Authorization"))
			require.True(t, service.HTTPUpstreamPublicHostsOnly(req.Context()))
			return respond(mp4, "video/mp4")
		default:
			t.Fatalf("unexpected provider request %v", req.URL)
			return nil, nil
		}
	}
	c, w := goodPutVideoTestContext(t, model, "")
	key, _ := middleware.GetAPIKeyFromContext(c)
	price := .15
	key.Group.VideoPrice480P = &price
	c.Request.Body = io.NopCloser(strings.NewReader(payload))
	c.Request.ContentLength = int64(len(payload))
	h.HiAPIVideoGeneration(c)
	require.Equal(t, 202, w.Code, w.Body.String())
	var receipt struct{ ID string }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &receipt))
	require.True(t, service.IsHiAPIVideoTaskID(receipt.ID))
	require.NotContains(t, w.Body.String(), native)
	require.InDelta(t, .60, bindings.hiAPI.job.HoldAmount, .000001)
	c, w = goodPutVideoTestContext(t, model, receipt.ID)
	testKey, _ := middleware.GetAPIKeyFromContext(c)
	testKey.ID++
	h.HiAPIVideoStatus(c)
	require.Equal(t, 404, w.Code, w.Body.String())
	c, w = goodPutVideoTestContext(t, model, receipt.ID)
	h.HiAPIVideoContent(c)
	require.Equal(t, 409, w.Code, w.Body.String())
	require.Zero(t, query)
	bindings.hiAPI.job.State = "completed"
	c, w = goodPutVideoTestContext(t, model, receipt.ID)
	h.HiAPIVideoStatus(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"completed"`)
	require.Zero(t, query)
	c, w = goodPutVideoTestContext(t, model, receipt.ID)
	c.Params = gin.Params{{Key: "request_id", Value: receipt.ID}}
	h.HiAPIVideoContent(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, mp4, w.Body.String())
	require.Equal(t, 1, submit)
	require.Equal(t, 1, query)
	require.Equal(t, 1, download)
	require.Empty(t, bindings.usage)
	slots.assertReleased(t)
}
