package handler

import (
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) studioView(c *gin.Context, j service.VideoStudioJob) service.VideoStudioJob {
	items := []service.VideoStudioJob{j.Visible(time.Now())}
	h.gatewayService.MarkVideoStudioPosters(c.Request.Context(), items)
	return items[0]
}
func (h *OpenAIGatewayHandler) VideoStudioPoster(c *gin.Context) {
	p, ok := studioAuthorized(c)
	if !ok {
		return
	}
	j := p.job.Visible(time.Now())
	if j.Status == "expired" {
		studioStop(c, 410, "视频已过期")
		return
	}
	key, _ := middleware.GetAPIKeyFromContext(c)
	b, err := h.gatewayService.ReadVideoStudioPoster(c.Request.Context(), j, key)
	if errors.Is(err, service.ErrVideoStudioNotFound) {
		studioStop(c, 404, "暂无可读取的成片封面")
		return
	}
	if err != nil {
		studioStop(c, 503, "封面暂时无法读取")
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(200, "image/jpeg", b)
}
