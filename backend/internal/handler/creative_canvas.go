package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type canvasKeys interface {
	GetByID(context.Context, int64) (*service.APIKey, error)
	VideoStudioWalletGrants(context.Context, int64, []int64) ([]*service.APIKey, error)
}
type canvasMedia interface {
	StudioModelGroup(*service.Group, string) bool
	MarkVideoStudioPosters(context.Context, []service.VideoStudioJob)
}
type CreativeCanvasHandler struct {
	store service.CreativeCanvasStore
	keys  canvasKeys
	media canvasMedia
	subs  *service.SubscriptionService
	cfg   *config.Config
}

func NewCreativeCanvasHandler(store service.CreativeCanvasStore, keys *service.APIKeyService, media *service.OpenAIGatewayService, subs *service.SubscriptionService, cfg *config.Config) *CreativeCanvasHandler {
	return &CreativeCanvasHandler{store, keys, media, subs, cfg}
}
func canvasErr(c *gin.Context, err error) {
	status := 503
	msg := "画布暂时不可用"
	switch {
	case errors.Is(err, service.ErrCanvasNotFound):
		status = 404
	case errors.Is(err, service.ErrCanvasConflict):
		status = 409
	case errors.Is(err, service.ErrCanvasLimit):
		status = 422
	case errors.Is(err, service.ErrCanvasAsset):
		status = 422
	case errors.Is(err, service.ErrCanvasInvalid):
		status = 400
	}
	if status != 503 {
		msg = err.Error()
	}
	response.Error(c, status, msg)
}
func (h *CreativeCanvasHandler) user(c *gin.Context) int64 {
	c.Header("Cache-Control", "private, no-store")
	s, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || s.UserID <= 0 {
		response.Unauthorized(c, "请先登录")
		return 0
	}
	if h == nil || h.cfg == nil || !h.cfg.Gateway.CreativeCanvasEnabled || c.Param("id") != "" && !service.CanvasID(c.Param("id")) {
		canvasErr(c, service.ErrCanvasNotFound)
		return 0
	}
	return s.UserID
}
func canvasDecode(c *gin.Context, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, service.CanvasBodyLimit))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			response.Error(c, 413, "画布过大")
		} else {
			canvasErr(c, service.ErrCanvasInvalid)
		}
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		canvasErr(c, service.ErrCanvasInvalid)
		return false
	}
	return true
}
func (h *CreativeCanvasHandler) List(c *gin.Context) {
	u := h.user(c)
	if u == 0 {
		return
	}
	items, e := h.store.List(c.Request.Context(), u)
	if e != nil {
		canvasErr(c, e)
		return
	}
	response.Success(c, gin.H{"items": items})
}
func (h *CreativeCanvasHandler) Create(c *gin.Context) {
	u := h.user(c)
	if u == 0 {
		return
	}
	var q struct {
		Title string `json:"title"`
	}
	if !canvasDecode(c, &q) {
		return
	}
	if !service.CanvasTitle(q.Title) {
		canvasErr(c, service.ErrCanvasInvalid)
		return
	}
	b, e := h.store.Create(c.Request.Context(), u, strings.TrimSpace(q.Title))
	if e != nil {
		canvasErr(c, e)
		return
	}
	response.Success(c, b)
}
func (h *CreativeCanvasHandler) read(c *gin.Context, u int64) {
	b, e := h.store.Get(c.Request.Context(), u, c.Param("id"))
	if e == nil {
		e = h.resolve(c, u, &b)
	}
	if e != nil {
		canvasErr(c, e)
		return
	}
	response.Success(c, b)
}
func (h *CreativeCanvasHandler) Get(c *gin.Context) {
	if u := h.user(c); u > 0 {
		h.read(c, u)
	}
}
func (h *CreativeCanvasHandler) Put(c *gin.Context) {
	u := h.user(c)
	if u == 0 {
		return
	}
	var q service.CanvasUpdate
	if !canvasDecode(c, &q) {
		return
	}
	if q.Revision < 1 || !service.CanvasTitle(q.Title) || q.Document.Validate() != nil {
		canvasErr(c, service.ErrCanvasInvalid)
		return
	}
	q.Title = strings.TrimSpace(q.Title)
	ctx := c.Request.Context()
	old, e := h.store.Get(ctx, u, c.Param("id"))
	if e != nil {
		canvasErr(c, e)
		return
	}
	if old.Revision != q.Revision {
		canvasErr(c, service.ErrCanvasConflict)
		return
	}
	oldLineage, e := old.Document.Lineage()
	if e != nil {
		canvasErr(c, e)
		return
	}
	lineage, e := q.Document.Lineage()
	if e != nil {
		canvasErr(c, e)
		return
	}
	if e := service.CheckCanvasGraphVersion(q.GraphVersion, len(oldLineage.Continuation) > 0 || len(lineage.Continuation) > 0); e != nil {
		canvasErr(c, e)
		return
	}
	// Already-persisted references keep their positions after revocation/expiry. Never accept a new foreign reference as a placeholder.
	prev := map[string]service.CanvasNode{}
	for _, n := range old.Document.Nodes {
		prev[n.ID] = n
	}
	check := service.CreativeCanvas{Assets: map[string]service.CanvasAsset{}}
	for _, n := range q.Document.Nodes {
		p := prev[n.ID]
		if n.Image != nil && (p.Image == nil || *p.Image != *n.Image) || n.VideoID != "" && p.VideoID != n.VideoID {
			check.Document.Nodes = append(check.Document.Nodes, n)
		}
		if n.Kind == "video-draft" && (lineage.Parent[n.ID] != oldLineage.Parent[n.ID] || !lineage.Input[n.ID].Equal(oldLineage.Input[n.ID])) {
			in := lineage.Input[n.ID]
			if in.Image != nil || in.VideoID != "" {
				check.Document.Nodes = append(check.Document.Nodes, service.CanvasNode{ID: n.ID, Image: in.Image, VideoID: in.VideoID})
			}
		}
	}
	if e = h.resolve(c, u, &check); e != nil {
		canvasErr(c, e)
		return
	}
	for _, n := range check.Document.Nodes {
		if !check.Assets[n.ID].Available {
			canvasErr(c, service.ErrCanvasAsset)
			return
		}
	}
	if e = h.store.Save(ctx, u, c.Param("id"), q); e != nil {
		canvasErr(c, e)
		return
	}
	h.read(c, u)
}
func (h *CreativeCanvasHandler) Delete(c *gin.Context) {
	u := h.user(c)
	if u == 0 {
		return
	}
	rev, e := strconv.ParseInt(c.Query("revision"), 10, 64)
	if e != nil || rev < 1 {
		canvasErr(c, service.ErrCanvasInvalid)
		return
	}
	if e = h.store.Remove(c.Request.Context(), u, c.Param("id"), rev); e != nil {
		canvasErr(c, e)
		return
	}
	response.Success(c, gin.H{})
}
func (h *CreativeCanvasHandler) Bind(c *gin.Context) {
	u := h.user(c)
	if u == 0 {
		return
	}
	var q struct {
		ID           string `json:"request_id"`
		Revision     int64  `json:"revision"`
		GraphVersion int    `json:"graph_version,omitempty"`
	}
	if !canvasDecode(c, &q) {
		return
	}
	node := c.Param("node")
	if !service.CanvasID(node) || !service.CanvasID(q.ID) || q.Revision < 1 {
		canvasErr(c, service.ErrCanvasInvalid)
		return
	}
	ctx := c.Request.Context()
	board, err := h.store.Get(ctx, u, c.Param("id"))
	if err != nil {
		canvasErr(c, err)
		return
	}
	if !slices.ContainsFunc(board.Document.Nodes, func(n service.CanvasNode) bool { return n.ID == node && n.Kind == "video-draft" }) {
		canvasErr(c, service.ErrCanvasNotFound)
		return
	}
	if board.Revision != q.Revision {
		canvasErr(c, service.ErrCanvasConflict)
		return
	}
	lineage, err := board.Document.Lineage()
	if err != nil {
		canvasErr(c, err)
		return
	}
	if err := service.CheckCanvasGraphVersion(q.GraphVersion, lineage.Continuation[node]); err != nil {
		canvasErr(c, err)
		return
	}
	if input := lineage.Input[node]; input.Image != nil || input.VideoID != "" {
		check := service.CreativeCanvas{Document: service.CanvasDocument{Nodes: []service.CanvasNode{{ID: node, Image: input.Image, VideoID: input.VideoID}}}}
		if err = h.resolve(c, u, &check); err != nil {
			canvasErr(c, err)
			return
		}
		if !check.Assets[node].Available {
			canvasErr(c, service.ErrCanvasAsset)
			return
		}
	}
	if e := h.store.Bind(c.Request.Context(), u, c.Param("id"), node, q.ID, q.Revision); e != nil {
		canvasErr(c, e)
		return
	}
	response.Success(c, gin.H{"request_id": q.ID})
}

// Reuse the studio grant checks and private poster index. This reads only stored metadata, never media/URL/provider status.
func (h *CreativeCanvasHandler) resolve(c *gin.Context, u int64, b *service.CreativeCanvas) error {
	ctx := c.Request.Context()
	if b.Assets == nil {
		b.Assets = map[string]service.CanvasAsset{}
	}
	lineage, err := b.Document.Lineage()
	if err != nil {
		return err
	}
	if len(lineage.Continuation) > 0 {
		b.Inputs = make(map[string]service.CanvasResolvedInput, len(lineage.Continuation))
		for id := range lineage.Continuation {
			in := lineage.Input[id]
			b.Inputs[id] = service.CanvasResolvedInput{CanvasInput: in, Available: in.Image == nil && in.VideoID == ""}
		}
	}
	imgs := []service.CanvasImage{}
	ids := map[string]bool{}
	for _, n := range b.Document.Nodes {
		if n.Image != nil {
			imgs = append(imgs, *n.Image)
		}
		if n.VideoID != "" {
			ids[n.VideoID] = true
		}
	}
	for _, a := range b.Attempts {
		ids[a.RequestID] = true
	}
	if len(ids)+len(imgs) == 0 {
		return nil
	}
	images, e := h.store.ResolveImages(ctx, u, imgs)
	if e != nil {
		return e
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	jobs, e := h.store.ReadJobs(ctx, u, list)
	if e != nil {
		return e
	}
	legacy := map[int64]*service.APIKey{}
	keyFor := func(id int64) (*service.APIKey, error) {
		if key, ok := legacy[id]; ok {
			return key, nil
		}
		key, err := h.keys.GetByID(ctx, id)
		if err != nil && !errors.Is(err, service.ErrAPIKeyNotFound) {
			return nil, err
		}
		if !middleware.StudioMetadataKeyAllowed(c, h.cfg, key, u) {
			key = nil
		}
		if key != nil && key.Group.IsSubscriptionType() {
			if h.subs == nil {
				key = nil
			} else if sub, err := h.subs.GetActiveSubscription(ctx, u, key.Group.ID); err != nil || sub == nil {
				key = nil
			}
		}
		legacy[id] = key
		return key, nil
	}
	allowedImages := make(map[service.CanvasImage]bool, len(images))
	for _, n := range b.Document.Nodes {
		if n.Image == nil {
			continue
		}
		ok := images[*n.Image]
		if ok && n.Image.Route != service.ImageStudioWalletOwnerSlot {
			k, err := keyFor(n.Image.Route)
			if err != nil {
				return err
			}
			ok = k != nil
		}
		allowedImages[*n.Image] = ok
		b.Assets[n.ID] = service.CanvasAsset{Available: ok}
	}
	groups := map[int64]bool{}
	for _, j := range jobs {
		if j.KeyID == service.VideoStudioWalletSlot && j.UserID == u {
			groups[j.GroupID] = true
		}
	}
	wallet := map[int64]*service.APIKey{}
	if len(groups) > 0 {
		groupIDs := make([]int64, 0, len(groups))
		for id := range groups {
			groupIDs = append(groupIDs, id)
		}
		keys, err := h.keys.VideoStudioWalletGrants(ctx, u, groupIDs)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if k != nil && k.GroupID != nil {
				wallet[*k.GroupID] = k
			}
		}
	}
	visible := []service.VideoStudioJob{}
	for _, j := range jobs {
		if j.UserID != u {
			continue
		}
		var k *service.APIKey
		if j.KeyID == service.VideoStudioWalletSlot {
			k = wallet[j.GroupID]
			if k == nil || !h.media.StudioModelGroup(k.Group, j.Model) {
				continue
			}
		} else {
			k, e = keyFor(j.KeyID)
			if e != nil {
				return e
			}
		}
		if matchesStudioGrant(k, j) {
			visible = append(visible, j.Visible(time.Now()))
		}
	}
	if len(visible) > 0 {
		h.media.MarkVideoStudioPosters(ctx, visible)
	}
	denied := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		denied[j.ID] = true
	}
	lookup := map[string]*service.VideoStudioJob{}
	for i := range visible {
		lookup[visible[i].ID] = &visible[i]
		delete(denied, visible[i].ID)
	}
	playable := func(j *service.VideoStudioJob) bool { return j != nil && j.Status == "completed" && j.Receipt != "" }
	for id, in := range b.Inputs {
		switch {
		case in.Image != nil:
			in.Available = allowedImages[*in.Image]
		case in.VideoID != "":
			in.Available = playable(lookup[in.VideoID])
		}
		b.Inputs[id] = in
	}
	for _, n := range b.Document.Nodes {
		if n.VideoID != "" {
			j := lookup[n.VideoID]
			b.Assets[n.ID] = service.CanvasAsset{Available: playable(j), Job: j}
		}
	}
	for i := range b.Attempts {
		a := &b.Attempts[i]
		a.Job = lookup[a.RequestID]
		if a.Job == nil {
			a.Unavailable = denied[a.RequestID]
		}
	}
	return nil
}
