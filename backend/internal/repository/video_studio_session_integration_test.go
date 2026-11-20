//go:build integration

package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Fixed transport: there is no network fallback or H3 account in this test.
type localMuseStudioUpstream struct {
	service.HTTPUpstream
	account   int64
	native    string
	creates   atomic.Int32
	completed atomic.Bool
	reject    atomic.Bool
	ambiguous atomic.Bool
}

func (m *localMuseStudioUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	if id != m.account || req.URL.Host != "studio-muse.invalid" {
		return nil, fmt.Errorf("unexpected upstream")
	}
	var body string
	switch {
	case req.Method == http.MethodPost && req.URL.Path == "/v1/videos":
		var v struct {
			Resolution        string
			Duration, Timeout int
			Extra             string
		}
		if err := json.NewDecoder(req.Body).Decode(&v); err != nil || v.Duration != service.MuseVideoDefaultSeconds || v.Resolution != "720p" || v.Timeout != 120 || v.Extra != "soft light" {
			return nil, fmt.Errorf("unexpected native options")
		}
		m.creates.Add(1)
		if m.ambiguous.Load() {
			return nil, io.ErrUnexpectedEOF
		}
		if m.reject.Load() {
			return &http.Response{StatusCode: 422, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		body = `{"id":"` + m.native + `","kind":"video","status":"queued"}`
	case req.Method == http.MethodGet && req.URL.Path == "/v1/videos/"+m.native:
		body = `{"id":"` + m.native + `","kind":"video","status":"processing"}`
		if m.completed.Load() {
			body = `{"id":"` + m.native + `","kind":"video","status":"completed","result":{"filename":"0123456789abcdef0123456789abcdef.mp4"}}`
		}
	case req.Method == http.MethodGet && req.URL.Path == "/v1/media/0123456789abcdef0123456789abcdef.mp4":
		if req.Header.Get("Authorization") != "" {
			return nil, fmt.Errorf("credentials sent to media")
		}
		body = "\x00\x00\x00\x18ftypisom" + strings.Repeat("x", 24)
	default:
		return nil, fmt.Errorf("unsupported fixture path")
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestVideoStudioZeroKeyJWTPostgresJourney(t *testing.T) {
	ctx := t.Context()
	c := testEntClient(t)
	db := integrationDB
	users := NewUserRepository(c, db)
	groups := NewGroupRepository(c, db)
	accounts := NewAccountRepository(c, db, nil)
	u := mustCreateUser(t, c, &service.User{Balance: 2})
	other := mustCreateUser(t, c, &service.User{Balance: 2})
	g := mustCreateGroup(t, c, &service.Group{Name: "JWT wallet " + uuid.NewString(), Platform: service.PlatformOpenAI, IsExclusive: true, RateMultiplier: 1, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{service.MuseVideoModel}}})
	a := mustCreateAccount(t, c, &service.Account{Name: "offline Muse " + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://studio-muse.invalid/v1", "api_key": "test-only", "model_mapping": map[string]any{service.MuseVideoModel: service.MuseVideoModel}}})
	_, err := db.ExecContext(ctx, `UPDATE groups SET allow_image_generation=true, video_price_720p=.15 WHERE id=$1`, g.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, a.ID, g.ID)
	require.NoError(t, err)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.JWT.Secret = uuid.NewString()
	cfg.JWT.ExpireHour = 1
	cfg.Gateway.VideoStudioEnabled = true
	cfg.Gateway.VideoStudioMuseGroupID = g.ID
	keys := service.NewAPIKeyService(NewAPIKeyRepository(c, db), users, groups, nil, nil, nil, cfg)
	price := service.NewBillingService(cfg, nil)
	conc := service.NewConcurrencyService(NewConcurrencyCache(integrationRedis, 2, 15))
	bc := service.NewBillingCacheService(nil, users, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(bc.Stop)
	up := &localMuseStudioUpstream{account: a.ID, native: "task_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]}
	gw := service.NewOpenAIGatewayService(accounts, NewUsageLogRepository(c, db), NewUsageBillingRepository(c, db), users, nil, nil, NewGatewayCache(integrationRedis), cfg, nil, conc, price, nil, bc, up, nil, nil, nil, service.NewModelPricingResolver(nil, price), nil, nil, nil, nil)
	t.Cleanup(gw.CloseOpenAIWSPool)
	h := handler.NewOpenAIGatewayHandler(gw, conc, bc, keys, nil, nil, nil, nil, cfg)
	auth := service.NewAuthService(c, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	us := service.NewUserService(users, nil, nil, nil)
	router := gin.New()
	v := router.Group("/api/v1/video-studio", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, us, nil, nil)))
	bind := h.VideoStudioBind(middleware.OwnedSessionKeyAuth(keys, nil, cfg, handler.VideoStudioKeyID))
	v.GET("/models", h.VideoStudioModels)
	v.GET("/jobs", h.VideoStudioList)
	v.POST("/jobs", middleware.RequestBodyLimit(handler.VideoStudioBodyLimit, httputil.NewBodyMemoryBudget(512<<20)), h.PrepareVideoStudioSubmit, bind, h.VideoStudioSubmit)
	v.GET("/jobs/:id", h.PrepareVideoStudioRead, bind, h.VideoStudioGet)
	v.GET("/jobs/:id/content", h.PrepareVideoStudioRead, bind, h.VideoStudioContent)
	token := func(id int64) string {
		t.Helper()
		owner, err := users.GetByID(ctx, id)
		require.NoError(t, err)
		s, err := auth.GenerateToken(context.Background(), owner)
		require.NoError(t, err)
		return s
	}
	session := token(u.ID)
	request := func(method, path, auth string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/v1/video-studio/"+path, bytes.NewReader(body))
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, 401, request("GET", "models", "", nil).Code)
	catalog := func() []service.VideoStudioModel {
		w := request("GET", "models", session, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var result struct {
			Data struct{ Items []service.VideoStudioModel }
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		return result.Data.Items
	}
	require.Empty(t, catalog(), "a private routing group is not an implicit public grant")
	cfg.Gateway.VideoStudioShareConfiguredGroups = true
	models := catalog()
	require.Len(t, models, 1)
	require.Equal(t, service.VideoStudioDurationAutomatic, models[0].DurationMode)
	require.Len(t, models[0].Quotes, 1)
	require.InDelta(t, .9, models[0].Quotes[service.MuseVideoDefaultSeconds], 1e-8)
	require.NotNil(t, models[0].MuseOptions)
	id := uuid.NewString()
	// Canvas links precede payment and use the exact same receipt namespace as the existing studio.
	cfg.Gateway.CreativeCanvasEnabled = true
	boardStore := NewCreativeCanvasRepository(db)
	ch := handler.NewCreativeCanvasHandler(boardStore, keys, gw, nil, cfg)
	cg := router.Group("/canvas", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, us, nil, nil)))
	cg.GET("/:id", ch.Get)
	board, e := boardStore.Create(ctx, u.ID, "两种镜头")
	require.NoError(t, e)
	first, second, waiting := uuid.NewString(), uuid.NewString(), uuid.NewString()
	board.Document.Nodes = []service.CanvasNode{{ID: first, Kind: "video-draft", Prompt: "推进"}, {ID: second, Kind: "video-draft", Prompt: "静止"}}
	require.NoError(t, boardStore.Save(ctx, u.ID, board.ID, service.CanvasUpdate{Revision: 1, Title: board.Title, Document: board.Document}))
	require.NoError(t, boardStore.Bind(ctx, u.ID, board.ID, second, waiting, 2))
	require.NoError(t, boardStore.Bind(ctx, u.ID, board.ID, first, id, 2))
	readBoard := func() service.CreativeCanvas {
		r := httptest.NewRequest("GET", "/canvas/"+board.ID, nil)
		r.Header.Set("Authorization", "Bearer "+session)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code, w.Body.String())
		var result struct{ Data service.CreativeCanvas }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		return result.Data
	}
	before := readBoard()
	require.Len(t, before.Attempts, 2)
	require.Nil(t, before.Attempts[0].Job)
	require.Nil(t, before.Attempts[1].Job)
	require.Zero(t, up.creates.Load())
	payload := map[string]any{"request_id": id, "quote_id": models[0].QuoteID, "model": "muse-video", "prompt": "cup", "resolution": "720p", "extra": "soft light", "timeout": 120}
	submit := func() *httptest.ResponseRecorder {
		b, _ := json.Marshal(payload)
		return request("POST", "jobs", session, b)
	}
	payload["group_id"] = g.ID
	require.Equal(t, 400, submit().Code)
	delete(payload, "group_id")
	for _, invalid := range []any{0, 5, 10, 15, nil} {
		payload["seconds"] = invalid
		require.Equal(t, 400, submit().Code, "no custom Muse duration is sold")
	}
	delete(payload, "seconds")
	var indexed int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM video_studio_jobs WHERE user_id=$1`, u.ID).Scan(&indexed))
	require.Zero(t, indexed)
	require.Zero(t, up.creates.Load())
	_, err = db.ExecContext(ctx, `UPDATE groups SET video_price_720p=.2 WHERE id=$1`, g.ID)
	require.NoError(t, err)
	require.Equal(t, 409, submit().Code)
	require.Zero(t, up.creates.Load())
	_, err = db.ExecContext(ctx, `UPDATE groups SET video_price_720p=.15 WHERE id=$1`, g.ID)
	require.NoError(t, err)
	w := submit()
	require.Equal(t, 202, w.Code, w.Body.String())
	payload["seconds"] = service.MuseVideoDefaultSeconds
	require.Equal(t, 200, submit().Code, "old clients sending the same default recover the same receipt")
	delete(payload, "seconds")
	require.EqualValues(t, 1, up.creates.Load())
	require.Equal(t, 404, request("GET", "jobs/"+id, token(other.ID), nil).Code)
	up.completed.Store(true)
	// Leave the tab closed: only the real reconciliation worker may observe and capture now.
	require.Eventually(t, func() bool {
		var completed bool
		return db.QueryRowContext(ctx, `SELECT state='completed' AND seconds=6 AND parameters->>'duration_mode'='automatic' AND charged_amount=.9 FROM video_studio_jobs WHERE user_id=$1 AND id=$2`, u.ID, id).Scan(&completed) == nil && completed
	}, 12*time.Second, 100*time.Millisecond)
	for range 2 {
		w = request("GET", "jobs/"+id, session, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"charged_amount":0.9`)
		require.NotContains(t, w.Body.String(), "soft light")
	}
	require.Equal(t, 1, int(up.creates.Load()))
	for range 2 {
		current := readBoard()
		require.Len(t, current.Attempts, 2)
		for _, attempt := range current.Attempts {
			if attempt.NodeID == first {
				require.Equal(t, id, attempt.RequestID)
				require.NotNil(t, attempt.Job)
				require.Equal(t, "completed", attempt.Job.Status)
				require.Equal(t, id, attempt.Job.ID)
			} else {
				require.Equal(t, second, attempt.NodeID)
				require.Equal(t, waiting, attempt.RequestID)
				require.Nil(t, attempt.Job)
			}
		}
	}
	require.EqualValues(t, 1, up.creates.Load())
	_, e = db.ExecContext(ctx, `UPDATE groups SET allow_image_generation=false WHERE id=$1`, g.ID)
	require.NoError(t, e)
	denied := readBoard()
	for _, attempt := range denied.Attempts {
		if attempt.RequestID == id {
			require.True(t, attempt.Unavailable)
			require.Nil(t, attempt.Job)
		}
	}
	_, e = db.ExecContext(ctx, `UPDATE groups SET allow_image_generation=true WHERE id=$1`, g.ID)
	require.NoError(t, e)
	var balance, frozen float64
	var keyCount, groupCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT balance,frozen_balance,(SELECT count(*) FROM api_keys WHERE user_id=$1),(SELECT count(*) FROM user_allowed_groups WHERE user_id=$1) FROM users WHERE id=$1`, u.ID).Scan(&balance, &frozen, &keyCount, &groupCount))
	require.InDelta(t, 1.1, balance, 1e-8)
	require.Zero(t, frozen)
	require.Zero(t, keyCount)
	require.Zero(t, groupCount)
	// An acknowledged native rejection releases the hold. A lost reply keeps it frozen; neither case replays.
	payload["request_id"] = uuid.NewString()
	up.reject.Store(true)
	w = submit()
	require.Equal(t, 422, w.Code, w.Body.String())
	require.Equal(t, 200, submit().Code)
	require.EqualValues(t, 2, up.creates.Load())
	require.NoError(t, db.QueryRowContext(ctx, `SELECT balance,frozen_balance FROM users WHERE id=$1`, u.ID).Scan(&balance, &frozen))
	require.InDelta(t, 1.1, balance, 1e-8)
	require.Zero(t, frozen)
	up.reject.Store(false)
	_, err = db.ExecContext(ctx, `UPDATE users SET balance=.01 WHERE id=$1`, u.ID)
	require.NoError(t, err)
	payload["request_id"] = uuid.NewString()
	w = submit()
	require.True(t, w.Code == 402 || w.Code == 403, w.Body.String())
	require.EqualValues(t, 2, up.creates.Load())
	_, err = db.ExecContext(ctx, `UPDATE users SET balance=1.1 WHERE id=$1`, u.ID)
	require.NoError(t, err)
	payload["request_id"] = uuid.NewString()
	up.ambiguous.Store(true)
	w = submit()
	require.Equal(t, 202, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "uncertain")
	require.Equal(t, 200, submit().Code)
	require.EqualValues(t, 3, up.creates.Load())
	require.NoError(t, db.QueryRowContext(ctx, `SELECT balance,frozen_balance FROM users WHERE id=$1`, u.ID).Scan(&balance, &frozen))
	require.InDelta(t, .2, balance, 1e-8)
	require.InDelta(t, .9, frozen, 1e-8)
	// Replay an exact pre-automatic checkout intent. The new catalogue must not reprice,
	// reject or resubmit the old 5s receipt; the stored fingerprint predates this contract.
	oldID := uuid.NewString()
	oldWire := []byte(fmt.Sprintf(`{"request_id":"%s","model":"muse-video","quote_id":"%s","prompt":"legacy pan","seconds":5}`, oldID, strings.Repeat("a", 64)))
	store := &apiKeyRepository{sql: db}
	old := service.VideoStudioJob{ID: oldID, UserID: u.ID, GroupID: g.ID, Model: service.MuseVideoModel, Seconds: 5, Fingerprint: fmt.Sprintf("%x", sha256.Sum256(oldWire))}
	won, err := store.ClaimVideoStudioJob(ctx, old)
	require.NoError(t, err)
	require.True(t, won)
	w = request("POST", "jobs", session, oldWire)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"seconds":5`)
	require.NotContains(t, w.Body.String(), `"duration_mode"`)
	require.Equal(t, 409, request("POST", "jobs", session, bytes.Replace(oldWire, []byte(`"seconds":5`), []byte(`"seconds":6`), 1)).Code)
	require.EqualValues(t, 3, up.creates.Load())
	legacy := service.VideoStudioJob{ID: uuid.NewString(), UserID: u.ID, KeyID: 999999, GroupID: g.ID, Model: service.MuseVideoModel, Seconds: 5, Fingerprint: "existing-job"}
	won, err = store.ClaimVideoStudioJob(ctx, legacy)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, 404, request("GET", "jobs/"+legacy.ID+"/content", session, nil).Code, "new Studio sharing must not unlock a previous key's work")
	_, err = db.ExecContext(ctx, `UPDATE users SET restrict_public_groups=true WHERE id=$1`, u.ID)
	require.NoError(t, err)
	require.Empty(t, catalog())
	require.Equal(t, 404, request("GET", "jobs/"+id+"/content", session, nil).Code)
	_, err = db.ExecContext(ctx, `UPDATE users SET restrict_public_groups=false WHERE id=$1`, u.ID)
	require.NoError(t, err)
	cfg.Gateway.VideoStudioShareConfiguredGroups = false
	require.Empty(t, catalog())
	require.Equal(t, 404, request("GET", "jobs/"+id+"/content", session, nil).Code)
}
