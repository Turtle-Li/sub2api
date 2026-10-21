package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetPaidFallbackSettings(c *gin.Context) {
	settings, err := h.settingService.GetPaidFallbackSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdatePaidFallbackSettings(c *gin.Context) {
	var settings service.PaidFallbackSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	if err := h.settingService.UpdatePaidFallbackSettings(c.Request.Context(), &settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, settings)
}

func (h *AccountHandler) SetPaidFallback(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var request struct {
		Enabled *bool `json:"enabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "enabled must be a boolean")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if *request.Enabled && !account.IsOpenAICompatible() {
		response.BadRequest(c, "Paid fallback requires an OpenAI-compatible account")
		return
	}
	if err := h.adminService.UpdateAccountExtra(c.Request.Context(), id, map[string]any{service.PaidFallbackExtraKey: *request.Enabled}); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": id, "enabled": *request.Enabled})
}
