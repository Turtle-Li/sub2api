package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// ListImageStudioKeys projects only non-secret selection metadata.
func (h *APIKeyHandler) ListImageStudioKeys(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	page, size := response.ParsePagination(c)
	keys, result, err := h.apiKeyService.ListImageStudioKeys(c.Request.Context(), subject.UserID, pagination.PaginationParams{Page: page, PageSize: size})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, keys, result.Total, page, size)
}
