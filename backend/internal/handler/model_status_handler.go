package handler

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type modelStatusGroup struct {
	ID           int64                                `json:"id"`
	Name         string                               `json:"name"`
	Platform     string                               `json:"platform"`
	Availability *service.ChannelMonitorV3GroupStatus `json:"availability"`
}

type modelStatusResponse struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Groups      []modelStatusGroup `json:"groups"`
}

// GetStatus publishes only selected active public groups, independently of prices.
func (h *ModelPlazaHandler) GetStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.monitorService == nil || h.settingService == nil || !h.settingService.GetChannelMonitorRuntime(c.Request.Context()).V3Active() {
		response.NotFound(c, "Model status is not enabled")
		return
	}
	_, authed := middleware.GetAuthSubjectFromContext(c)
	groups, err := h.monitorService.PublicStatusGroups(c.Request.Context(), !authed)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	ids := make([]int64, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	statuses, err := h.monitorService.PlazaStatuses(c.Request.Context(), ids, !authed)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Writer.Header().Add("Vary", "Accept-Language")
	response.Success(c, modelStatusResponse{GeneratedAt: time.Now().UTC(), Groups: toModelStatusGroups(groups, statuses, c.GetHeader("Accept-Language"))})
}

func toModelStatusGroups(groups []service.Group, statuses map[int64]*service.ChannelMonitorV3GroupStatus, language string) []modelStatusGroup {
	out := make([]modelStatusGroup, 0, len(groups))
	for _, group := range groups {
		name, _ := group.I18n.Localize(language, group.Name, group.Description)
		if status := statuses[group.ID]; status != nil && status.DisplayName != "" {
			name = status.DisplayName
		}
		out = append(out, modelStatusGroup{ID: group.ID, Name: name, Platform: group.Platform, Availability: statuses[group.ID]})
	}
	return out
}
