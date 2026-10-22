package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

func (s *OpenAIGatewayService) doOpenAIModelUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if permit, _ := request.Context().Value(paidFallbackPermitKey{}).(*paidFallbackPermit); permit != nil && request.GetBody != nil {
		body, err := request.GetBody()
		if err != nil {
			return nil, errors.New("paid fallback upstream model unavailable")
		}
		var value struct {
			Model string `json:"model"`
		}
		err = json.NewDecoder(body).Decode(&value)
		_ = body.Close()
		if err != nil {
			return nil, errors.New("paid fallback upstream model unavailable")
		}
		request = request.WithContext(context.WithValue(request.Context(), paidFallbackUpstreamModelKey{}, value.Model))
	}
	request = request.WithContext(WithPaidFallbackModelSend(request.Context()))
	return s.doOpenAIUpstream(request, proxyURL, account)
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, _ string, account *Account) (*http.Response, error) {
	if permit, _ := request.Context().Value(paidFallbackPermitKey{}).(*paidFallbackPermit); permit != nil {
		if account.ID != permit.accountID || !permit.settings.paidFallbackAllowed(WithPaidFallbackAPIKey(request.Context(), permit.apiKeyID), &permit.groupID) {
			return nil, errors.New("paid fallback admission revoked")
		}
	}
	if account.IsPaidFallback() && s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(account) {
		return nil, errors.New("paid fallback requires an auditable upstream transport; OAuth plugin dispatch is unsupported")
	}
	resolvedProxyURL, err := ResolveAccountProxyURL(account)
	if err != nil {
		return nil, err
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, resolvedProxyURL, account)
		if handled {
			return response, err
		}
	}
	return s.httpUpstream.Do(request, resolvedProxyURL, account.ID, account.Concurrency)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	_ string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	resolvedProxyURL, err := ResolveAccountProxyURL(account)
	if err != nil {
		return nil, err
	}
	// ModelTrace is an observational probe. Keep it on the account-aware
	// HTTPUpstream path so the configured TLS fingerprint and probe pool are
	// applied; the plugin protocol does not carry those transport attributes.
	if s.pluginManager != nil && accountTestMayChangeAvailability(request.Context()) {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, resolvedProxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			request,
			resolvedProxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(request, resolvedProxyURL, account.ID, account.Concurrency)
}
