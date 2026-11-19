package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type imageStudioWalletSettings interface {
	ImageStudioWalletConfig(context.Context) (service.ImageStudioWalletConfig, error)
	SetImageStudioWalletConfig(ctx context.Context, expected, config service.ImageStudioWalletConfig) (service.ImageStudioWalletConfig, error)
}

const studioWalletContextKey = "image_studio_wallet"

type studioWalletContext struct {
	owner service.BatchImageOwner
	group *service.Group
}

func batchImageStudioWalletContext(c *gin.Context) (studioWalletContext, bool) {
	value, _ := c.Get(studioWalletContextKey)
	wallet, ok := value.(studioWalletContext)
	return wallet, ok
}

// The legacy directory remains useful for old works, not for admission to the
// selected wallet group. The header allows current clients to identify rollout
// state without trusting any browser-side group selection.
func (h *BatchImageHandler) ImageStudioWalletDirectory(settings imageStudioWalletSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := settings.ImageStudioWalletConfig(c.Request.Context())
		if response.ErrorFrom(c, err) {
			c.Abort()
			return
		}
		mode := "off"
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if ok && subject.UserID > 0 {
			group, err := h.configuredWalletGroup(c.Request.Context(), config.GroupID)
			if response.ErrorFrom(c, err) {
				c.Abort()
				return
			}
			if group != nil {
				mode = "on"
			}
		}
		c.Header("X-Image-Studio-Wallet", mode)
		c.Header("Cache-Control", "private, no-store")
	}
}

// The Studio group supplies routing and pricing. Its API-key binding policy
// does not grant or restrict access to the session-authenticated wallet.
func (h *BatchImageHandler) configuredWalletGroup(ctx context.Context, id int64) (*service.Group, error) {
	if id <= 0 {
		return nil, nil
	}
	group, err := h.service.ImageStudioWalletGroup(ctx, id)
	if errors.Is(err, service.ErrGroupNotFound) || errors.Is(err, service.ErrImageStudioWalletGroup) {
		return nil, nil
	}
	return group, err
}

// Only mounted below the standard panel JWT middleware. Reading/cancelling
// owned work must stay possible even if the admin pauses new generation.
func (h *BatchImageHandler) ImageStudioWalletSession(settings imageStudioWalletSettings, generation bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Login required")
			return
		}
		wallet := studioWalletContext{owner: service.BatchImageOwner{
			UserID: subject.UserID, APIKeyID: service.ImageStudioWalletOwnerSlot, StudioWallet: true,
		}}
		if generation {
			ctx := c.Request.Context()
			config, err := settings.ImageStudioWalletConfig(ctx)
			if err != nil {
				batchImageError(c, err)
				c.Abort()
				return
			}
			group, err := h.configuredWalletGroup(ctx, config.GroupID)
			if err != nil {
				batchImageError(c, err)
				c.Abort()
				return
			}
			if group == nil {
				batchImageError(c, service.ErrImageStudioWalletUnavailable)
				c.Abort()
				return
			}
			wallet.group = group
			wallet.owner.GroupID = &group.ID
			wallet.owner.StudioAccountIDs = config.AccountIDs
		}
		c.Set(studioWalletContextKey, wallet)
	}
}

type imageStudioWalletAdminConfig struct {
	GroupID    *int64  `json:"group_id" binding:"required,gte=0"`
	AccountIDs []int64 `json:"account_ids" binding:"max=32"`
}

func (value imageStudioWalletAdminConfig) config() service.ImageStudioWalletConfig {
	return service.ImageStudioWalletConfig{GroupID: *value.GroupID, AccountIDs: value.AccountIDs}
}

func (h *BatchImageHandler) AdminImageStudioWalletGroup(settings imageStudioWalletSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		if adminaccess.FromContext(c.Request.Context()) != nil {
			response.Forbidden(c, "Image configuration requires full administrator access")
			return
		}
		ctx := c.Request.Context()
		if c.Request.Method == http.MethodPut {
			var update struct {
				Config   *imageStudioWalletAdminConfig `json:"config" binding:"required"`
				Expected *imageStudioWalletAdminConfig `json:"expected" binding:"required"`
			}
			if err := c.ShouldBindJSON(&update); err != nil {
				response.BadRequest(c, "Invalid Image Studio wallet configuration")
				return
			}
			if err := h.service.ValidateImageStudioWalletConfig(ctx, update.Config.config()); response.ErrorFrom(c, err) {
				return
			}
			if result, err := settings.SetImageStudioWalletConfig(ctx, update.Expected.config(), update.Config.config()); !response.ErrorFrom(c, err) {
				response.Success(c, result)
			}
			return
		}
		result, err := settings.ImageStudioWalletConfig(ctx)
		if !response.ErrorFrom(c, err) {
			response.Success(c, result)
		}
	}
}
