package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type updateDesktopConsoleSettingsRequest struct {
	ControlPlaneURL string                       `json:"control_plane_url"`
	Promotions      []service.DesktopPromotion   `json:"promotions"`
	UpdatePolicy    *service.DesktopUpdatePolicy `json:"update_policy"`
}

// GetDesktopConsoleSettings returns only settings owned by TT Switch.
// GET /api/v1/desktop-console/settings
func (h *SettingHandler) GetDesktopConsoleSettings(c *gin.Context) {
	settings, err := h.settingService.GetDesktopConsoleSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateDesktopConsoleSettings updates only settings owned by TT Switch.
// PUT /api/v1/desktop-console/settings
func (h *SettingHandler) UpdateDesktopConsoleSettings(c *gin.Context) {
	var request updateDesktopConsoleSettingsRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	updatePolicy := service.DefaultDesktopUpdatePolicy()
	if request.UpdatePolicy != nil {
		updatePolicy = *request.UpdatePolicy
	} else if current, getErr := h.settingService.GetDesktopUpdatePolicy(c.Request.Context()); getErr == nil {
		updatePolicy = current
	}

	settings, err := h.settingService.UpdateDesktopConsoleSettings(
		c.Request.Context(),
		service.DesktopConsoleSettings{
			ControlPlaneURL: request.ControlPlaneURL,
			Promotions:      request.Promotions,
			UpdatePolicy:    updatePolicy,
		},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// GetDesktopStorageSettings returns the TT Switch Tencent COS settings without
// ever returning the stored SecretKey.
// GET /api/v1/desktop-console/storage
func (h *SettingHandler) GetDesktopStorageSettings(c *gin.Context) {
	if h.desktopStorageService == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "desktop storage service is unavailable", "DESKTOP_STORAGE_UNAVAILABLE", nil)
		return
	}
	settings, err := h.desktopStorageService.Get(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateDesktopStorageSettings encrypts and saves the TT Switch Tencent COS
// credentials. An empty SecretKey keeps the existing value.
// PUT /api/v1/desktop-console/storage
func (h *SettingHandler) UpdateDesktopStorageSettings(c *gin.Context) {
	if h.desktopStorageService == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "desktop storage service is unavailable", "DESKTOP_STORAGE_UNAVAILABLE", nil)
		return
	}
	var request service.DesktopStorageSettings
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	settings, err := h.desktopStorageService.Update(c.Request.Context(), request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// TestDesktopStorageConnection verifies write, anonymous/public read, and
// delete permissions with one temporary object.
// POST /api/v1/desktop-console/storage/test
func (h *SettingHandler) TestDesktopStorageConnection(c *gin.Context) {
	if h.desktopStorageService == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "desktop storage service is unavailable", "DESKTOP_STORAGE_UNAVAILABLE", nil)
		return
	}
	var request service.DesktopStorageSettings
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	result, err := h.desktopStorageService.TestConnection(c.Request.Context(), request)
	if err != nil {
		response.Success(c, gin.H{"ok": false, "message": err.Error()})
		return
	}
	response.Success(c, gin.H{
		"ok":         true,
		"message":    "Tencent COS write/read/delete probe succeeded",
		"endpoint":   result.Endpoint,
		"object_key": result.ObjectKey,
	})
}
