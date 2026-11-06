package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ChannelMonitorV3Handler) CreateCategory(c *gin.Context) {
	var input service.ChannelMonitorV3CategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid category")
		return
	}
	category, err := h.service.CreateCategory(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, category)
}

func (h *ChannelMonitorV3Handler) UpdateCategory(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	var input service.ChannelMonitorV3CategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid category")
		return
	}
	category, err := h.service.UpdateCategory(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, category)
}

func (h *ChannelMonitorV3Handler) DeleteCategory(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteCategory(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *ChannelMonitorV3Handler) CreateComponent(c *gin.Context) {
	var input service.ChannelMonitorV3ComponentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid component")
		return
	}
	component, err := h.service.CreateComponent(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, component)
}

func (h *ChannelMonitorV3Handler) UpdateComponent(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	var input service.ChannelMonitorV3ComponentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid component")
		return
	}
	component, err := h.service.UpdateComponent(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, component)
}

func (h *ChannelMonitorV3Handler) DeleteComponent(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteComponent(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *ChannelMonitorV3Handler) Reorder(c *gin.Context) {
	var input struct {
		CategoryIDs  []int64 `json:"category_ids"`
		ComponentIDs []int64 `json:"component_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid order")
		return
	}
	if err := h.service.Reorder(c.Request.Context(), input.CategoryIDs, input.ComponentIDs); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

// SetMode switches the site between V1, V2 and V3 without a full settings save.
func (h *ChannelMonitorV3Handler) SetMode(c *gin.Context) {
	var input struct {
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid mode")
		return
	}
	if h.settings == nil {
		response.Error(c, http.StatusInternalServerError, "settings unavailable")
		return
	}
	mode, err := h.settings.SetChannelMonitorMode(c.Request.Context(), input.Mode)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"mode": mode})
}

func channelMonitorV3ID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}
