package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	UpstreamBalanceClientMessage = "Upstream service temporarily unavailable"
	upstreamBalanceProbeLimit    = 64 << 10
)

type UpstreamBalanceClassification struct {
	HideFromClient     bool
	IsBalanceExhausted bool
	ProviderCode       string
	NormalizedReason   string
}

var providerErrorCodePattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,64}$`)

func IsValidProviderErrorCode(code string) bool { return providerErrorCodePattern.MatchString(code) }

func extractTrustedBalanceFields(raw []byte, statusCode int, platform string, result *UpstreamBalanceClassification, billingCode, workspaceDeactivated *bool) string {
	var message string
	for _, field := range []string{"error.code", "code", "error.type"} {
		value := gjson.GetBytes(raw, field)
		if value.Type != gjson.String || !IsValidProviderErrorCode(value.Str) {
			continue
		}
		if result.ProviderCode == "" {
			result.ProviderCode = value.Str
		}
		switch value.Str {
		case "gateway_reservation_insufficient", "insufficient_quota", "billing_not_active":
			if !*billingCode {
				result.ProviderCode = value.Str
			}
			*billingCode = true
		case "deactivated_workspace":
			*workspaceDeactivated = true
		}
	}
	// This one fixed OpenAI code also has a deployed detail envelope.
	if statusCode == http.StatusPaymentRequired && platform == PlatformOpenAI {
		if dc := gjson.GetBytes(raw, "detail.code"); dc.Type == gjson.String && dc.Str == "deactivated_workspace" {
			*workspaceDeactivated = true
		}
	}
	if m := gjson.GetBytes(raw, "error.message"); m.Type == gjson.String {
		message = m.Str
	}
	return message
}

func extractEmbeddedJSONErrorEnvelope(message string) []byte {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}
	candidate := trimmed
	if !gjson.Valid(candidate) {
		lastBrace := strings.LastIndex(trimmed, "}")
		if lastBrace <= 0 {
			return nil
		}
		candidate = trimmed[:lastBrace+1]
		if !gjson.Valid(candidate) {
			return nil
		}
	}
	if !gjson.Get(candidate, "error").IsObject() {
		return nil
	}
	return []byte(candidate)
}

// ClassifyUpstreamBalanceError uses only provider-owned error fields. Other
// fields (including arbitrary detail, top-level message and echoed payloads)
// are not evidence of a billing error.
func ClassifyUpstreamBalanceError(platform string, statusCode int, body []byte) UpstreamBalanceClassification {
	var result UpstreamBalanceClassification
	var billingCode, workspaceDeactivated bool
	var message string
	if gjson.ValidBytes(body) {
		message = extractTrustedBalanceFields(body, statusCode, platform, &result, &billingCode, &workspaceDeactivated)
		if embedded := extractEmbeddedJSONErrorEnvelope(message); len(embedded) > 0 {
			if innerMsg := extractTrustedBalanceFields(embedded, statusCode, platform, &result, &billingCode, &workspaceDeactivated); innerMsg != "" {
				message = innerMsg
			}
		}
	} else if len(body) <= 512 {
		message = string(body)
	}
	switch statusCode {
	case http.StatusPaymentRequired:
		result.HideFromClient = true
		if platform == PlatformOpenAI && workspaceDeactivated {
			result.ProviderCode = "deactivated_workspace"
			result.NormalizedReason = "upstream_workspace_deactivated"
			return result
		}
	case http.StatusBadRequest:
		if !billingCode && !isUpstreamBalanceMessage(message) {
			return result
		}
		result.HideFromClient = true
	default:
		return result
	}
	result.IsBalanceExhausted = true
	result.NormalizedReason = "upstream_payment_required"
	return result
}

func isUpstreamBalanceMessage(message string) bool {
	message = strings.TrimRight(strings.ToLower(strings.TrimSpace(message)), ".!。！")
	switch message {
	case "your credit balance is too low", "credit balance is too low",
		"可用资金不足以支付本次请求", "your account has insufficient balance",
		"your credit balance is too low to access the anthropic api",
		"your credit balance is too low to access the anthropic api. please go to plans & billing to upgrade or purchase credits":
		return true
	default:
		return false
	}
}

type replayUpstreamBody struct {
	io.Reader
	io.Closer
}

type replayUpstreamReadError struct{ err error }

func (r *replayUpstreamReadError) Read([]byte) (int, error) {
	if r.err == nil {
		return 0, io.EOF
	}
	err := r.err
	r.err = nil
	return 0, err
}

// probeUpstreamBalanceResponse is called at each HTTP attempt, before any
// retry, transformation or response writing. Unmatched bodies are replayed
// unchanged for legacy handling. Only the terminal handler writes to clients.
func probeUpstreamBalanceResponse(ctx context.Context, c *gin.Context, limiter *RateLimitService, account *Account, resp *http.Response, model string) *UpstreamFailoverError {
	if resp == nil || (resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusPaymentRequired) {
		return nil
	}
	var body []byte
	var readErr error
	if resp.Body != nil {
		body, readErr = io.ReadAll(io.LimitReader(resp.Body, upstreamBalanceProbeLimit+1))
	}
	evidence := body
	if len(body) > upstreamBalanceProbeLimit || readErr != nil {
		evidence = nil // Never classify a partial 400 as a billing error.
	}
	failure := newHiddenUpstreamBalanceFailure(ctx, c, limiter, account, resp.StatusCode, resp.Header, evidence, model)
	if resp.Body != nil {
		if failure != nil {
			_ = resp.Body.Close()
			resp.Body = http.NoBody
		} else {
			unread := io.Reader(resp.Body)
			if readErr != nil {
				unread = io.MultiReader(&replayUpstreamReadError{err: readErr}, unread)
			}
			resp.Body = replayUpstreamBody{io.MultiReader(bytes.NewReader(body), unread), resp.Body}
		}
	}
	return failure
}

func (s *OpenAIGatewayService) probeOpenAIBalanceResponse(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, model string) *UpstreamFailoverError {
	if resp == nil || (resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusPaymentRequired) {
		return nil
	}
	var body []byte
	var readErr error
	if resp.Body != nil {
		body, readErr = io.ReadAll(io.LimitReader(resp.Body, upstreamBalanceProbeLimit+1))
	}
	evidence := body
	if len(body) > upstreamBalanceProbeLimit || readErr != nil {
		evidence = nil
	}
	redactedEvidence := evidence
	if len(redactedEvidence) > 0 {
		redactedEvidence = s.redactAgentIdentitySensitiveBody(ctx, account, redactedEvidence)
	}
	failure := newHiddenUpstreamBalanceFailure(ctx, c, s.rateLimitService, account, resp.StatusCode, resp.Header, redactedEvidence, model)
	if resp.Body != nil {
		if failure != nil {
			_ = resp.Body.Close()
			resp.Body = http.NoBody
		} else {
			unread := io.Reader(resp.Body)
			if readErr != nil {
				unread = io.MultiReader(&replayUpstreamReadError{err: readErr}, unread)
			}
			resp.Body = replayUpstreamBody{io.MultiReader(bytes.NewReader(body), unread), resp.Body}
		}
	}
	if failure != nil && account.Platform == PlatformGrok && s.rateLimitService == nil {
		// Standalone Grok gateways can omit the shared rate-limit service. Keep
		// the same provider policy they use for other upstream HTTP failures.
		s.handleGrokAccountUpstreamError(withGrokTeamRateLimitModel(ctx, model), account, failure.StatusCode, resp.Header, failure.ResponseBody)
	}
	return failure
}

func redactAccountSensitiveBody(account *Account, body []byte) []byte {
	if len(body) == 0 || account == nil {
		return body
	}
	redacted := sanitizeOpenAIPassthroughClientErrorText(account, string(body))
	return []byte(redacted)
}

func newHiddenUpstreamBalanceFailure(ctx context.Context, c *gin.Context, limiter *RateLimitService, account *Account, status int, headers http.Header, body []byte, model string) *UpstreamFailoverError {
	if account == nil || (status != http.StatusBadRequest && status != http.StatusPaymentRequired) {
		return nil
	}
	classification := ClassifyUpstreamBalanceError(account.Platform, status, body)
	if !classification.HideFromClient {
		return nil
	}
	safeBody := redactAccountSensitiveBody(account, body)
	safeMsg := sanitizeOpenAIPassthroughClientErrorText(account, extractUpstreamErrorMessage(safeBody))
	if safeMsg == "" {
		safeMsg = classification.NormalizedReason
	}
	// Retain only narrowly scoped evidence for diagnostics and keywords. No
	// application-supplied error passthrough rule can expose a hidden attempt.
	setOpsUpstreamError(c, status, classification.NormalizedReason, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
		UpstreamStatusCode: status, UpstreamRequestID: headers.Get("x-request-id"),
		ProviderCode: classification.ProviderCode, Kind: "failover", Reason: classification.NormalizedReason,
		Message:                  safeMsg,
		protectedUpstreamBalance: true,
	})
	if limiter != nil {
		limiter.HandleUpstreamError(ctx, account, status, headers, safeBody, model)
	}
	return &UpstreamFailoverError{
		StatusCode: status, ResponseBody: safeBody, HiddenUpstreamBalance: true,
		RetryableOnSameAccount: false, Scope: GatewayFailureScopeAccount,
		Reason: GatewayFailureReason(classification.NormalizedReason),
	}
}

// MustHideUpstreamBalance also protects terminal paths that receive a legacy
// failover error. The image endpoint's deliberate billing response is not a
// generic upstream error and keeps its existing client contract.
func (e *UpstreamFailoverError) MustHideUpstreamBalance(platform string) bool {
	return e != nil && e.Reason != OpenAIImagesInsufficientBalanceReason &&
		(e.HiddenUpstreamBalance || ClassifyUpstreamBalanceError(platform, e.StatusCode, e.ResponseBody).HideFromClient)
}
