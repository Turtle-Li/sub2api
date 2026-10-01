package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// codexDegradationProbeStore 是降智测试服务的最小依赖，便于测试替换。
type codexDegradationProbeStore interface {
	Create(ctx context.Context, req service.CodexDegradationProbeRequest) ([]service.CodexDegradationProbeResult, error)
	List(ctx context.Context) ([]service.CodexDegradationProbeResult, error)
	Get(ctx context.Context, id int64) (*service.CodexDegradationProbeResult, error)
	Delete(ctx context.Context, id int64) error
	DeleteAll(ctx context.Context) (int64, error)
}

// CodexDegradationProbeHandler 提供降智测试（固定 turn-state 票据路径）的管理接口。
type CodexDegradationProbeHandler struct {
	probes codexDegradationProbeStore
}

func NewCodexDegradationProbeHandler(probes *service.CodexDegradationProbeService) *CodexDegradationProbeHandler {
	return &CodexDegradationProbeHandler{probes: probes}
}

// List GET /api/v1/admin/codex-degradation-probes
func (h *CodexDegradationProbeHandler) List(c *gin.Context) {
	results, err := h.probes.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, results)
}

// Create POST /api/v1/admin/codex-degradation-probes
func (h *CodexDegradationProbeHandler) Create(c *gin.Context) {
	var req service.CodexDegradationProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	results, err := h.probes.Create(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, results)
}

// Get GET /api/v1/admin/codex-degradation-probes/:id
func (h *CodexDegradationProbeHandler) Get(c *gin.Context) {
	id, ok := parseCodexDegradationProbeID(c)
	if !ok {
		return
	}
	result, err := h.probes.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// Delete DELETE /api/v1/admin/codex-degradation-probes/:id
func (h *CodexDegradationProbeHandler) Delete(c *gin.Context) {
	id, ok := parseCodexDegradationProbeID(c)
	if !ok {
		return
	}
	if err := h.probes.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// DeleteAll DELETE /api/v1/admin/codex-degradation-probes
func (h *CodexDegradationProbeHandler) DeleteAll(c *gin.Context) {
	deleted, err := h.probes.DeleteAll(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": deleted})
}

func parseCodexDegradationProbeID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid probe ID")
		return 0, false
	}
	return id, true
}
