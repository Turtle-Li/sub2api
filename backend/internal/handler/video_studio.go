package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const studioPreparedKey = "video_studio_dispatch"

type studioDispatch struct {
	job       service.VideoStudioJob
	body      []byte
	duplicate bool
	price     float64
	currency  string
}

func studioStop(c *gin.Context, status int, msg string) { c.Abort(); response.Error(c, status, msg) }
func (h *OpenAIGatewayHandler) studioAccess(c *gin.Context) (service.VideoStudioStore, int64) {
	s, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || s.UserID <= 0 {
		studioStop(c, 401, "请先登录")
		return nil, 0
	}
	store := h.apiKeyService.VideoStudioStore()
	if store == nil {
		studioStop(c, 503, "视频工作台暂不可用")
		return nil, 0
	}
	c.Header("Cache-Control", "private, no-store")
	return store, s.UserID
}
func (h *OpenAIGatewayHandler) studioModels(c *gin.Context, store service.VideoStudioStore, user int64) ([]service.VideoStudioModel, error) {
	ctx := c.Request.Context()
	ids, err := store.VideoStudioGroupIDs(ctx, user)
	if err != nil {
		return nil, err
	}
	if h.cfg != nil && h.cfg.Gateway.VideoStudioShareConfiguredGroups {
		for _, id := range []int64{h.cfg.Gateway.VideoStudioMuseGroupID, h.cfg.Gateway.VideoStudioH3GroupID} {
			if id > 0 && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	keys, err := h.apiKeyService.VideoStudioWalletGrants(ctx, user, ids)
	if err != nil {
		return nil, err
	}
	return h.gatewayService.VideoStudioModels(c.Request.Context(), keys)
}
func (h *OpenAIGatewayHandler) VideoStudioModels(c *gin.Context) {
	store, user := h.studioAccess(c)
	if store == nil {
		return
	}
	enabled := h.cfg != nil && h.cfg.Gateway.VideoStudioEnabled
	items := []service.VideoStudioModel{}
	if enabled {
		var err error
		items, err = h.studioModels(c, store, user)
		if err != nil {
			studioStop(c, 503, "视频模型目录暂不可用")
			return
		}
	}
	response.Success(c, gin.H{"enabled": enabled, "items": items})
}
func (h *OpenAIGatewayHandler) VideoStudioList(c *gin.Context) {
	store, user := h.studioAccess(c)
	if store == nil {
		return
	}
	cursor := c.Query("cursor")
	if cursor != "" && !validStudioID(cursor) {
		studioStop(c, 400, "无效的历史记录游标")
		return
	}
	const page = 24
	items, err := store.ListVideoStudioJobs(c.Request.Context(), user, cursor, page+1)
	if err != nil {
		studioStop(c, 503, "暂时无法读取视频历史")
		return
	}
	next := ""
	if len(items) > page {
		items = items[:page]
		next = items[page-1].ID
	}
	now := time.Now()
	for i := range items {
		items[i] = items[i].Visible(now)
	}
	h.gatewayService.MarkVideoStudioPosters(c.Request.Context(), items)
	response.Success(c, gin.H{"items": items, "next_cursor": next})
}
func (h *OpenAIGatewayHandler) PrepareVideoStudioSubmit(c *gin.Context) {
	store, user := h.studioAccess(c)
	if store == nil {
		return
	}
	typ, _, err := mime.ParseMediaType(c.ContentType())
	if err != nil || typ != "application/json" || c.GetHeader("Content-Encoding") != "" {
		studioStop(c, 415, "请提交 JSON 视频请求")
		return
	}
	ctx := c.Request.Context()
	raw, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if errors.Is(err, httputil.ErrRequestBodyMemoryBudget) {
		studioStop(c, 429, "参考图处理繁忙，本次未提交")
		return
	}
	if err != nil || len(raw) > VideoStudioBodyLimit {
		studioStop(c, 413, "视频请求过大或读取失败")
		return
	}
	// Reserve room for JSON fields, fingerprinting and the normalized provider request
	// before parsing. Keep it through dispatch, including any wait for a gateway slot.
	if err := httputil.ReserveRequestBodyMemory(ctx, int64(len(raw))*6); err != nil {
		studioStop(c, 429, "参考图处理繁忙，本次未提交")
		return
	}
	q, err := decodeStudioSubmission(raw)
	if err != nil {
		studioStop(c, 400, err.Error())
		return
	}
	fingerprint := q.fingerprint()
	existing, err := store.FindVideoStudioJob(ctx, user, q.ID)
	if err == nil {
		if existing.Fingerprint != fingerprint {
			studioStop(c, 409, "同一任务标识不能用于不同的请求")
			return
		}
		c.Set(studioPreparedKey, studioDispatch{job: existing, duplicate: true})
		c.Set(middleware.VideoStudioBoundReadKey, existing.KeyID)
		return
	}
	if !errors.Is(err, service.ErrVideoStudioNotFound) {
		studioStop(c, 503, "暂时无法核对本次提交，请勿重复生成")
		return
	}
	if h.cfg == nil || !h.cfg.Gateway.VideoStudioEnabled {
		studioStop(c, 403, "视频工作台已暂停新建任务")
		return
	}
	models, err := h.studioModels(c, store, user)
	if err != nil {
		studioStop(c, 503, "无法确认视频模型或价格")
		return
	}
	for _, m := range models {
		if m.ID != q.Model {
			continue
		}
		if q.Quote != m.QuoteID {
			studioStop(c, 409, "视频报价或通道已变化，请刷新并重新确认")
			return
		}
		body, err := q.providerBody(ctx, m)
		if errors.Is(err, httputil.ErrRequestBodyMemoryBudget) {
			studioStop(c, 429, "参考图处理繁忙，本次未提交")
			return
		}
		if err != nil {
			studioStop(c, 400, err.Error())
			return
		}
		j := service.VideoStudioJob{ID: q.ID, UserID: user, KeyID: m.KeyID, GroupID: m.GroupID, Model: q.Model, Seconds: q.Seconds, Size: q.Size, Fingerprint: fingerprint, Parameters: q.parameters()}
		c.Set(studioPreparedKey, studioDispatch{job: j, body: body, price: m.Quotes[q.Seconds], currency: m.Currency})
		return
	}
	studioStop(c, 403, "当前没有此视频模型的可用授权")
}
func (h *OpenAIGatewayHandler) PrepareVideoStudioRead(c *gin.Context) {
	store, user := h.studioAccess(c)
	if store == nil {
		return
	}
	id := c.Param("id")
	if !validStudioID(id) {
		studioStop(c, 404, "视频任务不存在")
		return
	}
	job, err := store.FindVideoStudioJob(c.Request.Context(), user, id)
	if errors.Is(err, service.ErrVideoStudioNotFound) {
		studioStop(c, 404, "视频任务不存在")
		return
	}
	if err != nil {
		studioStop(c, 503, "暂时无法读取视频任务")
		return
	}
	c.Set(studioPreparedKey, studioDispatch{job: job})
	c.Set(middleware.VideoStudioBoundReadKey, job.KeyID)
}

// VideoStudioBind selects credentials only from the server-resolved job. Never fall back
// from a legacy key to a wallet, or from a revoked wallet to a different group.
func (h *OpenAIGatewayHandler) VideoStudioBind(legacy gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, _ := c.Get(studioPreparedKey)
		p, ok := v.(studioDispatch)
		if !ok || p.job.KeyID != service.VideoStudioWalletSlot {
			legacy(c)
			return
		}
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID != p.job.UserID || p.job.GroupID <= 0 {
			studioStop(c, 404, "视频任务或原授权不可用")
			return
		}
		grants, err := h.apiKeyService.VideoStudioWalletGrants(c.Request.Context(), subject.UserID, []int64{p.job.GroupID})
		if err != nil {
			studioStop(c, 503, "无法核对视频授权")
			return
		}
		if len(grants) != 1 || !h.gatewayService.StudioModelGroup(grants[0].Group, p.job.Model) {
			studioStop(c, 404, "视频任务或原授权不可用")
			return
		}
		middleware.BindVideoStudioWallet(c, grants[0])
	}
}
func VideoStudioKeyID(c *gin.Context) (int64, error) {
	v, _ := c.Get(studioPreparedKey)
	p, ok := v.(studioDispatch)
	if !ok || p.job.KeyID <= 0 {
		return 0, errors.New("missing studio owner")
	}
	return p.job.KeyID, nil
}
func studioAuthorized(c *gin.Context) (studioDispatch, bool) {
	v, _ := c.Get(studioPreparedKey)
	p, ready := v.(studioDispatch)
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ready || !ok || !matchesStudioGrant(key, p.job) {
		studioStop(c, 404, "视频任务或原授权不可用")
		return p, false
	}
	return p, true
}

func matchesStudioGrant(key *service.APIKey, job service.VideoStudioJob) bool {
	if key == nil || key.GroupID == nil || key.Group == nil || key.ID != job.KeyID || key.UserID != job.UserID ||
		*key.GroupID != job.GroupID || key.Group.ID != job.GroupID {
		return false
	}
	g := key.Group
	if g.IsSubscriptionType() || !service.GroupAllowsImageGeneration(g) || !slices.Contains(g.ModelAllowlist.Models, job.Model) {
		return false
	}
	if job.Model == service.MuseVideoModel {
		return (key.ID == service.VideoStudioWalletSlot || g.IsExclusive) && g.IsMuseMediaOnly()
	}
	return service.IsGoodPutVideoModel(job.Model) && service.IsGoodPutVideoGroup(g)
}

func (h *OpenAIGatewayHandler) VideoStudioSubmit(c *gin.Context) {
	p, ok := studioAuthorized(c)
	if !ok {
		return
	}
	store := h.apiKeyService.VideoStudioStore()
	ctx := c.Request.Context()
	if p.duplicate {
		response.Success(c, h.studioView(c, p.job))
		return
	}
	claimed, err := store.ClaimVideoStudioJob(ctx, p.job)
	if err != nil {
		studioStop(c, 503, "未能确认任务记录，请先查询本次任务")
		return
	}
	if !claimed {
		current, err := store.FindVideoStudioJob(ctx, p.job.UserID, p.job.ID)
		if err != nil || current.Fingerprint != p.job.Fingerprint {
			studioStop(c, 409, "提交标识冲突，请核对任务")
			return
		}
		response.Success(c, h.studioView(c, current))
		return
	}
	requestmodel.ResetRequestBody(c.Request, p.body)
	posterCtx, source := service.WatchStudioPosterSource(service.WithVideoStudioSubmission(ctx, p.job.ID, p.price, p.currency))
	c.Request = c.Request.WithContext(posterCtx)
	setActualUpstreamEndpoint(c, EndpointVideos)
	ep := service.MuseVideoCreate
	if service.IsGoodPutVideoModel(p.job.Model) {
		ep = service.GoodPutVideoCreate
	}
	r := studioJSON(c, func() { h.handleGrokMedia(c, ep, "") })
	receipt, state, review := studioCreateOutcome(p.job.Model, r)
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.UpdateVideoStudioJob(save, p.job.UserID, p.job.ID, receipt, state, review); err != nil {
		studioStop(c, 503, "生成结果待核对，请查询原任务，不要重新提交")
		return
	}
	j, err := store.FindVideoStudioJob(save, p.job.UserID, p.job.ID)
	if err != nil {
		studioStop(c, 503, "请查询原任务，不要重新提交")
		return
	}
	if state == "failed" && r.status >= 400 {
		studioStop(c, r.status, studioFailureMessage(r.status, r.errorKind()))
		return
	}
	if ms := h.gatewayService.CaptureVideoStudioPoster(ctx, j, source, store); ms > 0 {
		j.ActualDurationMS = ms
	}
	response.Accepted(c, h.studioView(c, j))
}
func studioFailureMessage(status int, code string) string {
	switch {
	case code == "studio_quote_changed":
		return "报价已变化，请刷新模型后重新确认"
	case code == "goodput_video_price_unavailable" || code == "muse_video_price_unavailable":
		return "通道暂时无法确认视频价格，未向渠道生成视频"
	case status == 429:
		return "当前通道已达到限额或已有进行中的任务，请查看原任务"
	case status == 402:
		return "余额或额度不足，未能开始本次生成"
	case status == 403:
		return "本次生成未通过权限或内容校验"
	default:
		return "本次生成请求被拒绝，请核对参数或权限"
	}
}
func (h *OpenAIGatewayHandler) VideoStudioGet(c *gin.Context) {
	p, ok := studioAuthorized(c)
	if !ok {
		return
	}
	j := p.job.Visible(time.Now())
	if j.Receipt == "" || j.Status == "expired" || j.Status == "failed" || (j.Status == "completed" && !j.BillingReview && j.Model != service.MuseVideoModel) {
		response.Success(c, h.studioView(c, j))
		return
	}
	posterCtx, source := service.WatchStudioPosterSource(c.Request.Context())
	c.Request = c.Request.WithContext(posterCtx)
	ep := service.MuseVideoStatus
	if service.IsGoodPutVideoModel(j.Model) {
		ep = service.GoodPutVideoStatus
	}
	setActualUpstreamEndpoint(c, EndpointVideos)
	r := studioJSON(c, func() { h.handleGrokMedia(c, ep, j.Receipt) })
	state, review := studioReceiptState(j.Model, j.Receipt, r)
	if state == "" {
		if r.status == 404 && time.Since(j.CreatedAt) >= 24*time.Hour {
			state = "expired"
		} else {
			if r.status < 400 {
				r.status = 502
			}
			studioStop(c, r.status, "暂时无法读取原视频任务，请勿重新提交")
			return
		}
	}
	store := h.apiKeyService.VideoStudioStore()
	if err := store.UpdateVideoStudioJob(c.Request.Context(), j.UserID, j.ID, j.Receipt, state, review); err != nil {
		studioStop(c, 503, "暂时无法更新视频记录")
		return
	}
	current, err := store.FindVideoStudioJob(c.Request.Context(), j.UserID, j.ID)
	if err != nil {
		studioStop(c, 503, "暂时无法读取视频记录")
		return
	}
	if ms := h.gatewayService.CaptureVideoStudioPoster(c.Request.Context(), current, source, store); ms > 0 {
		current.ActualDurationMS = ms
	}
	response.Success(c, h.studioView(c, current))
}
func (h *OpenAIGatewayHandler) VideoStudioContent(c *gin.Context) {
	p, ok := studioAuthorized(c)
	if !ok {
		return
	}
	j := p.job.Visible(time.Now())
	if j.Status == "expired" {
		studioStop(c, 410, "视频已过期")
		return
	}
	if j.Receipt == "" {
		studioStop(c, 409, "视频尚不可读取")
		return
	}
	ep := service.MuseVideoContent
	if service.IsGoodPutVideoModel(j.Model) {
		ep = service.GoodPutVideoContent
	}
	setActualUpstreamEndpoint(c, EndpointVideos)
	h.handleGrokMedia(c, ep, j.Receipt)
}

// Only create/status return small JSON receipts. Content always streams through the original writer.
type studioBuffer struct {
	gin.ResponseWriter
	header   http.Header
	buf      bytes.Buffer
	status   int
	overflow bool
}

func (w *studioBuffer) Header() http.Header  { return w.header }
func (w *studioBuffer) WriteHeader(code int) { w.status = code }
func (w *studioBuffer) WriteHeaderNow()      {}
func (w *studioBuffer) Write(b []byte) (int, error) {
	if len(b) > 64<<10-w.buf.Len() {
		w.overflow = true
		return len(b), nil
	}
	return w.buf.Write(b)
}
func (w *studioBuffer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *studioBuffer) Size() int                         { return w.buf.Len() }
func (w *studioBuffer) Status() int                       { return w.status }
func (w *studioBuffer) Written() bool                     { return w.buf.Len() > 0 }
func (w *studioBuffer) Flush()                            {}

type studioResult struct {
	status  int
	payload struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Usage  struct {
			Seconds int `json:"duration_seconds"`
		} `json:"usage"`
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
}

func (r studioResult) errorKind() string {
	if r.payload.Error.Type != "" {
		return r.payload.Error.Type
	}
	return r.payload.Error.Code
}

func studioJSON(c *gin.Context, run func()) studioResult {
	orig := c.Writer
	w := &studioBuffer{ResponseWriter: orig, header: make(http.Header), status: 200}
	c.Writer = w
	defer func() { c.Writer = orig }()
	run()
	r := studioResult{status: w.status}
	if !w.overflow {
		_ = json.Unmarshal(w.buf.Bytes(), &r.payload)
	}
	return r
}
func studioReceiptState(model, id string, r studioResult) (string, bool) {
	if r.status < 200 || r.status >= 300 || id == "" || r.payload.ID != id || (service.IsGoodPutVideoModel(model) && !service.IsGoodPutVideoTaskID(id)) || (model == service.MuseVideoModel && !service.IsMuseVideoTaskID(id)) {
		return "", false
	}
	switch r.payload.Status {
	case "queued":
		return "queued", false
	case "processing", "in_progress", "running":
		return "running", false
	case "completed":
		return "completed", service.IsGoodPutVideoModel(model) && r.payload.Usage.Seconds <= 0
	case "failed":
		return "failed", false
	}
	return "", false
}
func studioCreateOutcome(model string, r studioResult) (string, string, bool) {
	if state, review := studioReceiptState(model, r.payload.ID, r); state != "" {
		if model == service.MuseVideoModel && state == "completed" {
			state = "queued"
		}
		return r.payload.ID, state, review
	}
	// These errors are issued locally before the generation POST. Never infer
	// rejection from a generic upstream 5xx, malformed receipt, or timeout.
	switch r.errorKind() {
	case "studio_wallet_unavailable", "goodput_video_price_unavailable", "goodput_video_unavailable", "goodput_video_no_eligible_account", "muse_video_price_unavailable", "muse_video_no_eligible_account":
		return "", "failed", false
	}
	switch r.status {
	case 400, 401, 402, 403, 404, 409, 413, 415, 422, 429:
		return "", "failed", false
	}
	return "", "uncertain", false
}
