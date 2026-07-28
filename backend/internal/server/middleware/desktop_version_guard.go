package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const desktopUpdatePolicyCacheTTL = time.Second

// DesktopVersionGuard enforces the minimum TT Switch version on the server.
// The UI blocker is intentionally not the security boundary: affected builds
// receive HTTP 426 before authentication, payment, quota, key, or subscription
// handlers can run.
func DesktopVersionGuard(settingService *service.SettingService) gin.HandlerFunc {
	var cache struct {
		sync.Mutex
		policy    service.DesktopUpdatePolicy
		expiresAt time.Time
		valid     bool
	}

	loadPolicy := func(ctx *gin.Context, now time.Time) (service.DesktopUpdatePolicy, error) {
		cache.Lock()
		if cache.valid && now.Before(cache.expiresAt) {
			policy := cache.policy
			cache.Unlock()
			return policy, nil
		}
		cache.Unlock()

		policy, err := settingService.GetDesktopUpdatePolicy(ctx.Request.Context())
		if err != nil {
			return service.DesktopUpdatePolicy{}, err
		}
		cache.Lock()
		cache.policy = policy
		cache.expiresAt = now.Add(desktopUpdatePolicyCacheTTL)
		cache.valid = true
		cache.Unlock()
		return policy, nil
	}

	return func(c *gin.Context) {
		version, desktopRequest := service.DesktopClientVersion(
			c.GetHeader("X-TT-Switch-Version"),
			c.GetHeader("User-Agent"),
		)
		if !desktopRequest || desktopUpdateExemptPath(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}

		now := time.Now()
		policy, err := loadPolicy(c, now)
		if err != nil {
			response.InternalError(c, "desktop update policy is unavailable")
			c.Abort()
			return
		}
		status := policy.StatusFor(version, now)
		if !status.UpdateRequired {
			c.Next()
			return
		}

		metadata := map[string]string{
			"current_version":           status.CurrentVersion,
			"latest_version":            status.LatestVersion,
			"minimum_supported_version": status.MinimumSupportedVersion,
			"enforce_after":             status.EnforceAfter,
			"manual_download_url":       status.ManualDownloadURL,
		}
		response.ErrorWithDetails(
			c,
			http.StatusUpgradeRequired,
			"当前 TT Switch 版本已停用，请更新后继续使用",
			"DESKTOP_UPDATE_REQUIRED",
			metadata,
		)
		c.Abort()
	}
}

func desktopUpdateExemptPath(method, path string) bool {
	switch path {
	case "/api/v1/desktop/update-policy",
		"/api/v1/settings/desktop-update-policy",
		"/api/v1/desktop/discovery",
		"/api/v1/settings/desktop",
		"/api/v1/settings/public":
		return method == http.MethodGet
	case "/api/v1/auth/logout":
		return method == http.MethodPost
	default:
		return false
	}
}
