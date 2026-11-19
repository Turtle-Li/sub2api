package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GatewayGroupModelAllowlist adds the Muse endpoint policy on public gateway
// routes. Media-only surfaces like Image Studio keep the model check alone.
func GatewayGroupModelAllowlist() gin.HandlerFunc {
	allowlist := GroupModelAllowlist()
	return func(c *gin.Context) {
		key, _ := GetAPIKeyFromContext(c)
		if c.Request != nil && key != nil && key.Group.IsMuseMediaOnly() && !museMediaGroupRouteAllowed(c) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			groupModelAllowlistErrorWriter(c)(c, http.StatusNotFound, "Muse is available only through the image and video APIs")
			c.Abort()
			return
		}
		allowlist(c)
	}
}

func museMediaGroupRouteAllowed(c *gin.Context) bool {
	// Use the registered route template, not a caller-controlled URL prefix.
	path := c.FullPath()
	if strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	switch c.Request.Method {
	case http.MethodPost:
		switch path {
		case "/images/generations", "/images/edits", "/images/generations/async", "/images/edits/async", "/images/tasks",
			"/images/batches", "/images/batches/:id/cancel", "/videos", "/videos/generations":
			return true
		}
	case http.MethodGet:
		switch path {
		case "/models":
			return c.Query("client_version") == "" // Do not advertise a Codex manifest for a media-only group.
		case "/models/:model", "/usage", "/sub2api/billing",
			"/images/tasks/:task_id", "/images/tasks/:task_id/content",
			"/images/batches", "/images/batches/models", "/images/batches/:id", "/images/batches/:id/items",
			"/images/batches/:id/items/:custom_id/content", "/images/batches/:id/result-files", "/images/batches/:id/download",
			"/videos/:request_id", "/videos/:request_id/content", "/videos/generations/:request_id", "/videos/generations/:request_id/content":
			return true
		}
	}
	return false
}
