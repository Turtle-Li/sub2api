package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AttachmentGatewayHandler manages only the gateway's independent R2
// settings. Runtime rollout gates remain in static Attachment Gateway config.
type AttachmentGatewayHandler struct {
	r2Service *service.AttachmentR2Service
}

func NewAttachmentGatewayHandler(r2Service *service.AttachmentR2Service) *AttachmentGatewayHandler {
	return &AttachmentGatewayHandler{r2Service: r2Service}
}

func (h *AttachmentGatewayHandler) GetR2Config(c *gin.Context) {
	cfg, err := h.r2Service.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *AttachmentGatewayHandler) UpdateR2Config(c *gin.Context) {
	var req service.AttachmentR2Config
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	cfg, err := h.r2Service.UpdateConfig(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *AttachmentGatewayHandler) TestR2Connection(c *gin.Context) {
	var req service.AttachmentR2Config
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.r2Service.TestConnection(c.Request.Context(), req); err != nil {
		response.Success(c, gin.H{"ok": false, "message": err.Error()})
		return
	}
	response.Success(c, gin.H{
		"ok":      true,
		"message": "R2 object write/read/delete probe successful",
	})
}
