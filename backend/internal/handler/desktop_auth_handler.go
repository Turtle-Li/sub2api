package handler

import (
	"context"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// DesktopAuthHandler exposes the device-code authorization flow to TT Switch.
// It does not accept passwords or captcha proofs; the system browser continues
// to use the ordinary web login/registration endpoints and their protections.
type DesktopAuthHandler struct {
	authService        *service.AuthService
	userService        *service.UserService
	desktopAuthService *service.DesktopAuthService
	settingService     *service.SettingService
}

func NewDesktopAuthHandler(
	authService *service.AuthService,
	userService *service.UserService,
	desktopAuthService *service.DesktopAuthService,
	settingService *service.SettingService,
) *DesktopAuthHandler {
	return &DesktopAuthHandler{
		authService:        authService,
		userService:        userService,
		desktopAuthService: desktopAuthService,
		settingService:     settingService,
	}
}

type desktopAuthStartRequest struct {
	CodeChallenge string `json:"code_challenge" binding:"required"`
}

type desktopAuthTokenRequest struct {
	DeviceCode   string `json:"device_code" binding:"required"`
	CodeVerifier string `json:"code_verifier" binding:"required"`
}

type desktopAuthApproveRequest struct {
	UserCode string `json:"user_code" binding:"required"`
}

type desktopAuthStartResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type desktopAuthStatusResponse struct {
	Status   string `json:"status"`
	Interval int    `json:"interval,omitempty"`
}

// Start creates an opaque, short-lived authorization transaction.
// POST /api/v1/auth/desktop/start
func (h *DesktopAuthHandler) Start(c *gin.Context) {
	if h == nil || h.desktopAuthService == nil || h.settingService == nil {
		response.ErrorFrom(c, service.ErrDesktopAuthUnavailable)
		return
	}
	var request desktopAuthStartRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.ErrorFrom(c, service.ErrDesktopAuthInvalidRequest)
		return
	}

	verificationBase, err := h.desktopVerificationBaseURI(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	grant, err := h.desktopAuthService.Start(c.Request.Context(), request.CodeChallenge)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	verificationURI := desktopVerificationURI(verificationBase, grant.UserCode)
	response.Success(c, desktopAuthStartResponse{
		DeviceCode:              grant.DeviceCode,
		UserCode:                grant.UserCode,
		VerificationURI:         strings.SplitN(verificationURI, "?", 2)[0],
		VerificationURIComplete: verificationURI,
		ExpiresIn:               grant.ExpiresIn,
		Interval:                grant.Interval,
	})
}

// Token returns pending until an authenticated browser explicitly approves the
// grant. Once approved, a successful call consumes it and returns a normal
// login token pair; a parallel replay cannot obtain another one.
// POST /api/v1/auth/desktop/token
func (h *DesktopAuthHandler) Token(c *gin.Context) {
	if h == nil || h.desktopAuthService == nil || h.authService == nil || h.userService == nil {
		response.ErrorFrom(c, service.ErrDesktopAuthUnavailable)
		return
	}
	var request desktopAuthTokenRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.ErrorFrom(c, service.ErrDesktopAuthInvalidRequest)
		return
	}

	result, err := h.desktopAuthService.Consume(c.Request.Context(), request.DeviceCode, request.CodeVerifier)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	switch result.Status {
	case "pending":
		response.Success(c, desktopAuthStatusResponse{Status: "pending", Interval: 5})
		return
	case "expired", "denied":
		response.Success(c, desktopAuthStatusResponse{Status: result.Status})
		return
	case "authenticated":
		user, err := h.userService.GetProfile(c.Request.Context(), result.UserID)
		if err != nil {
			response.ErrorFrom(c, infraerrors.ServiceUnavailable("DESKTOP_AUTH_USER_UNAVAILABLE", "desktop authorization is temporarily unavailable"))
			return
		}
		respondWithTokenPairWithStatus(c, h.authService, user, "authenticated")
		return
	default:
		response.Success(c, desktopAuthStatusResponse{Status: "denied"})
	}
}

// Approve attaches the currently authenticated browser user to a pending
// device grant. It is deliberately in the authenticated route group; passing a
// user code alone never authorizes a desktop session.
// POST /api/v1/auth/desktop/approve
func (h *DesktopAuthHandler) Approve(c *gin.Context) {
	if h == nil || h.desktopAuthService == nil {
		response.ErrorFrom(c, service.ErrDesktopAuthUnavailable)
		return
	}
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var request desktopAuthApproveRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.ErrorFrom(c, service.ErrDesktopAuthInvalidRequest)
		return
	}
	result, err := h.desktopAuthService.Approve(c.Request.Context(), request.UserCode, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, desktopAuthStatusResponse{Status: result.Status})
}

func (h *DesktopAuthHandler) desktopVerificationBaseURI(ctx context.Context) (*url.URL, error) {
	// This signature is intentionally not exported: settings owns the configured
	// browser origin while request handlers own URL escaping. The frontend is
	// served at this URL and contains the /desktop-auth route.
	if h == nil || h.settingService == nil {
		return nil, infraerrors.ServiceUnavailable("DESKTOP_AUTH_NOT_CONFIGURED", "desktop authorization is not configured")
	}
	frontendURL := strings.TrimSpace(h.settingService.GetFrontendURL(ctx))
	if frontendURL == "" {
		return nil, infraerrors.ServiceUnavailable("DESKTOP_AUTH_NOT_CONFIGURED", "desktop authorization is not configured")
	}
	parsed, err := url.Parse(frontendURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return nil, infraerrors.ServiceUnavailable("DESKTOP_AUTH_NOT_CONFIGURED", "desktop authorization is not configured")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/desktop-auth"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func desktopVerificationURI(base *url.URL, userCode string) string {
	verification := *base
	verification.RawQuery = url.Values{"user_code": []string{userCode}}.Encode()
	return verification.String()
}
