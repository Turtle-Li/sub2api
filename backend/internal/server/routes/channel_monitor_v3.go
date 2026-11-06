package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func channelMonitorModeV3Guard(settings *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settings == nil || !settings.GetChannelMonitorRuntime(c.Request.Context()).V3Active() {
			response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
			c.Abort()
			return
		}
		c.Next()
	}
}

func registerChannelMonitorV3PublicRoutes(v1 *gin.RouterGroup, h *handler.Handlers, settings *service.SettingService, limiter *middleware.PanelRateLimiter) {
	monitor := v1.Group("/public/channel-monitor-v3")
	monitor.Use(limiter.PublicIP(), middleware.BackendModeUserGuard(settings), channelMonitorModeV3Guard(settings))
	monitor.GET("/status", h.ChannelMonitorV3.PublicStatus)
	monitor.GET("/incidents", h.ChannelMonitorV3.PublicIncidents)
}

func registerChannelMonitorV3UserRoutes(auth *gin.RouterGroup, h *handler.Handlers, settings *service.SettingService, limiter *middleware.PanelRateLimiter) {
	monitor := auth.Group("/channel-monitor-v3")
	monitor.Use(limiter.Heavy(), channelMonitorModeV3Guard(settings))
	monitor.GET("/status", h.ChannelMonitorV3.Status)
	monitor.GET("/incidents", h.ChannelMonitorV3.Incidents)
}

func registerChannelMonitorV3AdminRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	monitor := admin.Group("/channel-monitor-v3")
	monitor.PUT("/mode", h.ChannelMonitorV3.SetMode)
	monitor.GET("/settings", h.ChannelMonitorV3.GetSettings)
	monitor.PUT("/config", h.ChannelMonitorV3.UpdateConfig)
	monitor.POST("/categories", h.ChannelMonitorV3.CreateCategory)
	monitor.PUT("/categories/:id", h.ChannelMonitorV3.UpdateCategory)
	monitor.DELETE("/categories/:id", h.ChannelMonitorV3.DeleteCategory)
	monitor.POST("/components", h.ChannelMonitorV3.CreateComponent)
	monitor.PUT("/components/:id", h.ChannelMonitorV3.UpdateComponent)
	monitor.DELETE("/components/:id", h.ChannelMonitorV3.DeleteComponent)
	monitor.POST("/reorder", h.ChannelMonitorV3.Reorder)
	monitor.GET("/status", h.ChannelMonitorV3.Status)
	monitor.GET("/incidents", h.ChannelMonitorV3.Incidents)
}
