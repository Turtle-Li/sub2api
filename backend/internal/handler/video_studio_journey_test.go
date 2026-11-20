//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type studioJourneyRepo struct {
	service.APIKeyRepository
	key         *service.APIKey
	rows        map[string]service.VideoStudioJob
	failSave    bool
	bearerReads int
}

func (r *studioJourneyRepo) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	r.bearerReads++
	if id == r.key.ID {
		return r.key, nil
	}
	return nil, service.ErrAPIKeyNotFound
}
func (*studioJourneyRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }
func (r *studioJourneyRepo) VideoStudioGroupIDs(_ context.Context, user int64) ([]int64, error) {
	if user != r.key.UserID {
		return nil, nil
	}
	return []int64{*r.key.GroupID}, nil
}
func (r *studioJourneyRepo) ClaimVideoStudioJob(_ context.Context, j service.VideoStudioJob) (bool, error) {
	if _, ok := r.rows[j.ID]; ok {
		return false, nil
	}
	j.Status = "submitting"
	j.CreatedAt = time.Now()
	r.rows[j.ID] = j
	return true, nil
}
func (r *studioJourneyRepo) FindVideoStudioJob(_ context.Context, user int64, id string) (service.VideoStudioJob, error) {
	j, ok := r.rows[id]
	if !ok || j.UserID != user {
		return service.VideoStudioJob{}, service.ErrVideoStudioNotFound
	}
	return j, nil
}
func (r *studioJourneyRepo) ListVideoStudioJobs(_ context.Context, user int64, _ string, _ int) ([]service.VideoStudioJob, error) {
	items := []service.VideoStudioJob{}
	for _, j := range r.rows {
		if j.UserID == user {
			items = append(items, j)
		}
	}
	return items, nil
}
func (r *studioJourneyRepo) RecordVideoStudioDuration(_ context.Context, j service.VideoStudioJob, ms int64) error {
	current, ok := r.rows[j.ID]
	if ok && j.UserID == current.UserID && j.GroupID == current.GroupID && j.KeyID == current.KeyID && j.Receipt == current.Receipt && current.ActualDurationMS == 0 && current.Status == "completed" {
		current.ActualDurationMS = ms
		r.rows[j.ID] = current
	}
	return nil
}
func (r *studioJourneyRepo) UpdateVideoStudioJob(_ context.Context, user int64, id, receipt, state string, review bool) error {
	if r.failSave {
		return errors.New("simulated persistence failure")
	}
	j := r.rows[id]
	j.Receipt = receipt
	j.Status = state
	j.BillingReview = review
	r.rows[id] = j
	return nil
}

type studioJourneyRun func(string, string, []byte) *httptest.ResponseRecorder

type studioJourneyUsers struct {
	service.UserRepository
	user *service.User
}

func (r studioJourneyUsers) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

type studioJourneyGroups struct {
	service.GroupRepository
	g *service.Group
}

func (r studioJourneyGroups) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if id != r.g.ID {
		return nil, service.ErrGroupNotFound
	}
	return r.g, nil
}
func studioJourney(t *testing.T, h *OpenAIGatewayHandler, model string) (*service.APIKey, *studioJourneyRepo, *int64, studioJourneyRun) {
	t.Helper()
	h.cfg.Gateway.VideoStudioEnabled = true
	c, _ := goodPutVideoTestContext(t, model, "")
	k, ok := middleware.GetAPIKeyFromContext(c)
	require.True(t, ok)
	k.Status = service.StatusActive
	k.Group.Status = service.StatusActive
	k.Group.Hydrated = true
	k.User.Status = service.StatusActive
	k.User.Balance = 7
	k.User.Concurrency = 5
	k.User.AllowedGroups = []int64{*k.GroupID}
	repo := &studioJourneyRepo{key: k, rows: map[string]service.VideoStudioJob{}}
	h.apiKeyService = service.NewAPIKeyService(repo, studioJourneyUsers{user: k.User}, studioJourneyGroups{g: k.Group}, nil, nil, nil, h.cfg)
	h.cfg.RunMode = config.RunModeStandard
	h.billingCacheService = service.NewBillingCacheService(nil, studioJourneyUsers{user: k.User}, nil, nil, nil, nil, h.cfg, nil)
	t.Cleanup(h.billingCacheService.Stop)
	router := gin.New()
	user := k.UserID
	root := router.Group("/api/v1/video-studio", func(c *gin.Context) {
		if user == 0 {
			c.AbortWithStatus(401)
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user, Concurrency: 5})
	})
	bind := h.VideoStudioBind(middleware.OwnedSessionKeyAuth(h.apiKeyService, nil, h.cfg, VideoStudioKeyID))
	root.GET("/models", h.VideoStudioModels)
	root.GET("/jobs", h.VideoStudioList)
	root.POST("/jobs", middleware.RequestBodyLimit(VideoStudioBodyLimit, httputil.NewBodyMemoryBudget(512<<20)), h.PrepareVideoStudioSubmit, bind, h.VideoStudioSubmit)
	root.GET("/jobs/:id", h.PrepareVideoStudioRead, bind, h.VideoStudioGet)
	root.GET("/jobs/:id/poster", h.PrepareVideoStudioRead, bind, h.VideoStudioPoster)
	root.GET("/jobs/:id/content", h.PrepareVideoStudioRead, bind, h.VideoStudioContent)
	run := func(method, path string, body []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/video-studio/"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key-ID", "99999")
		router.ServeHTTP(w, req)
		return w
	}
	return k, repo, &user, run
}

func TestVideoStudioH3SessionJourney(t *testing.T) {
	model := service.GoodPutH3Model
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, model)
	k, repo, user, run := studioJourney(t, h, model)
	posts := 0
	polls := 0
	reads := 0
	state := "in_progress"
	catalogPrice := "0.09"
	unknown := false
	mp4 := "\x00\x00\x00\x18ftypisom" + strings.Repeat("x", 24)
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		res := func(b string) *http.Response {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(b)), ContentLength: int64(len(b))}
		}
		switch req.URL.Path {
		case "/api/v1/public/models":
			return res(goodPutVideoTestCatalog(model, catalogPrice)), nil
		case "/v1/videos":
			posts++
			if unknown {
				return nil, errors.New("simulated timeout after dispatch")
			}
			return res(`{"id":"video_studio","status":"queued"}`), nil
		case "/v1/videos/video_studio":
			polls++
			return res(`{"id":"video_studio","status":"` + state + `","usage":{"duration_seconds":5}}`), nil
		case "/v1/videos/video_studio/content":
			reads++
			s := res(mp4)
			s.Header.Set("Content-Type", "video/mp4")
			return s, nil
		default:
			t.Fatalf("unexpected fixture call: %s", req.URL.Path)
			return nil, nil
		}
	}
	w := run("GET", "models", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var catalog struct {
		Data struct {
			Items []service.VideoStudioModel `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &catalog))
	require.Len(t, catalog.Data.Items, 1, w.Body.String())
	m := catalog.Data.Items[0]
	require.Equal(t, 0.75, m.Quotes[5])
	require.Equal(t, "USD", m.Currency)
	require.Zero(t, posts)
	q := studioSubmission{ID: uuid.NewString(), Model: model, Quote: m.QuoteID, Prompt: "orbit", Seconds: 5, First: testStudioPNG, Last: testStudioPNG}
	submit := func() *httptest.ResponseRecorder {
		b, err := json.Marshal(q)
		require.NoError(t, err)
		return run("POST", "jobs", b)
	}
	w = submit()
	require.Equal(t, 202, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), q.ID)
	require.NotContains(t, w.Body.String(), "gpv_")
	require.Equal(t, 1, posts)
	require.Equal(t, 200, submit().Code)
	require.Equal(t, 1, posts)
	q.Prompt = "different"
	require.Equal(t, 409, submit().Code)
	require.Equal(t, 1, posts)
	q.Prompt = "orbit"
	require.Equal(t, 200, run("GET", "jobs", nil).Code)
	require.Zero(t, polls)
	require.Zero(t, reads)
	state = "completed"
	w = run("GET", "jobs/"+q.ID, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "completed")
	require.Equal(t, 1, polls)
	k.User.Balance = 0
	h.cfg.Gateway.VideoStudioEnabled = false
	require.Equal(t, 200, submit().Code, "a duplicate is a receipt read, even when new jobs are disabled and the wallet is empty")
	w = run("GET", "jobs/"+q.ID+"/content", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, mp4, w.Body.String())
	require.Equal(t, 1, reads)
	*user++
	require.Equal(t, 404, run("GET", "jobs/"+q.ID+"/content", nil).Code)
	*user--
	k.User.AllowedGroups = nil
	require.Equal(t, 404, run("GET", "jobs/"+q.ID+"/content", nil).Code)
	k.User.AllowedGroups = []int64{k.Group.ID}
	require.Equal(t, 1, reads)
	q.ID = uuid.NewString()
	require.Equal(t, 403, submit().Code)
	h.cfg.Gateway.VideoStudioEnabled = true
	k.User.Balance = 7
	q.Quote = strings.Repeat("0", 64)
	require.Equal(t, 409, submit().Code)
	require.Equal(t, 1, posts)
	q.Quote = m.QuoteID
	catalogPrice = "0.50"
	w = submit()
	require.Equal(t, 503, w.Code, w.Body.String())
	require.Equal(t, "failed", repo.rows[q.ID].Status)
	require.Equal(t, 1, posts)
	catalogPrice = "0.09"
	q.ID = uuid.NewString()
	unknown = true
	w = submit()
	require.Equal(t, 202, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "uncertain")
	require.Equal(t, 200, submit().Code)
	require.Equal(t, 2, posts)
	unknown = false
	q.ID = uuid.NewString()
	repo.failSave = true
	w = submit()
	require.Equal(t, 503, w.Code)
	repo.failSave = false
	require.Equal(t, 200, submit().Code)
	require.Equal(t, 3, posts)
	*user = 0
	require.Equal(t, 401, run("GET", "jobs/"+q.ID+"/content", nil).Code)
	slots.assertReleased(t)
}

type studioJourneyBilling struct {
	service.UsageBillingRepository
	bindings *grokMediaSlotBindings
}

func (r studioJourneyBilling) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.bindings.museCharges = append(r.bindings.museCharges, cmd)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func TestVideoStudioMuseSessionObservesOnlyOnDemand(t *testing.T) {
	const model = service.MuseVideoModel
	h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformOpenAI, model)
	key, repo, user, run := studioJourney(t, h, model)
	price := 0.15
	key.Group.VideoPrice720P = &price
	key.Group.ModelPricing = nil
	const nativeID = "task_0123456789abcdef0123"
	const filename = "0123456789abcdef0123456789abcdef.mp4"
	mp4 := "\x00\x00\x00\x18ftypisom" + strings.Repeat("x", 24)
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 3, 2))))
	second := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	posts, polls, reads := 0, 0, 0
	upstream.call = func(req *http.Request, accountID int64) (*http.Response, error) {
		require.Equal(t, int64(1), accountID)
		var body string
		switch req.URL.Path {
		case "/v1/videos":
			require.Equal(t, "POST", req.Method)
			posts++
			var payload map[string]any
			require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
			require.Equal(t, []any{testStudioPNG, second}, payload["images"])
			require.NotContains(t, payload, "media")
			require.NotContains(t, payload, "first_frame")
			require.NotContains(t, payload, "last_frame")
			require.Equal(t, float64(6), payload["duration"])
			body = `{"id":"` + nativeID + `","status":"completed"}`
		case "/v1/videos/" + nativeID:
			polls++
			body = `{"id":"` + nativeID + `","status":"completed","result":{"filename":"` + filename + `","bytes":36}}`
		case "/v1/media/" + filename:
			require.Empty(t, req.Header.Get("Authorization"))
			reads++
			body = mp4
		default:
			t.Fatalf("unexpected URL %s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	}
	getModel := func() string {
		var cat struct {
			Data struct{ Items []service.VideoStudioModel }
		}
		w := run("GET", "models", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cat))
		require.Len(t, cat.Data.Items, 1, w.Body.String())
		m := cat.Data.Items[0]
		require.Equal(t, model, m.ID)
		require.Equal(t, "USD", m.Currency)
		require.InDelta(t, 0.9, m.Quotes[6], 0.000001)
		return m.QuoteID
	}
	id := uuid.NewString()
	body, err := json.Marshal(studioSubmission{ID: id, Model: model, Quote: getModel(), Prompt: "slow pan", Seconds: 6, References: []string{testStudioPNG, second}})
	require.NoError(t, err)
	w := run("POST", "jobs", body)
	require.Equal(t, 202, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"queued"`)
	require.Equal(t, 200, run("POST", "jobs", body).Code)
	for range 3 {
		w = run("GET", "jobs", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), nativeID)
	}
	require.Equal(t, 1, posts)
	require.Zero(t, polls)
	require.Zero(t, reads)
	require.Empty(t, bindings.museCharges)
	for range 3 {
		w = run("GET", "jobs/"+id, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"status":"completed"`)
		require.NotContains(t, w.Body.String(), filename)
	}
	require.Zero(t, reads)
	require.Empty(t, bindings.billed)
	require.Empty(t, bindings.museCharges, "wallet does not enter the API-key billing path")
	require.Equal(t, 1, bindings.museLedger.captures)
	require.Zero(t, bindings.museLedger.job.APIKeyID)
	require.Equal(t, key.UserID, bindings.museLedger.job.UserID)
	require.Equal(t, key.Group.ID, bindings.museLedger.job.GroupID)
	require.InDelta(t, .9, bindings.museLedger.job.HoldAmount, 1e-8)
	require.Zero(t, repo.bearerReads)
	key.User.Balance = 0
	for range 2 {
		w = run("GET", "jobs/"+id+"/content", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, mp4, w.Body.String())
	}
	key.Group.IsExclusive = false
	require.Equal(t, 404, run("GET", "jobs/"+id+"/content", nil).Code)
	key.Group.IsExclusive = true
	*user++
	require.Equal(t, 404, run("GET", "jobs/"+id+"/content", nil).Code)
	require.Equal(t, 1, posts)
	require.Equal(t, 2, reads)
	require.Equal(t, 1, bindings.museLedger.captures)
	require.Zero(t, repo.bearerReads)
	slots.assertReleased(t)
}

// The HTTP journey never relies on a real credential. Monetary effects and racing
// settlement are covered separately against PostgreSQL, not by this transport fixture.
type journeyMuseLedger struct {
	goodPutVideoTestJobs
	captures int
}

func (r *journeyMuseLedger) AttachVideoReceipt(ctx context.Context, id, p, n string) error {
	if err := r.goodPutVideoTestJobs.AttachVideoReceipt(ctx, id, p, n); err != nil {
		return err
	}
	r.job.State = "pending"
	return nil
}
func (r *journeyMuseLedger) FinishVideoJob(_ context.Context, id, state string) (bool, error) {
	if id != r.job.ID || r.job.State != "pending" {
		return false, nil
	}
	r.job.State = state
	if state == "completed" {
		r.captures++
	}
	return true, nil
}
func (r studioJourneyBilling) MuseStudioVideoJobs() service.DurableVideoJobRepository {
	return r.bindings.museLedger
}
