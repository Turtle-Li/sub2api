package routes

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// InternalHealth is deliberately narrow so the internal routes do not depend
// on application auth middleware or reveal dependency details.
type InternalHealth interface {
	Authorized(string) bool
	Live() bool
	Ready(context.Context) bool
}

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine, internalHealth InternalHealth) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/internal/livez", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if internalHealth == nil || !internalHealth.Authorized(c.GetHeader("X-Monitor-Token")) {
			c.JSON(http.StatusUnauthorized, gin.H{"live": false})
			return
		}
		if !internalHealth.Live() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"live": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"live": true})
	})

	r.GET("/internal/readyz", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if internalHealth == nil || !internalHealth.Authorized(c.GetHeader("X-Monitor-Token")) {
			c.JSON(http.StatusUnauthorized, gin.H{"ready": false})
			return
		}
		if !internalHealth.Ready(c.Request.Context()) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ready": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ready": true})
	})

	// Claude Code 遥测日志（忽略，直接返回200）
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})
}
