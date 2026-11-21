package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerCreativeCanvasRoutes(auth *gin.RouterGroup, h *handler.CreativeCanvasHandler) {
	g := auth.Group("/creative-canvases")
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:id", h.Get)
	g.PUT("/:id", h.Put)
	g.DELETE("/:id", h.Delete)
	g.POST("/:id/nodes/:node/attempts", h.Bind)
}
