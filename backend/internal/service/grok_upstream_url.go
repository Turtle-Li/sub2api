package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

func grokBaseURLValidator(account *Account, cfg *config.Config) (xai.BaseURLValidator, error) {
	if account == nil || !account.IsGrok() {
		return nil, fmt.Errorf("grok account is required")
	}
	switch account.Type {
	case AccountTypeOAuth:
		// Official gateway hosts are always trusted and always usable, even when
		// the operator enables a restrictive URL allowlist. A custom forwarding
		// host is vetted by the same operator policy as API-key accounts.
		//
		// The official-vs-custom decision is made on the host, not via
		// ValidateTrustedBaseURL: that validator relaxes to accept-any under the
		// XAI_ALLOW_UNSAFE_URL_OVERRIDES debug switch, which must never let an
		// OAuth bearer token reach an arbitrary custom host.
		policyValidator := grokOperatorPolicyValidator(cfg)
		return redactedGrokBaseURLValidator(func(raw string) (string, error) {
			if xai.IsOfficialBaseURL(raw) {
				return xai.ValidateTrustedBaseURL(raw)
			}
			return policyValidator(raw)
		}), nil
	case AccountTypeAPIKey:
		return redactedGrokBaseURLValidator(grokOperatorPolicyValidator(cfg)), nil
	default:
		return nil, fmt.Errorf("unsupported grok account type: %s", account.Type)
	}
}

// grokOperatorPolicyValidator 按全局出站 URL 安全策略校验自定义 base_url：
// HTTPS 在白名单开启时强制 UpstreamHosts，关闭时仅做格式校验；HTTP 无论
// Enabled 状态都必须同时开启 allow_insecure_http，并精确匹配 UpstreamHosts
// 中的主机与端口。私网和 Tailnet/CGNAT 地址还需要 allow_private_hosts。
func grokOperatorPolicyValidator(cfg *config.Config) xai.BaseURLValidator {
	if cfg == nil {
		return xai.ValidateBaseURL
	}
	return func(raw string) (string, error) {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", errors.New("invalid URL")
		}
		if strings.EqualFold(parsed.Scheme, "http") {
			return validateGrokInsecureHTTPURL(raw, parsed, cfg.Security.URLAllowlist)
		}
		if !cfg.Security.URLAllowlist.Enabled {
			return urlvalidator.ValidateURLFormat(raw, false)
		}
		return urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
			AllowedHosts:     cfg.Security.URLAllowlist.UpstreamHosts,
			RequireAllowlist: true,
			AllowPrivate:     cfg.Security.URLAllowlist.AllowPrivateHosts,
		})
	}
}

func validateGrokInsecureHTTPURL(raw string, parsed *url.URL, policy config.URLAllowlistConfig) (string, error) {
	if !policy.AllowInsecureHTTP {
		return "", errors.New("insecure HTTP is disabled")
	}
	validated, err := urlvalidator.ValidateHTTPURL(raw, true, urlvalidator.ValidationOptions{
		AllowedHosts:     []string{parsed.Hostname()},
		RequireAllowlist: true,
		AllowPrivate:     policy.AllowPrivateHosts,
	})
	if err != nil {
		return "", err
	}
	if !grokHTTPAuthorityAllowlisted(parsed, policy.UpstreamHosts) {
		return "", errors.New("HTTP endpoint is not allowlisted")
	}
	return validated, nil
}

// grokHTTPAuthorityAllowlisted requires an exact authority match for HTTP.
// A non-default port therefore needs an explicit host:port entry; a host-only
// entry cannot authorize arbitrary services on the same private machine.
func grokHTTPAuthorityAllowlisted(parsed *url.URL, allowed []string) bool {
	if parsed == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(parsed.Host))
	if want == "" {
		return false
	}
	for _, entry := range allowed {
		candidate := strings.ToLower(strings.TrimSpace(entry))
		if candidate == "" || strings.Contains(candidate, "://") || strings.ContainsAny(candidate, "/?#@") {
			continue
		}
		if candidate == want {
			return true
		}
	}
	return false
}

func redactedGrokBaseURLValidator(validator xai.BaseURLValidator) xai.BaseURLValidator {
	return func(raw string) (string, error) {
		validated, err := validator(raw)
		if err != nil {
			return "", errors.New("base URL rejected by URL security policy")
		}
		return validated, nil
	}
}

func buildGrokResponsesURL(account *Account, cfg *config.Config, settings ...*SettingService) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	if len(settings) > 0 && settings[0] != nil {
		baseURL = settings[0].ResolveGrokBaseURL(context.Background(), account)
	}
	return xai.BuildResponsesURLWithValidator(baseURL, validator)
}

func buildGrokChatCompletionsURL(account *Account, cfg *config.Config, settings ...*SettingService) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	if len(settings) > 0 && settings[0] != nil {
		baseURL = settings[0].ResolveGrokBaseURL(context.Background(), account)
	}
	return xai.BuildChatCompletionsURLWithValidator(baseURL, validator)
}

// buildGrokBillingURL 解析 billing 探测端点：跟随账号的转发 base_url，
// 未定制的账号仍指向官方 CLI 网关。
func buildGrokBillingURL(account *Account, cfg *config.Config, weekly bool) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokBaseURL()
	// Official public/regional API hosts do not expose Grok Build billing.
	// Keep custom relays on their configured host because they may proxy the CLI
	// billing path alongside inference.
	if xai.IsOfficialBaseURL(baseURL) && !isGrokCLIProxyBaseURL(baseURL) {
		baseURL = xai.DefaultCLIBaseURL
	}
	return xai.BuildBillingURLWithValidator(baseURL, weekly, validator)
}

func buildGrokMediaURL(account *Account, cfg *config.Config, endpoint GrokMediaEndpoint, requestID string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	baseURL := account.GetGrokMediaBaseURL()
	switch endpoint {
	case GrokMediaEndpointImagesGenerations:
		return xai.BuildImagesGenerationsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointImagesEdits:
		return xai.BuildImagesEditsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosGenerations:
		return xai.BuildVideosGenerationsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosEdits:
		return xai.BuildVideosEditsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideosExtensions:
		return xai.BuildVideosExtensionsURLWithValidator(baseURL, validator)
	case GrokMediaEndpointVideoStatus:
		return xai.BuildVideoURLWithValidator(baseURL, requestID, validator)
	case GrokMediaEndpointVideoContent:
		videoURL, err := xai.BuildVideoURLWithValidator(baseURL, requestID, validator)
		if err != nil {
			return "", err
		}
		return videoURL + "/content", nil
	default:
		return "", fmt.Errorf("unsupported grok media endpoint: %s", endpoint)
	}
}

// buildGrokVoiceURL returns the official xAI Voice API endpoint.
// Voice HTTP (/tts, /stt, /custom-voices) and WS (/realtime) are only exposed
// by api.x.ai — the CLI chat proxy does not implement them. When the account
// base_url points at the CLI proxy (or is empty), fall back to DefaultBaseURL.
func buildGrokVoiceURL(account *Account, cfg *config.Config, endpoint string) (string, error) {
	validator, err := grokBaseURLValidator(account, cfg)
	if err != nil {
		return "", err
	}
	base := ""
	if account != nil {
		base = account.GetGrokMediaBaseURL()
	}
	if strings.TrimSpace(base) == "" || isGrokCLIProxyBaseURL(base) {
		base = xai.DefaultBaseURL
	}
	validated, err := validator(base)
	if err != nil {
		return "", err
	}
	ep := strings.Trim(strings.TrimSpace(endpoint), "/")
	if ep == "" {
		return "", fmt.Errorf("voice endpoint is required")
	}
	parts := strings.Split(ep, "/")
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid voice endpoint path")
		}
		encoded = append(encoded, url.PathEscape(part))
	}
	return strings.TrimRight(validated, "/") + "/" + strings.Join(encoded, "/"), nil
}

func isGrokCLIProxyBaseURL(raw string) bool {
	return isGrokCLIProxyTarget(raw)
}
