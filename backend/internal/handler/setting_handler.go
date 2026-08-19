package handler

import (
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SettingHandler 公开设置处理器（无需认证）
type SettingHandler struct {
	settingService           *service.SettingService
	notificationEmailService *service.NotificationEmailService
	version                  string
}

// NewSettingHandler 创建公开设置处理器
func NewSettingHandler(settingService *service.SettingService, version string) *SettingHandler {
	return &SettingHandler{
		settingService: settingService,
		version:        version,
	}
}

// SetNotificationEmailService attaches the public notification email service without
// changing the constructor signature used by existing tests.
func (h *SettingHandler) SetNotificationEmailService(notificationEmailService *service.NotificationEmailService) {
	h.notificationEmailService = notificationEmailService
}

// GetPublicSettings 获取公开设置
// GET /api/v1/settings/public
func (h *SettingHandler) GetPublicSettings(c *gin.Context) {
	settings, err := h.settingService.GetPublicSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.PublicSettings{
		RegistrationEnabled:                 settings.RegistrationEnabled,
		EmailVerifyEnabled:                  settings.EmailVerifyEnabled,
		ForceEmailOnThirdPartySignup:        settings.ForceEmailOnThirdPartySignup,
		RegistrationEmailSuffixWhitelist:    settings.RegistrationEmailSuffixWhitelist,
		RegistrationEmailDomainQuotaEnabled: settings.RegistrationEmailDomainQuotaEnabled,
		PromoCodeEnabled:                    settings.PromoCodeEnabled,
		PasswordResetEnabled:                settings.PasswordResetEnabled,
		InvitationCodeEnabled:               settings.InvitationCodeEnabled,
		TotpEnabled:                         settings.TotpEnabled,
		PasskeyEnabled:                      settings.PasskeyEnabled,
		LoginAgreementEnabled:               settings.LoginAgreementEnabled,
		LoginAgreementMode:                  settings.LoginAgreementMode,
		LoginAgreementUpdatedAt:             settings.LoginAgreementUpdatedAt,
		LoginAgreementRevision:              settings.LoginAgreementRevision,
		LoginAgreementDocuments:             publicLoginAgreementDocumentsToDTO(settings.LoginAgreementDocuments),
		TurnstileEnabled:                    settings.TurnstileEnabled,
		TurnstileSiteKey:                    settings.TurnstileSiteKey,
		TencentCaptchaEnabled:               settings.TencentCaptchaEnabled,
		TencentCaptchaAppID:                 settings.TencentCaptchaAppID,
		TencentCaptchaRegion:                settings.TencentCaptchaRegion,
		AliyunCaptchaEnabled:                settings.AliyunCaptchaEnabled,
		AliyunCaptchaSceneID:                settings.AliyunCaptchaSceneID,
		AliyunCaptchaPrefix:                 settings.AliyunCaptchaPrefix,
		AliyunCaptchaRegion:                 settings.AliyunCaptchaRegion,
		SiteName:                            settings.SiteName,
		SiteLogo:                            settings.SiteLogo,
		SiteSubtitle:                        settings.SiteSubtitle,
		APIBaseURL:                          settings.APIBaseURL,
		ContactInfo:                         settings.ContactInfo,
		DocURL:                              settings.DocURL,
		HomeContent:                         settings.HomeContent,
		CompactHomeEnabled:                  settings.CompactHomeEnabled,
		HideCcsImportButton:                 settings.HideCcsImportButton,
		PurchaseSubscriptionEnabled:         settings.PurchaseSubscriptionEnabled,
		PurchaseSubscriptionURL:             settings.PurchaseSubscriptionURL,
		TableDefaultPageSize:                settings.TableDefaultPageSize,
		TablePageSizeOptions:                settings.TablePageSizeOptions,
		CustomMenuItems:                     dto.ParseUserVisibleMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                     dto.ParseCustomEndpoints(settings.CustomEndpoints),
		DingTalkOAuthEnabled:                settings.DingTalkOAuthEnabled,
		LinuxDoOAuthEnabled:                 settings.LinuxDoOAuthEnabled,
		WeChatOAuthEnabled:                  settings.WeChatOAuthEnabled,
		WeChatOAuthOpenEnabled:              settings.WeChatOAuthOpenEnabled,
		WeChatOAuthMPEnabled:                settings.WeChatOAuthMPEnabled,
		WeChatOAuthMobileEnabled:            settings.WeChatOAuthMobileEnabled,
		OIDCOAuthEnabled:                    settings.OIDCOAuthEnabled,
		OIDCOAuthProviderName:               settings.OIDCOAuthProviderName,
		GitHubOAuthEnabled:                  settings.GitHubOAuthEnabled,
		GoogleOAuthEnabled:                  settings.GoogleOAuthEnabled,
		BackendModeEnabled:                  settings.BackendModeEnabled,
		PaymentEnabled:                      settings.PaymentEnabled,
		Version:                             h.version,
		ServerTimezone:                      timezone.Name(),
		ServerUTCOffset:                     timezone.UTCOffset(),
		BalanceLowNotifyEnabled:             settings.BalanceLowNotifyEnabled,
		AccountQuotaNotifyEnabled:           settings.AccountQuotaNotifyEnabled,
		BalanceLowNotifyThreshold:           settings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:         settings.BalanceLowNotifyRechargeURL,

		ChannelMonitorEnabled:                settings.ChannelMonitorEnabled,
		ChannelMonitorMode:                   settings.ChannelMonitorMode,
		ChannelMonitorDefaultIntervalSeconds: settings.ChannelMonitorDefaultIntervalSeconds,
		ChannelMonitorHideThroughput:         settings.ChannelMonitorHideThroughput,
		ChannelMonitorShowQuota:              settings.ChannelMonitorShowQuota,

		AvailableChannelsEnabled: settings.AvailableChannelsEnabled,

		ModelPlazaEnabled:     settings.ModelPlazaEnabled,
		ModelPlazaRequireAuth: settings.ModelPlazaRequireAuth,

		AffiliateEnabled: settings.AffiliateEnabled,

		RiskControlEnabled: settings.RiskControlEnabled,

		AllowUserViewErrorRequests: settings.AllowUserViewErrorRequests,
	})
}

// GetDesktopSettings returns the narrow, unauthenticated discovery contract
// used by desktop clients to locate the account control plane.
// GET /api/v1/settings/desktop
func (h *SettingHandler) GetDesktopSettings(c *gin.Context) {
	controlPlaneURL, err := h.settingService.GetDesktopControlPlaneURL(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if controlPlaneURL == "" {
		controlPlaneURL = desktopRequestOrigin(c)
	}
	if controlPlaneURL == "" {
		response.InternalError(c, "desktop control plane URL is unavailable")
		return
	}

	response.Success(c, dto.DesktopSettings{
		SchemaVersion:   1,
		ControlPlaneURL: controlPlaneURL,
	})
}

// GetDesktopPromotions returns the active, presentation-safe promotion
// catalog for TT Switch. Disabled and out-of-window items are filtered here so
// older clients cannot accidentally surface them.
// GET /api/v1/settings/desktop-promotions
func (h *SettingHandler) GetDesktopPromotions(c *gin.Context) {
	items, err := h.settingService.GetDesktopPromotions(c.Request.Context(), time.Now())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.DesktopPromotions{
		SchemaVersion: 1,
		Items:         items,
	})
}

// GetDesktopTools returns the active declaration-only tool catalog used by
// TT Switch. The catalog contains no commands or paths; local clients still
// enforce their own action allowlist before changing configuration.
// GET /api/v1/desktop/tools
func (h *SettingHandler) GetDesktopTools(c *gin.Context) {
	catalog, err := h.settingService.GetDesktopToolCatalog(c.Request.Context(), false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, catalog)
}

// GetDesktopToolVersion is the cheap synchronization probe used by TT Switch.
// GET /api/v1/desktop/tools/version
func (h *SettingHandler) GetDesktopToolVersion(c *gin.Context) {
	catalog, err := h.settingService.GetDesktopToolCatalog(c.Request.Context(), false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.DesktopToolCatalogVersion{
		SchemaVersion: catalog.SchemaVersion,
		Version:       catalog.Version,
	})
}

// GetDesktopUpdatePolicy returns the version decision for the requesting TT
// Switch build. It remains public so a blocked or logged-out client can still
// discover and install the required signed update.
// GET /api/v1/desktop/update-policy
func (h *SettingHandler) GetDesktopUpdatePolicy(c *gin.Context) {
	policy, err := h.settingService.GetDesktopUpdatePolicy(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	clientVersion, _ := service.DesktopClientVersion(
		c.GetHeader("X-TT-Switch-Version"),
		c.GetHeader("User-Agent"),
	)
	if clientVersion == "" {
		clientVersion = strings.TrimSpace(c.Query("current_version"))
	}
	response.Success(c, policy.StatusFor(clientVersion, time.Now()))
}

func desktopRequestOrigin(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	host := strings.TrimSpace(c.Request.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if isRequestHTTPS(c) {
		scheme = "https"
	}
	origin, err := service.NormalizeDesktopControlPlaneURL(scheme + "://" + host)
	if err != nil {
		return ""
	}
	return origin
}

// UnsubscribeNotificationEmail handles optional notification email opt-outs.
// GET /api/v1/settings/email-unsubscribe?token=...
func (h *SettingHandler) UnsubscribeNotificationEmail(c *gin.Context) {
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		response.BadRequest(c, "token is required")
		return
	}
	result, err := h.notificationEmailService.Unsubscribe(c.Request.Context(), token)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	body := "<!doctype html><html><head><meta charset=\"utf-8\"><title>Unsubscribed</title></head><body style=\"font-family:-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;padding:32px;\"><h1>Unsubscribed</h1><p>You have unsubscribed <strong>" + html.EscapeString(result.Email) + "</strong> from <strong>" + html.EscapeString(result.Event) + "</strong> emails.</p></body></html>"
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(body))
}

func publicLoginAgreementDocumentsToDTO(items []service.LoginAgreementDocument) []dto.LoginAgreementDocument {
	result := make([]dto.LoginAgreementDocument, 0, len(items))
	for _, item := range items {
		result = append(result, dto.LoginAgreementDocument{
			ID:        item.ID,
			Title:     item.Title,
			ContentMD: item.ContentMD,
		})
	}
	return result
}
