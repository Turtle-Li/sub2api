package admin

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// codexGatewayHunterStore 是满血网关监控服务的最小依赖，便于测试替换。
type codexGatewayHunterStore interface {
	State(ctx context.Context) (*service.CodexGatewayHunterState, error)
	UpdateConfig(ctx context.Context, cfg service.CodexGatewayHunterConfig) (service.CodexGatewayHunterConfig, error)
	SetEnabled(ctx context.Context, enabled bool) (*service.CodexGatewayHunterState, error)
}

// CodexGatewayHunterHandler 提供满血网关监控的管理接口。
type CodexGatewayHunterHandler struct {
	hunter codexGatewayHunterStore
}

func NewCodexGatewayHunterHandler(hunter *service.CodexGatewayHunterService) *CodexGatewayHunterHandler {
	return &CodexGatewayHunterHandler{hunter: hunter}
}

// State GET /api/v1/admin/codex-gateway-hunter/state
func (h *CodexGatewayHunterHandler) State(c *gin.Context) {
	state, err := h.hunter.State(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

// UpdateConfig PUT /api/v1/admin/codex-gateway-hunter/config
func (h *CodexGatewayHunterHandler) UpdateConfig(c *gin.Context) {
	var req service.CodexGatewayHunterConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	cfg, err := h.hunter.UpdateConfig(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// Start POST /api/v1/admin/codex-gateway-hunter/start
func (h *CodexGatewayHunterHandler) Start(c *gin.Context) {
	h.setEnabled(c, true)
}

// Stop POST /api/v1/admin/codex-gateway-hunter/stop
func (h *CodexGatewayHunterHandler) Stop(c *gin.Context) {
	h.setEnabled(c, false)
}

func (h *CodexGatewayHunterHandler) setEnabled(c *gin.Context, enabled bool) {
	state, err := h.hunter.SetEnabled(c.Request.Context(), enabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}
