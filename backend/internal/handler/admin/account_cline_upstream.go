package admin

import (
	"errors"
	"io"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Cline 上游钉住管理端接口：枚举/校验/钉住 Cline Pass 订阅模型背后的上游渠道。
// 配置与探测快照只落 account.Extra（cline_upstream_pins / cline_upstream_meta），
// 普通账号编辑不会覆盖（见 admin_account.go 的受管字段保留列表）。

type clineUpstreamProbeRequest struct {
	Models []string `json:"models"`
}

type clineUpstreamValidateRequest struct {
	Model string `json:"model" binding:"required"`
}

type clineUpstreamTestRequest struct {
	Model     string   `json:"model" binding:"required"`
	Mode      string   `json:"mode"`
	Upstreams []string `json:"upstreams"`
	Exclude   []string `json:"exclude"`
	Sort      string   `json:"sort"`
}

type clineUpstreamPinsRequest struct {
	Pins map[string]service.ClineUpstreamPin `json:"pins"`
}

func (h *AccountHandler) clineUpstreamService() (*service.ClineUpstreamService, bool) {
	if h == nil || h.clineUpstream == nil {
		return nil, false
	}
	return h.clineUpstream, true
}

func clineUpstreamAccountID(c *gin.Context) (int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return accountID, true
}

// GetClineUpstreams 返回账号的钉住配置、探测快照与可钉模型清单。
func (h *AccountHandler) GetClineUpstreams(c *gin.Context) {
	svc, ok := h.clineUpstreamService()
	if !ok {
		response.ErrorFrom(c, service.ErrClineUpstreamUnavailable)
		return
	}
	accountID, ok := clineUpstreamAccountID(c)
	if !ok {
		return
	}
	state, err := svc.GetState(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

// UpdateClineUpstreams 全量覆盖账号的每模型钉住配置。
func (h *AccountHandler) UpdateClineUpstreams(c *gin.Context) {
	svc, ok := h.clineUpstreamService()
	if !ok {
		response.ErrorFrom(c, service.ErrClineUpstreamUnavailable)
		return
	}
	accountID, ok := clineUpstreamAccountID(c)
	if !ok {
		return
	}
	var req clineUpstreamPinsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := svc.UpdatePins(c.Request.Context(), accountID, req.Pins); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	state, err := svc.GetState(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

// ProbeClineUpstreams 探测模型（默认全部订阅模型）并刷新渠道清单与管道类型。
func (h *AccountHandler) ProbeClineUpstreams(c *gin.Context) {
	svc, ok := h.clineUpstreamService()
	if !ok {
		response.ErrorFrom(c, service.ErrClineUpstreamUnavailable)
		return
	}
	accountID, ok := clineUpstreamAccountID(c)
	if !ok {
		return
	}
	var req clineUpstreamProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	results, err := svc.Probe(c.Request.Context(), accountID, req.Models)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": accountID, "results": results})
}

// ValidateClineUpstreams 把模型的每个渠道各钉一次，返回可用性地图。
func (h *AccountHandler) ValidateClineUpstreams(c *gin.Context) {
	svc, ok := h.clineUpstreamService()
	if !ok {
		response.ErrorFrom(c, service.ErrClineUpstreamUnavailable)
		return
	}
	accountID, ok := clineUpstreamAccountID(c)
	if !ok {
		return
	}
	var req clineUpstreamValidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := svc.Validate(c.Request.Context(), accountID, req.Model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// TestClineUpstreams 用临时钉住配置发一条真实请求，验证网关是否采纳。
func (h *AccountHandler) TestClineUpstreams(c *gin.Context) {
	svc, ok := h.clineUpstreamService()
	if !ok {
		response.ErrorFrom(c, service.ErrClineUpstreamUnavailable)
		return
	}
	accountID, ok := clineUpstreamAccountID(c)
	if !ok {
		return
	}
	var req clineUpstreamTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := svc.Test(c.Request.Context(), accountID, req.Model, service.ClineUpstreamPin{
		Mode:      req.Mode,
		Upstreams: req.Upstreams,
		Exclude:   req.Exclude,
		Sort:      req.Sort,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
