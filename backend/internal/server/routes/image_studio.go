package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func registerImageStudioRoutes(authenticated *gin.RouterGroup, h *handler.Handlers, keys *service.APIKeyService, subscriptions *service.SubscriptionService, cfg *config.Config, settings *service.SettingService) {
	studio := authenticated.Group("/image-studio")
	studio.GET("/keys", h.APIKey.ListImageStudioKeys)
	studio.Use(middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize))
	studio.Use(middleware.ImageStudioKeyAuth(keys, subscriptions, cfg))
	studio.Use(middleware.GroupModelAllowlist())
	studio.Use(middleware.RequireGroupAssignment(settings, middleware.OpenAIErrorWriter))
	studio.POST("/batches", h.BatchImage.Submit)
	studio.GET("/batches", h.BatchImage.List)
	studio.GET("/batches/models", h.BatchImage.Models)
	studio.GET("/batches/:id", h.BatchImage.Get)
	studio.GET("/batches/:id/items", h.BatchImage.Items)
	studio.GET("/batches/:id/items/:custom_id/content", h.BatchImage.ItemContent)
	studio.GET("/batches/:id/result-files", h.BatchImage.ResultFiles)
	studio.GET("/batches/:id/download", h.BatchImage.Download)
	studio.POST("/batches/:id/cancel", h.BatchImage.Cancel)
}
