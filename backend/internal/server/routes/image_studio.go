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
	if settings != nil {
		studio.GET("/keys", h.BatchImage.ImageStudioWalletDirectory(settings), h.APIKey.ListImageStudioKeys)
		wallet := studio.Group("/wallet", middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize))
		admit := h.BatchImage.ImageStudioWalletSession(settings, true)
		wallet.POST("/batches", admit, h.BatchImage.Submit)
		wallet.GET("/batches/models", admit, h.BatchImage.Models)
		registerImageStudioBatchHistory(wallet.Group("", h.BatchImage.ImageStudioWalletSession(settings, false)), h.BatchImage)
	} else {
		studio.GET("/keys", h.APIKey.ListImageStudioKeys)
	}
	legacy := studio.Group("", middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize),
		middleware.ImageStudioKeyAuth(keys, subscriptions, cfg), middleware.GroupModelAllowlist(),
		middleware.RequireGroupAssignment(settings, middleware.OpenAIErrorWriter))
	legacy.POST("/batches", h.BatchImage.Submit)
	legacy.GET("/batches/models", h.BatchImage.Models)
	registerImageStudioBatchHistory(legacy, h.BatchImage)
}

func registerImageStudioBatchHistory(group *gin.RouterGroup, batch *handler.BatchImageHandler) {
	group.GET("/batches", batch.List)
	group.GET("/batches/gallery", batch.Gallery)
	group.GET("/batches/:id", batch.Get)
	group.GET("/batches/:id/items", batch.Items)
	group.GET("/batches/:id/items/:custom_id/content", batch.ItemContent)
	group.GET("/batches/:id/items/:custom_id/thumbnail", batch.ItemThumbnail)
	group.GET("/batches/:id/result-files", batch.ResultFiles)
	group.GET("/batches/:id/download", batch.Download)
	group.POST("/batches/:id/cancel", batch.Cancel)
}
