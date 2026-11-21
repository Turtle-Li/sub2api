//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCreativeCanvasJWTPostgresJourney(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	db := integrationDB
	users := NewUserRepository(client, db)
	u := mustCreateUser(t, client, &service.User{Balance: 5})
	other := mustCreateUser(t, client, &service.User{Balance: 5})
	cfg := &config.Config{}
	cfg.JWT.Secret = uuid.NewString()
	cfg.JWT.ExpireHour = 1
	cfg.Gateway.CreativeCanvasEnabled = true
	auth := service.NewAuthService(client, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	us := service.NewUserService(users, nil, nil, nil)
	token := func(id int64) string {
		x, e := users.GetByID(ctx, id)
		require.NoError(t, e)
		s, e := auth.GenerateToken(ctx, x)
		require.NoError(t, e)
		return s
	}
	me, them := token(u.ID), token(other.ID)
	store := NewCreativeCanvasRepository(db)
	h := handler.NewCreativeCanvasHandler(store, nil, nil, nil, cfg)
	router := gin.New()
	g := router.Group("/boards", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, us, nil, nil)))
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:id", h.Get)
	g.PUT("/:id", h.Put)
	g.DELETE("/:id", h.Delete)
	g.POST("/:id/nodes/:node/attempts", h.Bind)
	call := func(method, path, session string, data any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if data != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(data))
		}
		req := httptest.NewRequest(method, "/boards"+path, &buf)
		req.Header.Set("Content-Type", "application/json")
		if session != "" {
			req.Header.Set("Authorization", "Bearer "+session)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	board := func(w *httptest.ResponseRecorder) service.CreativeCanvas {
		require.Equal(t, 200, w.Code, w.Body.String())
		var out struct {
			Data service.CreativeCanvas `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out.Data
	}
	require.Equal(t, 401, call("GET", "", "", nil).Code)
	cfg.Gateway.CreativeCanvasEnabled = false
	require.Equal(t, 404, call("GET", "", me, nil).Code)
	cfg.Gateway.CreativeCanvasEnabled = true
	require.Equal(t, 400, call("POST", "", me, map[string]any{"title": "mine", "user_id": other.ID}).Code)
	b := board(call("POST", "", me, map[string]string{"title": "镜头实验"}))
	id := "/" + b.ID
	requestWith := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/boards"+id, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+me)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, 400, requestWith(`{"revision":1,"title":"镜头","document":{"viewport":{"x":0,"y":0,"zoom":1},"nodes":[{"id":"`+uuid.NewString()+`","kind":"note","x":0,"y":0,"url":"https://example.invalid/private"}],"edges":[]}}`).Code)
	require.Equal(t, 400, requestWith(`{"title":"x","revision":1,"document":{}} {}`).Code)
	require.Equal(t, 413, requestWith(`{"title":"`+strings.Repeat("x", service.CanvasBodyLimit+1)+`"}`).Code)
	require.Equal(t, 409, call("DELETE", id+"?revision=2", me, nil).Code)

	im, a, z := uuid.NewString(), uuid.NewString(), uuid.NewString()
	imageGroup := mustCreateGroup(t, client, &service.Group{Name: "Canvas Images " + uuid.NewString()})
	src := service.CanvasImage{Route: 0, Batch: "board_" + uuid.NewString(), Item: "cover", Index: 1}
	batch := func(user int64, ref service.CanvasImage) {
		_, e := db.ExecContext(ctx, `INSERT INTO batch_image_jobs(batch_id,user_id,api_key_id,studio_group_id,provider,model,status,item_count,currency) VALUES($1,$2,0,$3,'openai','fixture','completed',1,'USD')`, ref.Batch, user, imageGroup.ID)
		require.NoError(t, e)
		_, e = db.ExecContext(ctx, `INSERT INTO batch_image_items(job_id,custom_id,status,request_hash,image_count) VALUES($1,$2,'success','fixture',2)`, ref.Batch, ref.Item)
		require.NoError(t, e)
	}
	batch(u.ID, src)
	d := service.CanvasDocument{Viewport: service.CanvasViewport{X: 0, Y: -82, Zoom: .7}, Nodes: []service.CanvasNode{{ID: im, Kind: "image", Image: &src}, {ID: a, Kind: "video-draft", Prompt: "推近", X: 420}, {ID: z, Kind: "video-draft", Prompt: "静止", X: 420, Y: 240}}, Edges: []service.CanvasEdge{{Source: im, Target: a}, {Source: im, Target: z}}}
	save := func(doc service.CanvasDocument, rev int64) *httptest.ResponseRecorder {
		return call("PUT", id, me, service.CanvasUpdate{Title: b.Title, Revision: rev, Document: doc})
	}
	foreign := src
	foreign.Batch = "other_" + uuid.NewString()
	batch(other.ID, foreign)
	d.Nodes[0].Image = &foreign
	require.Equal(t, 422, save(d, 1).Code)
	wrong := src
	wrong.Index = 2
	d.Nodes[0].Image = &wrong
	require.Equal(t, 422, save(d, 1).Code)
	wrong = src
	wrong.Route = 91
	require.Equal(t, 422, save(d, 1).Code)
	archive := src
	archive.Batch = "archive_" + uuid.NewString()
	batch(u.ID, archive)
	_, e := db.ExecContext(ctx, `UPDATE batch_image_items SET provider_source_object='cos-jsonl:v1:1' WHERE job_id=$1`, archive.Batch)
	require.NoError(t, e)
	d.Nodes[0].Image = &archive
	require.Equal(t, 422, save(d, 1).Code)
	d.Nodes[0].Image = &src
	b = board(save(d, 1))
	require.Equal(t, int64(2), b.Revision)
	require.True(t, b.Assets[im].Available)
	require.Equal(t, d.Viewport, b.Document.Viewport)
	require.Equal(t, 409, save(d, 1).Code)
	require.Equal(t, 404, call("GET", id, them, nil).Code)
	require.Equal(t, 404, call("DELETE", id+"?revision=2", them, nil).Code)
	require.NotContains(t, call("GET", "", them, nil).Body.String(), b.ID)
	bind := func(node, request, session string, rev int64) *httptest.ResponseRecorder {
		return call("POST", id+"/nodes/"+node+"/attempts", session, map[string]any{"request_id": request, "revision": rev})
	}
	r1, r2 := uuid.NewString(), uuid.NewString()
	require.Equal(t, 409, bind(a, r1, me, 1).Code)
	require.Equal(t, 404, bind(a, r1, them, 2).Code)
	require.Equal(t, 200, bind(z, r2, me, 2).Code)
	require.Equal(t, 200, bind(a, r1, me, 2).Code)
	require.Equal(t, 200, bind(a, r1, me, 2).Code)
	require.Equal(t, 409, bind(z, r1, me, 2).Code)
	require.Equal(t, 404, bind(im, uuid.NewString(), me, 2).Code)
	b = board(call("GET", id, me, nil))
	require.Len(t, b.Attempts, 2)
	require.Nil(t, b.Attempts[0].Job)
	d.Nodes[1].PinnedID = r2
	require.Equal(t, 400, save(d, 2).Code)
	d.Nodes[1].PinnedID = ""
	otherSource := src
	otherSource.Batch = "ours_" + uuid.NewString()
	batch(u.ID, otherSource)
	d.Nodes[0].Image = &otherSource
	require.Equal(t, 400, save(d, 2).Code)
	d.Nodes[0].Image = &src
	_, e = db.ExecContext(ctx, `UPDATE batch_image_jobs SET output_expires_at=now()-interval '1 minute' WHERE batch_id=$1`, src.Batch)
	require.NoError(t, e)
	d.Nodes[0].X = 95
	b = board(save(d, 2))
	require.False(t, b.Assets[im].Available)
	require.Equal(t, 422, bind(a, uuid.NewString(), me, b.Revision).Code)
	require.Len(t, b.Document.Nodes, 3)
	require.Equal(t, 409, call("DELETE", id+"?revision=2", me, nil).Code)
	var money float64
	var jobs, keys int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT balance,(SELECT count(*) FROM video_studio_jobs WHERE user_id=$1),(SELECT count(*) FROM api_keys WHERE user_id=$1) FROM users WHERE id=$1`, u.ID).Scan(&money, &jobs, &keys))
	require.Equal(t, 5., money)
	require.Zero(t, jobs)
	require.Zero(t, keys)
	// Two tabs at the same revision: one write wins, the other sees a conflict, never silent last-writer-wins.
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, title := range []string{"分支甲", "分支乙"} {
		wg.Add(1)
		go func(title string) {
			defer wg.Done()
			errs <- store.Save(context.Background(), u.ID, b.ID, service.CanvasUpdate{Title: title, Revision: b.Revision, Document: d})
		}(title)
	}
	wg.Wait()
	close(errs)
	ok, conflict := 0, 0
	for err := range errs {
		switch err {
		case nil:
			ok++
		case service.ErrCanvasConflict:
			conflict++
		default:
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, ok)
	require.Equal(t, 1, conflict)
	require.Equal(t, 200, call("DELETE", id+"?revision=4", me, nil).Code)
	require.Equal(t, 404, call("GET", id, me, nil).Code)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM batch_image_jobs WHERE batch_id=$1`, src.Batch).Scan(&count))
	require.Equal(t, 1, count)
}

func TestCreativeCanvasSimultaneousReceiptsStayOnTheirBranches(t *testing.T) {
	ctx := t.Context()
	db := integrationDB
	client := testEntClient(t)
	users := NewUserRepository(client, db)
	u := mustCreateUser(t, client, &service.User{Balance: 3})
	group := mustCreateGroup(t, client, &service.Group{Name: "Camera variants " + uuid.NewString(), Platform: service.PlatformOpenAI, IsExclusive: true, RateMultiplier: 1, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{service.MuseVideoModel}}})
	_, err := db.ExecContext(ctx, `UPDATE groups SET allow_image_generation=true WHERE id=$1`, group.ID)
	require.NoError(t, err)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.JWT.Secret = uuid.NewString()
	cfg.Gateway.CreativeCanvasEnabled = true
	cfg.Gateway.VideoStudioMuseGroupID = group.ID
	cfg.Gateway.VideoStudioShareConfiguredGroups = true
	cfg.JWT.ExpireHour = 1
	keys := service.NewAPIKeyService(NewAPIKeyRepository(client, db), users, NewGroupRepository(client, db), nil, nil, nil, cfg)
	gw := service.NewOpenAIGatewayService(nil, nil, nil, users, nil, nil, NewGatewayCache(integrationRedis), cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	t.Cleanup(gw.CloseOpenAIWSPool)
	store := NewCreativeCanvasRepository(db)
	h := handler.NewCreativeCanvasHandler(store, keys, gw, nil, cfg)
	route := gin.New()
	route.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: u.ID}) })
	route.GET("/:id", h.Get)
	route.PUT("/:id", h.Put)
	b, e := store.Create(ctx, u.ID, "镜头比对")
	require.NoError(t, e)
	a, z := uuid.NewString(), uuid.NewString()
	b.Document.Nodes = []service.CanvasNode{{ID: a, Kind: "video-draft"}, {ID: z, Kind: "video-draft"}}
	require.NoError(t, store.Save(ctx, u.ID, b.ID, service.CanvasUpdate{Title: b.Title, Revision: 1, Document: b.Document}))
	s1, s2, s3 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, p := range [][2]string{{a, s1}, {a, s2}, {z, s3}} {
		require.NoError(t, store.Bind(ctx, u.ID, b.ID, p[0], p[1], 2))
	}
	read := func() service.CreativeCanvas {
		w := httptest.NewRecorder()
		route.ServeHTTP(w, httptest.NewRequest("GET", "/"+b.ID, nil))
		require.Equal(t, 200, w.Code, w.Body.String())
		var d struct{ Data service.CreativeCanvas }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &d))
		return d.Data
	}
	// The studio writer, not the board, materializes results in reverse order. No provider is configured.
	for _, id := range []string{s3, s2, s1} {
		inserted, e := keys.VideoStudioStore().ClaimVideoStudioJob(ctx, service.VideoStudioJob{ID: id, UserID: u.ID, KeyID: 0, GroupID: group.ID, Model: service.MuseVideoModel, Seconds: 6, Size: "16:9", Fingerprint: "local"})
		require.NoError(t, e)
		require.True(t, inserted)
		require.NoError(t, keys.VideoStudioStore().UpdateVideoStudioJob(ctx, u.ID, id, "test-only-"+id, "completed", false))
		board := read()
		for _, x := range board.Attempts {
			if x.RequestID == id {
				require.NotNil(t, x.Job)
				require.Equal(t, id, x.Job.ID)
				if id == s3 {
					require.Equal(t, z, x.NodeID)
				} else {
					require.Equal(t, a, x.NodeID)
				}
			}
		}
	}
	b.Document.Nodes[0].PinnedID = s1
	b.Document.Nodes = append(b.Document.Nodes, service.CanvasNode{Kind: "video", ID: uuid.NewString(), VideoID: s3})
	var data bytes.Buffer
	require.NoError(t, json.NewEncoder(&data).Encode(service.CanvasUpdate{Title: b.Title, Revision: 2, Document: b.Document}))
	w := httptest.NewRecorder()
	route.ServeHTTP(w, httptest.NewRequest("PUT", "/"+b.ID, &data))
	require.Equal(t, 200, w.Code, w.Body.String())
	again := read()
	require.Equal(t, s1, again.Document.Nodes[0].PinnedID)
	require.True(t, again.Assets[b.Document.Nodes[2].ID].Available)
	require.Len(t, again.Attempts, 3)
	// A recorded association is not permission to submit from an out-of-date tab.
	require.ErrorIs(t, store.Bind(ctx, u.ID, b.ID, a, s1, 2), service.ErrCanvasConflict)
	require.NoError(t, store.Bind(ctx, u.ID, b.ID, a, s1, again.Revision))
	require.NoError(t, store.Remove(ctx, u.ID, b.ID, 3))
	_, err = keys.VideoStudioStore().FindVideoStudioJob(ctx, u.ID, s3)
	require.NoError(t, err)
}
