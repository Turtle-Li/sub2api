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

// GetStatus uses the original plaza visibility boundary and a price-free DTO.
func (h *ModelPlazaHandler) GetStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.monitorService == nil || h.settingService == nil || !h.settingService.GetChannelMonitorRuntime(c.Request.Context()).V3Active() {
		response.NotFound(c, "Model status is not enabled")
		return
	}
	rt := h.settingService.GetModelPlazaRuntime(c.Request.Context())
	if !rt.Enabled {
		response.NotFound(c, "Model plaza is not enabled")
		return
	}
	subject, authed := middleware.GetAuthSubjectFromContext(c)
	if rt.RequireAuth && !authed {
		response.Unauthorized(c, "Authentication required")
		return
	}
	groups, err := h.plazaService.ListGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var allowed map[int64]struct{}
	var restricted bool
	if authed {
		allowed, restricted, err = h.apiKeyService.GetUserGroupVisibility(c.Request.Context(), subject.UserID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	visible := filterPlazaVisibleGroups(groups, allowed, restricted)
	ids := make([]int64, 0, len(visible))
	for _, group := range visible {
		ids = append(ids, group.ID)
	}
	statuses, err := h.monitorService.PlazaStatuses(c.Request.Context(), ids, !authed)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !authed && len(statuses) == 0 {
		response.NotFound(c, "Model status publication is not enabled")
		return
	}
	c.Writer.Header().Add("Vary", "Accept-Language")
	response.Success(c, modelStatusResponse{GeneratedAt: time.Now().UTC(), Groups: toModelStatusGroups(visible, statuses, c.GetHeader("Accept-Language"))})
}

func toModelStatusGroups(groups []service.PlazaGroup, statuses map[int64]*service.ChannelMonitorV3GroupStatus, language string) []modelStatusGroup {
	out := make([]modelStatusGroup, 0, len(groups))
	for _, group := range groups {
		name, _ := group.I18n.Localize(language, group.Name, group.Description)
		out = append(out, modelStatusGroup{ID: group.ID, Name: name, Platform: group.Platform, Availability: statuses[group.ID]})
	}
	return out
}
