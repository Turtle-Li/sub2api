package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func shopNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Next()
}

// Secret management always requires a human JWT, including when optional
// step-up is disabled. Legacy administrator API keys are not admitted.
func shopAdminJWT(c *gin.Context) {
	if c.GetHeader("x-api-key") != "" || c.GetString("auth_method") == service.AuditAuthMethodAdminAPIKey {
		response.Forbidden(c, "商城管理需要管理员登录")
		c.Abort()
		return
	}
	c.Next()
}

func RegisterShopRoutes(v1 *gin.RouterGroup, h *handler.ShopHandler, jwt middleware.JWTAuthMiddleware, admin middleware.AdminAuthMiddleware, audit middleware.AuditLogMiddleware, settings *service.SettingService, limits *middleware.PanelRateLimiter) {
	user := v1.Group("/shop")
	user.Use(shopNoStore, gin.HandlerFunc(jwt), middleware.BackendModeUserGuard(settings), limits.Global())
	user.GET("/products", h.Products)
	user.GET("/products/:id", h.Product)
	user.POST("/orders", h.CreateOrder)
	user.GET("/orders/:id", h.Order)
	user.POST("/orders/:id/input", h.SubmitInput)
	user.POST("/orders/:id/input/check", h.CheckInput)
	ops := v1.Group("/admin/shop")
	ops.Use(shopNoStore, shopAdminJWT, gin.HandlerFunc(admin), limits.Global(), gin.HandlerFunc(audit), middleware.AdminComplianceGuard(settings))
	ops.GET("/products", h.AdminProducts)
	ops.GET("/action-integrations", h.ActionIntegrations)
	ops.POST("/products", h.SaveProduct)
	ops.PUT("/products/:id", h.SaveProduct)
	ops.GET("/orders", h.Orders)
	ops.GET("/orders/:id", h.AdminOrder)
	ops.POST("/orders/:id/input/reveal", h.RevealInput)
	ops.POST("/orders/:id/action", h.Action)
	ops.POST("/orders/:id/automation", h.ManageAutomation)
	ops.POST("/orders/:id/notifications/retry", h.RetryNotifications)
}
