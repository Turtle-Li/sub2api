package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// GeminiV1BetaInteractions 代理 Google 原生 POST /v1beta/interactions（主要用于 Gemini TTS）。
// 按官方 Token 计费（优先取上游 usage，辅以 WAV 真实时长按 25 tokens/s 兜底）。
func (h *GatewayHandler) GeminiV1BetaInteractions(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		googleError(c, http.StatusUnauthorized, "Invalid API key")
		return
	}
	authSubject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		googleError(c, http.StatusInternalServerError, "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.gemini_v1beta.interactions",
		zap.Int64("user_id", authSubject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	effectivePlatform := effectiveAPIKeyPlatform(c, apiKey)
	if !middleware.HasForcePlatform(c) && effectivePlatform != service.PlatformGemini {
		googleError(c, http.StatusBadRequest, "API key group platform is not gemini")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if handleRequestBodyReadError(c, reqLog, err) {
			return
		}
		status, message := requestBodyReadFailure(reqLog, c.Request, err)
		googleErrorWithType(c, status, "invalid_request_error", "", message)
		return
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		googleError(c, http.StatusBadRequest, "Request body must be a valid JSON object")
		return
	}

	modelName := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if modelName == "" {
		googleError(c, http.StatusBadRequest, "Missing model in request body")
		return
	}
	if resolvedModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok && strings.TrimSpace(resolvedModel) != "" {
		modelName = strings.TrimSpace(resolvedModel)
	}
	reqLog = reqLog.With(zap.String("model", modelName))

	setOpsRequestContext(c, modelName, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(false, false)))
	pricingCtx, pricingAt := service.WithGatewayTokenRequestPricing(c.Request.Context())
	c.Request = c.Request.WithContext(pricingCtx)

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, modelName)
	reqModel := modelName
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, authSubject, service.ContentModerationProtocolGemini, reqModel, body); decision != nil && !decision.AllowNextStage {
		googleSecurityAuditError(c, decision)
		return
	}
	if channelMapping.Mapped {
		modelName = channelMapping.MappedModel
	}

	h.executeGeminiInteraction(c, reqLog, apiKey, authSubject, pricingAt, channelMapping, reqModel, modelName, body, "", true)
}

// GeminiOpenAIAudioSpeech 提供 OpenAI 兼容 POST /v1/audio/speech（及 /audio/speech）入口。
// 将标准 OpenAI TTS 请求翻译为 Google /v1beta/interactions，支持 wav（默认）和 pcm 二进制响应，
// 与原生入口共用同一套 Gemini 账号池、并发控制、故障切换和 Token+WAV 兜底计费。
func (h *GatewayHandler) GeminiOpenAIAudioSpeech(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		writeOpenAISpeechError(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	authSubject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		writeOpenAISpeechError(c, http.StatusInternalServerError, "server_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.gemini.openai_audio_speech",
		zap.Int64("user_id", authSubject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	if effectiveAPIKeyPlatform(c, apiKey) != service.PlatformGemini {
		writeOpenAISpeechError(c, http.StatusNotFound, "not_found_error", "Audio Speech API is not supported for this platform")
		return
	}

	rawBody, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if handleRequestBodyReadError(c, reqLog, err) {
			return
		}
		status, message := requestBodyReadFailure(reqLog, c.Request, err)
		writeOpenAISpeechError(c, status, "invalid_request_error", message)
		return
	}
	if len(rawBody) == 0 || !gjson.ValidBytes(rawBody) {
		writeOpenAISpeechError(c, http.StatusBadRequest, "invalid_request_error", "Request body must be a valid JSON object")
		return
	}

	var speechReq service.OpenAISpeechRequest
	if err := json.Unmarshal(rawBody, &speechReq); err != nil {
		writeOpenAISpeechError(c, http.StatusBadRequest, "invalid_request_error", "Invalid JSON request body")
		return
	}
	modelName := strings.TrimSpace(speechReq.Model)
	if modelName == "" {
		writeOpenAISpeechError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if resolvedModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok && strings.TrimSpace(resolvedModel) != "" {
		modelName = strings.TrimSpace(resolvedModel)
	}
	reqLog = reqLog.With(zap.String("model", modelName))

	setOpsRequestContext(c, modelName, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(false, false)))
	pricingCtx, pricingAt := service.WithGatewayTokenRequestPricing(c.Request.Context())
	c.Request = c.Request.WithContext(pricingCtx)

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, modelName)
	reqModel := modelName
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, authSubject, service.ContentModerationProtocolOpenAIChat, reqModel, rawBody); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}
	if channelMapping.Mapped {
		modelName = channelMapping.MappedModel
	}

	interactionBody, responseFormat, err := service.TranslateOpenAISpeechToGeminiInteraction(speechReq, modelName)
	if err != nil {
		writeOpenAISpeechError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	h.executeGeminiInteraction(c, reqLog, apiKey, authSubject, pricingAt, channelMapping, reqModel, modelName, interactionBody, responseFormat, false)
}

func (h *GatewayHandler) executeGeminiInteraction(
	c *gin.Context,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	authSubject middleware.AuthSubject,
	pricingAt time.Time,
	channelMapping service.ChannelMappingResult,
	reqModel string,
	modelName string,
	interactionBody []byte,
	openAIFormat string,
	nativePassthrough bool,
) {
	writeErr := func(status int, errType, msg string) {
		if nativePassthrough {
			googleErrorWithType(c, status, errType, "", msg)
		} else {
			writeOpenAISpeechError(c, status, errType, msg)
		}
	}

	subscription, _ := middleware.GetSubscriptionFromContext(c)
	geminiConcurrency := NewConcurrencyHelper(h.concurrencyHelper.concurrencyService, SSEPingFormatNone, 0)
	streamStarted := false
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	userReleaseFunc, err := geminiConcurrency.AcquireUserSlotWithWait(c, authSubject.UserID, authSubject.Concurrency, false, &streamStarted)
	if err != nil {
		reqLog.Warn("gemini.interactions.user_slot_acquire_failed", zap.Error(err))
		writeErr(http.StatusTooManyRequests, "rate_limit_error", err.Error())
		return
	}
	userReleaseFunc = wrapReleaseOnDone(c.Request.Context(), userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("gemini.interactions.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		if nativePassthrough {
			googleBillingError(c, status, code, message)
		} else {
			writeOpenAISpeechError(c, status, code, message)
		}
		return
	}

	inflightRelease, err := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, tokenInflightEstimate(modelName, interactionBody))
	if err != nil {
		reqLog.Info("gemini.interactions.inflight_reservation_rejected", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		if nativePassthrough {
			googleBillingError(c, status, code, message)
		} else {
			writeOpenAISpeechError(c, status, code, message)
		}
		return
	}
	defer inflightRelease()

	fs := NewFailoverState(h.maxAccountSwitchesGemini, false)
	for {
		selectionCtx := service.WithNativeGeminiAccountSelection(c.Request.Context())
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(selectionCtx, apiKey.GroupID, "", modelName, fs.FailedAccountIDs, "", int64(0))
		if err != nil {
			if failoverClientGone(c) {
				return
			}
			if len(fs.FailedAccountIDs) > 0 && fs.LastFailoverErr != nil {
				reqLog.Warn("gemini.interactions.no_more_accounts", zap.Int("switches", fs.SwitchCount), zap.Error(err))
				if nativePassthrough {
					h.handleGeminiFailoverExhausted(c, fs.LastFailoverErr)
				} else {
					writeOpenAISpeechFailoverError(c, fs.LastFailoverErr)
				}
				return
			}
			markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
			writeErr(http.StatusServiceUnavailable, "service_unavailable", "No available Gemini accounts: "+err.Error())
			return
		}
		account := selection.Account
		if !service.IsGeminiInteractionsAccount(account) {
			if selection.Acquired && selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			fs.FailedAccountIDs[account.ID] = struct{}{}
			continue
		}

		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				markOpsRoutingCapacityLimited(c)
				writeErr(http.StatusServiceUnavailable, "service_unavailable", "No available Gemini accounts")
				return
			}
			accountReleaseFunc, err = geminiConcurrency.AcquireAccountSlotWithWaitTimeout(
				c,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
				selection.WaitPlan.Timeout,
				false,
				&streamStarted,
			)
			if err != nil {
				reqLog.Warn("gemini.interactions.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				writeErr(http.StatusTooManyRequests, "rate_limit_error", err.Error())
				return
			}
		}
		admissionCtx := service.ContextWithSelectionProfitGate(c.Request.Context(), selection)
		latest, vetoed, reason := h.gatewayService.GatewayProfitControlVetoLatest(admissionCtx, account)
		if vetoed {
			if accountReleaseFunc != nil {
				accountReleaseFunc()
			}
			reqLog.Debug("gemini.interactions.account_profit_vetoed", zap.Int64("account_id", account.ID), zap.String("reason", reason))
			if fs.RecordProfitVeto(account.ID) == FailoverExhausted {
				markOpsRoutingCapacityLimited(c)
				writeErr(http.StatusServiceUnavailable, "service_unavailable", profitVetoExhaustedMessage)
				return
			}
			continue
		}
		account = latest
		accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)
		setOpsSelectedAccount(c, account.ID, account.Platform)

		requestCtx := c.Request.Context()
		if fs.SwitchCount > 0 {
			requestCtx = service.WithAccountSwitchCount(requestCtx, fs.SwitchCount, h.metadataBridgeEnabled())
		}

		res, err := h.geminiCompatService.ForwardInteraction(requestCtx, c, account, modelName, interactionBody, nativePassthrough)
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}
		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				switch fs.HandleFailoverError(c.Request.Context(), h.gatewayService, account.ID, account.Platform, account.GetPoolModeRetryCount(), failoverErr) {
				case FailoverContinue:
					continue
				case FailoverExhausted:
					if nativePassthrough {
						h.handleGeminiFailoverExhausted(c, fs.LastFailoverErr)
					} else {
						writeOpenAISpeechFailoverError(c, fs.LastFailoverErr)
					}
					return
				case FailoverCanceled:
					failoverClientGone(c)
					return
				}
			}
			var clientErr *service.GeminiInteractionClientError
			if errors.As(err, &clientErr) {
				if !nativePassthrough {
					writeOpenAISpeechError(c, clientErr.StatusCode, "invalid_request_error", extractGeminiTTSUpstreamErrorMessage(clientErr.Body))
				}
				return
			}
			reqLog.Error("gemini.interactions.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			return
		}

		// OpenAI 兼容入口需要把解出的 WAV/PCM 二进制音频写回客户端。
		if !nativePassthrough {
			if len(res.WAVAudio) == 0 {
				writeOpenAISpeechError(c, http.StatusBadGateway, "upstream_error", "Upstream Gemini TTS response contained no audio")
				return
			}
			if res.AudioInfo != nil && res.AudioInfo.DurationMs > 0 {
				c.Header("X-Audio-Duration-Ms", strconv.FormatInt(res.AudioInfo.DurationMs, 10))
			}
			if res.ForwardResult != nil {
				c.Header("X-Audio-Output-Tokens", strconv.Itoa(res.ForwardResult.Usage.OutputTokens))
			}
			if openAIFormat == "pcm" {
				pcm, pcmErr := service.ExtractPCMFromWAV(res.WAVAudio)
				if pcmErr != nil {
					writeOpenAISpeechError(c, http.StatusBadGateway, "upstream_error", "Failed to extract PCM audio from upstream WAV")
					return
				}
				c.Data(http.StatusOK, "audio/pcm", pcm)
			} else {
				c.Data(http.StatusOK, "audio/wav", res.WAVAudio)
			}
		}

		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(interactionBody)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
		forceCacheBilling := fs.ForceCacheBilling
		quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
		sessionID := service.ExtractClientSessionID(c)
		forwardResult := res.ForwardResult

		channelUsageFields := clientRequestedUsageFields(c, channelMapping, reqModel, forwardResult.UpstreamModel)
		h.submitUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:             forwardResult,
				QuotaPlatform:      quotaPlatform,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				PricingAt:          pricingAt,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: requestPayloadHash,
				ForceCacheBilling:  forceCacheBilling,
				APIKeyService:      h.apiKeyService,
				SessionID:          sessionID,
				ChannelUsageFields: channelUsageFields,
			}); err != nil {
				logger.L().With(
					zap.String("component", "handler.gemini.interactions"),
					zap.Int64("user_id", authSubject.UserID),
					zap.Int64("api_key_id", apiKey.ID),
					zap.Any("group_id", apiKey.GroupID),
					zap.String("model", modelName),
					zap.Int64("account_id", account.ID),
				).Error("gemini.interactions.record_usage_failed", zap.Error(err))
			}
		})
		return
	}
}

func writeOpenAISpeechError(c *gin.Context, status int, errType, message string) {
	if strings.TrimSpace(errType) == "" {
		errType = "invalid_request_error"
	}
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(status)
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

func writeOpenAISpeechFailoverError(c *gin.Context, failoverErr *service.UpstreamFailoverError) {
	if failoverErr == nil {
		writeOpenAISpeechError(c, http.StatusBadGateway, "upstream_error", "Upstream Gemini TTS request failed")
		return
	}
	status := failoverErr.StatusCode
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	msg := extractGeminiTTSUpstreamErrorMessage(failoverErr.ResponseBody)
	if msg == "" {
		msg = "Upstream Gemini TTS request failed"
	}
	writeOpenAISpeechError(c, status, "upstream_error", msg)
}

func extractGeminiTTSUpstreamErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if gjson.ValidBytes(body) {
		if msg := strings.TrimSpace(gjson.GetBytes(body, "error.message").String()); msg != "" {
			return msg
		}
		if msg := strings.TrimSpace(gjson.GetBytes(body, "message").String()); msg != "" {
			return msg
		}
	}
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 512 {
		return trimmed[:512]
	}
	return trimmed
}
