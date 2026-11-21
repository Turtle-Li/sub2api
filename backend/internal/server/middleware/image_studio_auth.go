package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type studioAPIKeyReader interface {
	apiKeyLastUsedToucher
	GetByID(context.Context, int64) (*service.APIKey, error)
}

// ImageStudioKeyAuth requires a JWT subject and an owned key ID, never a key credential.
func ImageStudioKeyAuth(keys studioAPIKeyReader, subscriptions *service.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return OwnedSessionKeyAuth(keys, subscriptions, cfg, func(c *gin.Context) (int64, error) { return strconv.ParseInt(c.GetHeader("X-API-Key-ID"), 10, 64) })
}

// OwnedSessionKeyAuth resolves a non-secret key ID but never trusts it without checking the session owner.
func OwnedSessionKeyAuth(keys studioAPIKeyReader, subscriptions *service.SubscriptionService, cfg *config.Config, selectID func(*gin.Context) (int64, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok {
			AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Login required")
			return
		}
		id, err := selectID(c)
		if err != nil || id <= 0 {
			AbortWithError(c, http.StatusBadRequest, "API_KEY_ID_REQUIRED", "Select an API key ID")
			return
		}
		key, err := keys.GetByID(c.Request.Context(), id)
		if err != nil && !errors.Is(err, service.ErrAPIKeyNotFound) {
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load API key")
			return
		}
		if err != nil || key == nil || key.UserID != subject.UserID || key.User == nil || key.User.ID != subject.UserID {
			AbortWithError(c, http.StatusNotFound, "API_KEY_NOT_FOUND", "API key not found")
			return
		}
		admitLoadedAPIKey(c, key, keys, subscriptions, cfg)
	}
}

// Only called after binding the JWT owner and freshly authorized channel group.
func BindVideoStudioWallet(c *gin.Context, key *service.APIKey) {
	c.Set(string(ContextKeyAPIKey), key)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: key.UserID, Concurrency: key.User.Concurrency})
	c.Set(string(ContextKeyUserRole), key.User.Role)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, key.UserID))
	setGroupContext(c, key.Group)
}
