package routes

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// InternalHealth is deliberately narrow so the internal routes do not depend
// on application auth middleware or reveal dependency details.
type InternalHealth interface {
	Authorized(string) bool
	Live() bool
	Ready(context.Context) bool
}

type internalPaymentRefundRollbackReadiness interface {
	RefundRollbackReadiness(context.Context) (service.PaymentRefundRollbackReadiness, error)
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
		payload := gin.H{"live": true}
		if traffic, ok := internalHealth.(interface{ InFlightRequests() int64 }); ok {
			payload["in_flight_requests"] = traffic.InFlightRequests()
		}
		c.JSON(http.StatusOK, payload)
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

	// This is intentionally separate from generic readiness. A non-zero count
	// means a rollback could strand an already-reserved subscription or wallet
	// entitlement; callers must wait for reconciliation or resolve it manually.
	r.GET("/internal/refund-rollback-readiness", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if internalHealth == nil || !internalHealth.Authorized(c.GetHeader("X-Monitor-Token")) {
			c.JSON(http.StatusUnauthorized, gin.H{"ready": false})
			return
		}
		readiness, ok := internalHealth.(internalPaymentRefundRollbackReadiness)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, service.PaymentRefundRollbackReadiness{
				EntitlementReservedReviewedPendingCount: -1,
			})
			return
		}
		result, err := readiness.RefundRollbackReadiness(c.Request.Context())
		if err != nil {
			// Do not expose database or provider failure details through a route
			// designed for release automation. -1 is the documented fail-closed
			// count sentinel.
			c.JSON(http.StatusServiceUnavailable, service.PaymentRefundRollbackReadiness{
				EntitlementReservedReviewedPendingCount: -1,
			})
			return
		}
		if !result.Ready || result.EntitlementReservedReviewedPendingCount != 0 {
			result.Ready = false
			c.JSON(http.StatusServiceUnavailable, result)
			return
		}
		c.JSON(http.StatusOK, result)
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
