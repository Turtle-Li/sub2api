package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	attachmentgateway "github.com/Wei-Shaw/sub2api/internal/service/attachment_gateway"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// Basis Points（bps.openai.com，Excel 插件后端）上游：复用 Codex OAuth 令牌与账号池，
// 仅对白名单模型生效。协议转换（工具经 run_officejs 中转、历史重放）由 basispoints 包完成。
// 参与账号与总开关在降智修复面板动态配置（见 openai_bps_config.go），执行情况见 openai_bps_monitor.go。任何在向客户端写出字节之前的失败都会透明回退到原 Codex 请求，
// 由总开关、参与账号列表和按账号熔断三层控制，便于上游策略变化时快速降级。

const (
	bpsOrigin              = "https://bps.openai.com"
	bpsUserAgent           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0"
	bpsErrorBodyLimit      = 4096
	bpsBreakerThreshold    = 3
	bpsBreakerOpenDuration = 10 * time.Minute
	// bpsFirstOutputBudget 是未配置首输出超时时，BPS 从发起到出现首个模型产出事件的上限。
	bpsFirstOutputBudget = 20 * time.Second
	// bpsSessionCooldown 是同一会话 BPS 转换失败后直接走原路径的时长：
	// 同一对话的下一轮通常以相同方式失败，避免每轮都先浪费一次 BPS 往返。
	bpsSessionCooldown    = 3 * time.Minute
	bpsSessionCooldownMax = 10000
	bpsUpstreamEndpoint   = "/basispoints/api/responses"
)

// bpsUpstreamModels 为已完成 Codex 别名归一化后的上游模型名。
// gpt-6-sol / gpt-6-luna 等其余模型一律走原路径。
var bpsUpstreamModels = map[string]struct{}{
	"gpt-6-astra":   {},
	"gpt-5.6-sol":   {},
	"gpt-5.6-terra": {},
	"gpt-5.6-luna":  {},
}

var bpsStaticHeaders = [][2]string{
	{"X-Basispoints-Auth-Mode", "chatgpt"},
	{"Content-Type", "application/json"},
	{"Accept", "text/event-stream"},
	{"Accept-Encoding", "identity"},
	{"Origin", bpsOrigin},
	{"X-OpenAI-Internal-Basispoints-Client-Agent-Profile", "excel"},
	{"X-OpenAI-Internal-Basispoints-Client-Editor", "excel"},
	{"X-OpenAI-Internal-Basispoints-Client-Host", "office"},
	{"X-OpenAI-Internal-Basispoints-Client-Platform", "excel"},
	{"X-OpenAI-Internal-Basispoints-Client-Platform-Class", "PC"},
	{"X-OpenAI-Internal-Basispoints-Client-Product", "basispoints-excel-plugin"},
	{"X-OpenAI-Internal-Basispoints-Client-Runtime", "desktop"},
	{"X-OpenAI-Internal-Basispoints-Office-Host", "Excel"},
	{"X-OpenAI-Internal-Basispoints-Office-Platform", "PC"},
	{"X-Stainless-Lang", "js"},
	{"X-Stainless-Runtime", "browser:chrome"},
	{"User-Agent", bpsUserAgent},
}

func isBPSUpstreamModel(model string) bool {
	_, ok := bpsUpstreamModels[strings.ToLower(strings.TrimSpace(model))]
	return ok
}

// ---- 按账号熔断 ----

type bpsBreakerState struct {
	failures  int
	openUntil time.Time
}

type bpsCircuitBreaker struct {
	mu     sync.Mutex
	states map[int64]*bpsBreakerState
	now    func() time.Time
}

var bpsBreaker = &bpsCircuitBreaker{states: map[int64]*bpsBreakerState{}, now: time.Now}

func (b *bpsCircuitBreaker) allow(accountID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[accountID]
	return state == nil || !b.now().Before(state.openUntil)
}

func (b *bpsCircuitBreaker) recordSuccess(accountID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.states, accountID)
}

// recordFailure 连续失败达到阈值或遇到鉴权/限流类状态码时熔断该账号的 BPS 路径。
func (b *bpsCircuitBreaker) recordFailure(accountID int64, statusCode int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[accountID]
	if state == nil {
		state = &bpsBreakerState{}
		b.states[accountID] = state
	}
	state.failures++
	immediate := statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden || statusCode == http.StatusTooManyRequests
	if immediate || state.failures >= bpsBreakerThreshold {
		state.openUntil = b.now().Add(bpsBreakerOpenDuration)
		state.failures = 0
		return true
	}
	return false
}

// state 返回连续失败数与熔断截止时间（未熔断时为 nil）。
func (b *bpsCircuitBreaker) state(accountID int64) (int, *time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[accountID]
	if state == nil {
		return 0, nil
	}
	if !b.now().Before(state.openUntil) {
		return state.failures, nil
	}
	openUntil := state.openUntil
	return state.failures, &openUntil
}

// ---- 按会话冷却 ----

type bpsSessionCooldownStore struct {
	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time
}

var bpsSessionCooldowns = &bpsSessionCooldownStore{until: map[string]time.Time{}, now: time.Now}

func (c *bpsSessionCooldownStore) mark(scope string) {
	if scope == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.until) >= bpsSessionCooldownMax {
		for key, until := range c.until {
			if !now.Before(until) {
				delete(c.until, key)
			}
		}
		if len(c.until) >= bpsSessionCooldownMax {
			return
		}
	}
	c.until[scope] = now.Add(bpsSessionCooldown)
}

func (c *bpsSessionCooldownStore) active(scope string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.until[scope]
	if ok && !c.now().Before(until) {
		delete(c.until, scope)
		return false
	}
	return ok
}

// ---- 执行记录 ----

// 跳过原因（请求不适用 BPS，直接走原路径，不计入熔断）。
const (
	bpsSkipUnsupportedModel   = "unsupported_model"
	bpsSkipNotOAuth           = "not_oauth"
	bpsSkipCompact            = "compact"
	bpsSkipImageGeneration    = "image_generation"
	bpsSkipMessagesBridge     = "messages_bridge"
	bpsSkipClientToolMapping  = "client_tool_mapping"
	bpsSkipNativeTool         = "native_tool"
	bpsSkipBreakerOpen        = "breaker_open"
	bpsSkipInlineImage        = "inline_image"
	bpsSkipUnsupportedRequest = "unsupported_request"
	bpsSkipSessionCooldown    = "session_cooldown"
)

// bpsRoutineSkips 是按配置或请求类型必然发生的跳过，只计入内存监控，不写 ops 日志。
var bpsRoutineSkips = map[string]bool{
	bpsSkipUnsupportedModel: true,
	bpsSkipNotOAuth:         true,
	bpsSkipCompact:          true,
	bpsSkipImageGeneration:  true,
	bpsSkipMessagesBridge:   true,
}

// 失败原因（BPS 已发起但失败，计入熔断）。
const (
	bpsFailureNetwork    = "network"
	bpsFailureHTTPStatus = "http_status"
	bpsFailureStream     = "stream_before_output"
	bpsFailureHandler    = "handler_before_output"
)

func (a *openAIBPSAttempt) event(outcome, reason, detail string, statusCode int) BPSEvent {
	event := BPSEvent{AccountID: a.accountID, Model: a.upstreamModel, Outcome: outcome, Reason: reason, Detail: detail, StatusCode: statusCode, RequestedEffort: a.effort, AppliedEffort: a.appliedEffort}
	if !a.startedAt.IsZero() {
		event.DurationMs = time.Since(a.startedAt).Milliseconds()
	}
	return event
}

func (a *openAIBPSAttempt) recordSkip(reason, detail string) {
	event := a.event(BPSOutcomeSkipped, reason, detail, 0)
	bpsMonitor.record(event)
	if !bpsRoutineSkips[reason] {
		a.persist(event, false)
	}
}

// persist 把 BPS 结果写入 ops 系统日志（warn 级别才会入库），供事后按账号、模型追查。
func (a *openAIBPSAttempt) persist(event BPSEvent, breakerOpened bool) {
	detail := truncateString(sanitizeUpstreamErrorMessage(event.Detail), 300)
	log := a.log
	if log == nil {
		log = logger.L()
	}
	log.Warn(fmt.Sprintf("[OpenAI BPS] %s (account: %d, reason: %s, status: %d, breaker_open: %v): %s",
		event.Outcome, event.AccountID, event.Reason, event.StatusCode, breakerOpened, detail),
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", event.AccountID),
		zap.String("model", event.Model),
		zap.String("bps_outcome", event.Outcome),
		zap.String("bps_reason", event.Reason),
		zap.Int64("bps_elapsed_ms", event.DurationMs),
	)
}

// recordFailure 记录一次 BPS 失败并推进熔断；afterOutput 表示客户端已收到输出、无法回退。
// 已发起请求后的转换或流失败还会让该会话短暂冷却，网络与状态码类失败交给账号熔断。
func (a *openAIBPSAttempt) recordFailure(reason string, statusCode int, detail string, afterOutput bool) {
	opened := bpsBreaker.recordFailure(a.accountID, statusCode)
	if reason == bpsFailureStream || reason == bpsFailureHandler {
		bpsSessionCooldowns.mark(a.scope)
	}
	outcome := BPSOutcomeFallback
	if afterOutput {
		outcome = BPSOutcomeErrorAfterOutput
	}
	event := a.event(outcome, reason, detail, statusCode)
	bpsMonitor.record(event)
	a.persist(event, opened)
}

func (a *openAIBPSAttempt) recordSuccess() {
	bpsBreaker.recordSuccess(a.accountID)
	bpsMonitor.record(a.event(BPSOutcomeSuccess, "", "", 0))
}

// ---- 图片外链化 ----

// BPSImageExternalizer 把请求体中的内联 data:image 上传到对象存储并替换为预签名 URL。
// BPS 接受 HTTPS image_url，但拒绝内联 data URL（422）。
type BPSImageExternalizer interface {
	Externalize(ctx context.Context, body []byte) attachmentgateway.URLResult
}

func (s *OpenAIGatewayService) SetBPSImageExternalizer(externalizer BPSImageExternalizer) {
	s.bpsImageExternalizer = externalizer
}

var bpsInlineImageMarker = []byte("data:image/")

// ---- 请求 ----

// bpsReplay 保存上游原生工具调用，供下一轮历史还原；键按账号、API key 与线程隔离。
var bpsReplay basispoints.ReplayCache

// openAIBPSAttempt 描述一次可尝试 BPS 的请求；nil 表示本轮直接走原路径。
type openAIBPSAttempt struct {
	accountID int64
	// log 是请求上下文的 logger（携带 request_id 等字段），为 nil 时用全局 logger。
	log           *zap.Logger
	body          []byte
	upstreamModel string
	effort        string
	scope         string
	// appliedEffort 是 BPS 实际使用的档位（max→xhigh），计费以此为准。
	appliedEffort string
	startedAt     time.Time
	// clientStream 是客户端是否请求流式响应；非流式预读到终态再放行。
	clientStream bool
	// budgetDeadline 非零时为首输出超时预算中留给 BPS 的截止时间，其余留给原路径回退。
	budgetDeadline time.Time
}

// holdDeadline 返回预读放行的绝对截止时间：到点后已有产出即放行给客户端。
func (a *openAIBPSAttempt) holdDeadline() time.Time {
	deadline := a.startedAt.Add(bpsHoldLimit)
	if !a.budgetDeadline.IsZero() && a.budgetDeadline.Before(deadline) {
		deadline = a.budgetDeadline
	}
	return deadline
}

// outputDeadline 返回等待响应头与首个模型产出事件的截止时间，超出即回退原路径。
// 配置了首输出超时时取其留给 BPS 的一半，否则取 bpsFirstOutputBudget。
func (a *openAIBPSAttempt) outputDeadline() time.Time {
	deadline := a.startedAt.Add(bpsFirstOutputBudget)
	if !a.budgetDeadline.IsZero() {
		deadline = a.budgetDeadline
	}
	if hold := a.holdDeadline(); hold.Before(deadline) {
		deadline = hold
	}
	return deadline
}

// openAIBPSAttemptFor 判定请求是否可以尝试 BPS。只做廉价检查，不触碰网络。
// 仅对总开关开启且在参与列表中的账号生效；不适用的请求记为跳过。
func (s *OpenAIGatewayService) openAIBPSAttemptFor(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	upstreamModel string,
	effort string,
	isCompactRequest bool,
	imageIntent bool,
	compatMessagesBridge bool,
) *openAIBPSAttempt {
	if account == nil || account.IsShadow() || account.IsOpenAIAgentIdentity() {
		return nil
	}
	enabled, listed, liveSearch := s.settingService.isOpenAIBPSUpstreamAccount(ctx, account.ID)
	if !enabled || !listed {
		return nil
	}
	attempt := &openAIBPSAttempt{accountID: account.ID, log: logger.FromContext(ctx), body: body, upstreamModel: upstreamModel, effort: effort}
	skip, detail := "", ""
	switch {
	case !account.IsOpenAIOAuth():
		skip = bpsSkipNotOAuth
	case !isBPSUpstreamModel(upstreamModel):
		skip = bpsSkipUnsupportedModel
	case isCompactRequest:
		skip = bpsSkipCompact
	case imageIntent || openAIRequestBodyHasImageGenerationDeclaration(body):
		skip = bpsSkipImageGeneration
	case compatMessagesBridge:
		skip = bpsSkipMessagesBridge
	}
	apiKeyID := getAPIKeyIDFromContext(c)
	if skip == "" {
		identity, _ := resolveOpenAIWSExecutionScope(c, body, apiKeyID)
		attempt.scope = fmt.Sprintf("account:%d/key:%d/thread:%s", account.ID, apiKeyID, identity)
		if _, ok := openAIResponsesClientToolMapping(c); ok {
			skip = bpsSkipClientToolMapping
		} else if reason := basispoints.NativeFallbackReason(body, liveSearch); reason != "" {
			skip, detail = bpsSkipNativeTool, reason
		} else if !bpsBreaker.allow(account.ID) {
			skip = bpsSkipBreakerOpen
		} else if bpsSessionCooldowns.active(attempt.scope) {
			skip = bpsSkipSessionCooldown
		}
	}
	if skip != "" {
		attempt.recordSkip(skip, detail)
		return nil
	}
	return attempt
}

// doOpenAIUpstreamPreferBPS 优先尝试 BPS；在向客户端写出任何字节之前的失败
// 透明回退为原始 upstreamReq。返回的 attempt 非 nil 表示 resp 来自 BPS。
func (s *OpenAIGatewayService) doOpenAIUpstreamPreferBPS(
	upstreamReq *http.Request,
	proxyURL string,
	account *Account,
	token string,
	attempt *openAIBPSAttempt,
) (*http.Response, *openAIBPSAttempt, error) {
	if attempt != nil {
		if resp, ok := s.tryOpenAIBPSUpstream(upstreamReq.Context(), account, token, attempt); ok {
			return resp, attempt, nil
		}
	}
	resp, err := s.doOpenAIUpstream(upstreamReq, proxyURL, account)
	return resp, nil, err
}

func (s *OpenAIGatewayService) tryOpenAIBPSUpstream(parent context.Context, account *Account, token string, attempt *openAIBPSAttempt) (*http.Response, bool) {
	attempt.startedAt = time.Now()
	deadline := attempt.holdDeadline()
	outputDeadline := attempt.outputDeadline()
	// 等待 BPS 响应头受首产出截止约束；拿到响应头后不再取消，流由 bpsPrimedBody 关闭时释放。
	ctx, cancel := context.WithCancel(parent)
	headerTimer := time.AfterFunc(time.Until(outputDeadline), cancel)
	released := false
	defer func() {
		if !released {
			headerTimer.Stop()
			cancel()
		}
	}()
	body := attempt.body
	if bytes.Contains(body, bpsInlineImageMarker) {
		if s.bpsImageExternalizer == nil {
			attempt.recordSkip(bpsSkipInlineImage, "image externalizer unavailable")
			return nil, false
		}
		result := s.bpsImageExternalizer.Externalize(ctx, body)
		if bytes.Contains(result.Body, bpsInlineImageMarker) {
			// 外链化失败或部分失败：不计入熔断，直接走原路径。
			attempt.recordSkip(bpsSkipInlineImage, fmt.Sprintf("images: %d, externalized: %d, errors: %d",
				result.Metrics.ImageCount, result.Metrics.ExternalizedCount, result.Metrics.Errors))
			return nil, false
		}
		body = result.Body
	}
	bpsBody, bridge, err := prepareBPSRequestBody(body, attempt)
	if err != nil {
		// 请求形态不受 BPS 支持（结构化输出、item_reference 等）：不计入熔断。
		attempt.recordSkip(bpsSkipUnsupportedRequest, err.Error())
		return nil, false
	}
	attempt.appliedEffort = bridge.Effort

	for retriedEncrypted := false; ; retriedEncrypted = true {
		resp, statusCode, errBody, err := s.sendOpenAIBPSRequest(ctx, account, token, bpsBody)
		if err != nil {
			attempt.recordFailure(bpsFailureNetwork, 0, err.Error(), false)
			return nil, false
		}
		if statusCode == http.StatusBadRequest && !retriedEncrypted && extractUpstreamErrorCode(errBody) == "invalid_encrypted_content" {
			if stripped, ok := bpsStripEncryptedReasoning(bpsBody); ok {
				bpsBody = stripped
				continue
			}
		}
		if resp == nil {
			attempt.recordFailure(bpsFailureHTTPStatus, statusCode, extractUpstreamErrorMessage(errBody), false)
			return nil, false
		}
		if !headerTimer.Stop() {
			_ = resp.Body.Close()
			attempt.recordFailure(bpsFailureNetwork, 0, "response headers arrived after the BPS deadline", false)
			return nil, false
		}
		stream := newBPSBridgeStream(bridge, resp.Body, attempt.clientStream, deadline)
		stream.onClose = cancel
		stream.outputDeadline = outputDeadline
		if ok, reason := stream.primeUntilOutput(); !ok {
			_ = stream.Close()
			attempt.recordFailure(bpsFailureStream, 0, reason, false)
			return nil, false
		}
		released = true
		resp.Body = stream
		return resp, true
	}
}

// prepareBPSRequestBody 写入上游模型名与回退 effort 后交给 basispoints 转换。
// max 等 BPS 不支持的档位由 basispoints.NormalizeEffort 映射为 xhigh。
func prepareBPSRequestBody(body []byte, attempt *openAIBPSAttempt) ([]byte, *basispoints.Bridge, error) {
	body, err := sjson.SetBytes(body, "model", attempt.upstreamModel)
	if err != nil {
		return nil, nil, err
	}
	if attempt.effort != "" && strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String()) == "" && !gjson.GetBytes(body, "reasoning_effort").Exists() {
		if body, err = sjson.SetBytes(body, "reasoning.effort", attempt.effort); err != nil {
			return nil, nil, err
		}
	}
	return basispoints.Prepare(body, attempt.scope, &bpsReplay)
}

// sendOpenAIBPSRequest 发送一次 BPS 请求。2xx 且为 SSE 时返回 resp；
// 否则返回状态码与截断的错误体（resp 为 nil，连接已关闭）。
func (s *OpenAIGatewayService) sendOpenAIBPSRequest(ctx context.Context, account *Account, token string, payload []byte) (*http.Response, int, []byte, error) {
	req, err := http.NewRequestWithContext(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI), http.MethodPost, basispoints.ResponsesURL, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, nil, err
	}
	authHeaders, err := s.buildOpenAIAuthenticationHeaders(ctx, account, token)
	if err != nil {
		return nil, 0, nil, err
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
		return nil, 0, nil, err
	}
	chatgptAccountID := strings.TrimSpace(req.Header.Get("chatgpt-account-id"))
	if chatgptAccountID == "" {
		return nil, 0, nil, errors.New("missing chatgpt account id")
	}
	req.Header.Set("X-OpenAI-Account-ID", chatgptAccountID)
	for _, header := range bpsStaticHeaders {
		req.Header.Set(header[0], header[1])
	}

	proxyURL, err := ResolveAccountProxyURL(account)
	if err != nil {
		return nil, 0, nil, err
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, 0, nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && isEventStreamResponse(resp.Header) {
		return resp, resp.StatusCode, nil, nil
	}
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, bpsErrorBodyLimit))
	_ = resp.Body.Close()
	statusCode := resp.StatusCode
	if statusCode >= 200 && statusCode < 300 {
		errBody = []byte("non event-stream success response")
	}
	return nil, statusCode, errBody, nil
}

// bpsStripEncryptedReasoning 删除带 encrypted_content 的推理条目，用于 invalid_encrypted_content 重试。
func bpsStripEncryptedReasoning(payload []byte) ([]byte, bool) {
	items := gjson.GetBytes(payload, "input").Array()
	kept := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		if item.Get("type").String() == "reasoning" && item.Get("encrypted_content").String() != "" {
			continue
		}
		kept = append(kept, json.RawMessage(item.Raw))
	}
	if len(kept) == len(items) {
		return nil, false
	}
	stripped, err := sjson.SetBytes(payload, "input", kept)
	return stripped, err == nil
}
