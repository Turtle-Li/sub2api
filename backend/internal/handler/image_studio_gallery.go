package handler

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *BatchImageHandler) Gallery(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, err := h.service.Gallery(c.Request.Context(), owner, service.BatchImageGalleryRequest{Limit: limit, Cursor: c.Query("cursor"), Status: c.Query("status"), Search: c.Query("q"), RootBatchID: c.Query("root")})
	if err != nil {
		batchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *BatchImageHandler) ItemThumbnail(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	content, err := h.download.ItemThumbnail(c.Request.Context(), owner, c.Param("id"), c.Param("custom_id"))
	if err != nil {
		c.Header("Cache-Control", "private, no-store")
		if errors.Is(err, service.ErrStudioThumbnailPending) {
			c.Header("Retry-After", "2")
		}
		batchImageError(c, err)
		return
	}
	// A preview never records the user's explicit original-download action.
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("Vary", "Authorization, X-API-Key-ID")
	c.Header("X-Content-Type-Options", "nosniff")
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(content))
	c.Header("ETag", etag)
	for _, tag := range strings.Split(c.GetHeader("If-None-Match"), ",") {
		if strings.TrimPrefix(strings.TrimSpace(tag), "W/") == etag || strings.TrimSpace(tag) == "*" {
			c.Status(http.StatusNotModified)
			return
		}
	}
	c.Data(http.StatusOK, "image/jpeg", content)
}
