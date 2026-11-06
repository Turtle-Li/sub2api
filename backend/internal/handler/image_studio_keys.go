package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"time"
)

type imageStudioKey struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	GroupID        *int64     `json:"group_id"`
	GroupName      string     `json:"group_name"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Expired        bool       `json:"expired"`
	QuotaExhausted bool       `json:"quota_exhausted"`
}

func studioKeySummary(key service.APIKey) imageStudioKey {
	out := imageStudioKey{ID: key.ID, Name: key.Name, Status: key.Status, GroupID: key.GroupID, ExpiresAt: key.ExpiresAt, Expired: key.IsExpired(), QuotaExhausted: key.IsQuotaExhausted()}
	if key.Group != nil {
		out.GroupName = key.Group.Name
	}
	return out
}

// ListImageStudioKeys projects only non-secret selection metadata.
func (h *APIKeyHandler) ListImageStudioKeys(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	page, size := response.ParsePagination(c)
	keys, result, err := h.apiKeyService.List(c.Request.Context(), subject.UserID, pagination.PaginationParams{Page: page, PageSize: size}, service.APIKeyListFilters{})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]imageStudioKey, 0, len(keys))
	for _, key := range keys {
		if key.UserID == subject.UserID {
			out = append(out, studioKeySummary(key))
		}
	}
	response.Paginated(c, out, result.Total, page, size)
}
