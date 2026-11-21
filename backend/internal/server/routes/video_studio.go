package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func registerVideoStudioRoutes(auth *gin.RouterGroup, h *handler.Handlers, keys *service.APIKeyService, subs *service.SubscriptionService, cfg *config.Config, settings *service.SettingService) {
	v := auth.Group("/video-studio")
	g := h.OpenAIGateway
	bind := g.VideoStudioBind(middleware.OwnedSessionKeyAuth(keys, subs, cfg, handler.VideoStudioKeyID))
	group := middleware.RequireGroupAssignment(settings, middleware.OpenAIErrorWriter)
	v.GET("/models", g.VideoStudioModels)
	v.GET("/jobs", g.VideoStudioList)
	// Session routes do not inherit the public media ingress memory budget.
	limit := int64(512 << 20)
	if cfg != nil && cfg.Gateway.RequestBodyMemoryBudgetBytes > 0 {
		limit = min(limit, cfg.Gateway.RequestBodyMemoryBudgetBytes)
	}
	v.POST("/jobs", middleware.RequestBodyLimit(handler.VideoStudioBodyLimit, httputil.NewBodyMemoryBudget(limit)), g.PrepareVideoStudioSubmit, bind, group, g.VideoStudioSubmit)
	v.GET("/jobs/:id", g.PrepareVideoStudioRead, bind, group, g.VideoStudioGet)
	v.GET("/jobs/:id/poster", g.PrepareVideoStudioRead, bind, group, g.VideoStudioPoster)
	v.GET("/jobs/:id/content", g.PrepareVideoStudioRead, bind, group, g.VideoStudioContent)
}
