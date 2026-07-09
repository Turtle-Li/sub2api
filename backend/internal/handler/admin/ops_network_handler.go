package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetNetworkBandwidthSummary returns host network ingress/egress bandwidth.
// GET /api/v1/admin/ops/network/summary
func (h *OpsHandler) GetNetworkBandwidthSummary(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !h.opsService.IsRealtimeMonitoringEnabled(c.Request.Context()) {
		response.Success(c, gin.H{
			"enabled": false,
			"summary": gin.H{
				"enabled": false,
				"status":  "disabled",
			},
		})
		return
	}
	summary, err := h.opsService.GetNetworkBandwidthSummary(c.Request.Context())
	if err != nil {
		if isOpsRealtimeRequestCanceled(c, err) {
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"enabled": true,
		"summary": summary,
	})
}

// GetNetworkBandwidthSettings returns host network bandwidth monitor settings.
// GET /api/v1/admin/ops/network/settings
func (h *OpsHandler) GetNetworkBandwidthSettings(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	cfg, err := h.opsService.GetNetworkBandwidthSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// GetNetworkInterfaces returns detected host network interfaces for bandwidth monitoring.
// GET /api/v1/admin/ops/network/interfaces
func (h *OpsHandler) GetNetworkInterfaces(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	interfaces, defaultIface, err := h.opsService.GetNetworkInterfaces(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"interfaces":    interfaces,
		"default_iface": defaultIface,
	})
}

// UpdateNetworkBandwidthSettings updates host network bandwidth monitor settings.
// PUT /api/v1/admin/ops/network/settings
func (h *OpsHandler) UpdateNetworkBandwidthSettings(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req service.OpsNetworkBandwidthSettingsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	cfg, err := h.opsService.UpdateNetworkBandwidthSettings(c.Request.Context(), &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}
