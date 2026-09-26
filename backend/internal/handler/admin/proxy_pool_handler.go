package admin

import (
	"strconv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ProxyPoolHandler manages proxy pools used for future automatic account imports.
type ProxyPoolHandler struct {
	poolService *service.ProxyPoolService
}

func NewProxyPoolHandler(poolService *service.ProxyPoolService) *ProxyPoolHandler {
	return &ProxyPoolHandler{poolService: poolService}
}

type proxyPoolResponse struct {
	ID        int64                  `json:"id"`
	Name      string                 `json:"name"`
	Notes     *string                `json:"notes"`
	Stats     service.ProxyPoolStats `json:"stats"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

type createProxyPoolRequest struct {
	Name  string  `json:"name" binding:"required"`
	Notes *string `json:"notes"`
}

type updateProxyPoolRequest struct {
	Name  *string `json:"name"`
	Notes *string `json:"notes"`
}

type proxyPoolMembersRequest struct {
	ProxyIDs []int64 `json:"proxy_ids" binding:"required"`
}

func toProxyPoolResponse(summary service.ProxyPoolSummary) proxyPoolResponse {
	return proxyPoolResponse{
		ID: summary.Pool.ID, Name: summary.Pool.Name, Notes: summary.Pool.Notes,
		Stats: summary.Stats, CreatedAt: summary.Pool.CreatedAt, UpdatedAt: summary.Pool.UpdatedAt,
	}
}

func parseProxyPoolID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_PROXY_POOL_ID", "invalid proxy pool id"))
		return 0, false
	}
	return id, true
}

func (h *ProxyPoolHandler) List(c *gin.Context) {
	pools, err := h.poolService.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]proxyPoolResponse, len(pools))
	for i := range pools {
		out[i] = toProxyPoolResponse(pools[i])
	}
	response.Success(c, out)
}

func (h *ProxyPoolHandler) Get(c *gin.Context) {
	id, ok := parseProxyPoolID(c)
	if !ok {
		return
	}
	summary, err := h.poolService.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toProxyPoolResponse(*summary))
}

func (h *ProxyPoolHandler) Create(c *gin.Context) {
	var req createProxyPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool, err := h.poolService.Create(c.Request.Context(), req.Name, req.Notes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toProxyPoolResponse(service.ProxyPoolSummary{Pool: *pool}))
}

func (h *ProxyPoolHandler) Update(c *gin.Context) {
	id, ok := parseProxyPoolID(c)
	if !ok {
		return
	}
	var req updateProxyPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool, err := h.poolService.Update(c.Request.Context(), id, req.Name, req.Notes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toProxyPoolResponse(service.ProxyPoolSummary{Pool: *pool}))
}

func (h *ProxyPoolHandler) Delete(c *gin.Context) {
	id, ok := parseProxyPoolID(c)
	if !ok {
		return
	}
	if err := h.poolService.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *ProxyPoolHandler) AddMembers(c *gin.Context) {
	id, ok := parseProxyPoolID(c)
	if !ok {
		return
	}
	var req proxyPoolMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	affected, err := h.poolService.AssignProxies(c.Request.Context(), id, req.ProxyIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"affected": affected})
}

func (h *ProxyPoolHandler) RemoveMembers(c *gin.Context) {
	var req proxyPoolMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	affected, err := h.poolService.ReleaseProxies(c.Request.Context(), req.ProxyIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"affected": affected})
}

func (h *ProxyPoolHandler) ListProxyIDs(c *gin.Context) {
	id, ok := parseProxyPoolID(c)
	if !ok {
		return
	}
	ids, err := h.poolService.ListProxyIDs(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"proxy_ids": ids})
}
