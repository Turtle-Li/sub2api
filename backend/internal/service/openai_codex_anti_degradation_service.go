package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

const (
	DefaultAntiDegradationInterval      = 1 * time.Hour
	DefaultAntiDegradationSourceID      = int64(15)
	DefaultAntiDegradationProbeModel    = "gpt-6-astra"
	DefaultAntiDegradationClientVersion = "0.154.0"
	DefaultAntiDegradationUserAgent     = "codex-tui/0.154.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	DefaultWestUSProxyURL               = "http://faf0f4f404d7:6455cfc8e603a1fc69280f02b6735f8c240b7335d334d23e@100.67.153.111:7890"
	CodexResponsesEndpoint             = "https://chatgpt.com/backend-api/codex/responses"

	// TakaichiProbePrompt 严格用于识别 Tier 3（超满血，知识库截止 2025.12）实例。
	// 只有回答包含“高市”的实例才具备未剪枝、长思维链满血推理能力（通过鹈鹕等复杂基准测试）。
	// 石破茂（Tier 2，剪枝版）或岸田文雄（Tier 1，降智）均在此阶段被严格过滤剔除。
	TakaichiProbePrompt     = "不要联网，不要调用工具，直接回答，你记忆中的日本首相是谁？"
	TakaichiExpectedKeyword = "高市"
	MaxHarvestAttempts      = 25
)

var defaultAntiDegradationModels = []string{
	"gpt-6-astra",
	"gpt-5.6-sol",
	"gpt-6-luna",
	"gpt-5.6-terra",
	"gpt-5.5",
	"gpt-5.4-mini",
}

var defaultProtectedAccountIDs = []int64{15, 69, 9, 56}

// OpenAICodexAntiDegradationService 在 Sub2API 内部自动守护 OpenAI/Codex 账号，
// 周期性（默认 1 小时）通过西美干净出口代理（Proxy 40）探测并获取活的 Phoenix 满血集群
// 路由 Cookie（__oailb, __cflb），为受保护的目标账号生成 780 字节满血防降智票据并持久化写入。
type OpenAICodexAntiDegradationService struct {
	accountRepo       AccountRepository
	proxyRepo         ProxyRepository
	interval          time.Duration
	sourceAccountID   int64
	targetAccountIDs  []int64
	models            []string
	leaderLock        *singletonJobLock
	stopCh            chan struct{}
	stopOnce          sync.Once
	wg                sync.WaitGroup
	syncMu            sync.Mutex
	lastSyncTime      time.Time
	lastSyncStatus    string
	lastHarvestHost   string
	lastRenewedCounts int
}

func NewOpenAICodexAntiDegradationService(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	interval time.Duration,
) *OpenAICodexAntiDegradationService {
	if interval <= 0 {
		interval = DefaultAntiDegradationInterval
	}
	return &OpenAICodexAntiDegradationService{
		accountRepo:      accountRepo,
		proxyRepo:        proxyRepo,
		interval:         interval,
		sourceAccountID:  DefaultAntiDegradationSourceID,
		targetAccountIDs: append([]int64(nil), defaultProtectedAccountIDs...),
		models:           append([]string(nil), defaultAntiDegradationModels...),
		stopCh:           make(chan struct{}),
	}
}

func (s *OpenAICodexAntiDegradationService) Start() {
	if s == nil || s.accountRepo == nil || s.interval <= 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		// 启动时延迟 30 秒执行一次初始续期，避免和系统启动高峰并发
		select {
		case <-time.After(30 * time.Second):
			s.runOnceSafe()
		case <-s.stopCh:
			return
		}

		for {
			select {
			case <-ticker.C:
				s.runOnceSafe()
			case <-s.stopCh:
				return
			}
		}
	}()
	slog.Info("[AntiDegradation] Service started", "interval", s.interval.String(), "source_account_id", s.sourceAccountID)
}

func (s *OpenAICodexAntiDegradationService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.wg.Wait()
	slog.Info("[AntiDegradation] Service stopped")
}

func (s *OpenAICodexAntiDegradationService) runOnceSafe() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if s.leaderLock != nil {
		lockedCtx, release, ok := s.leaderLock.try(ctx)
		if !ok {
			return
		}
		defer release()
		ctx = lockedCtx
	}

	if err := s.SyncOnce(ctx); err != nil {
		slog.Error("[AntiDegradation] Sync failed", "error", err)
	}
}

// SyncOnce 执行一轮完整的防降智探测与票据续期流程。
func (s *OpenAICodexAntiDegradationService) SyncOnce(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	slog.Info("[AntiDegradation] Starting anti-degradation renewal cycle...")
	startTime := time.Now()

	// 1. 获取源账号（通常为 Plus 账号，作为洁净 Cookie 来源）
	sourceAcc, err := s.accountRepo.GetByID(ctx, s.sourceAccountID)
	if err != nil {
		s.lastSyncStatus = fmt.Sprintf("failed to get source account %d: %v", s.sourceAccountID, err)
		return fmt.Errorf("failed to get source account %d: %w", s.sourceAccountID, err)
	}

	sourceToken := sourceAcc.GetCredential("access_token")
	sourceAccountUID := sourceAcc.GetCredential("account_id")
	if sourceToken == "" {
		s.lastSyncStatus = "source account has no access_token"
		return fmt.Errorf("source account %d has no access_token", s.sourceAccountID)
	}

	proxyURL := s.resolveProxyURL(ctx, sourceAcc)
	if proxyURL == "" {
		proxyURL = DefaultWestUSProxyURL
	}

	// 2. 从源账号探测收集活的 Cloudflare 路由 Cookie（__oailb, __cflb）
	harvestedCookie, clusterHost, err := s.harvestRoutingCookie(ctx, sourceToken, sourceAccountUID, proxyURL)
	if err != nil {
		s.lastSyncStatus = fmt.Sprintf("cookie harvest failed: %v", err)
		return fmt.Errorf("failed to harvest routing cookie: %w", err)
	}
	s.lastHarvestHost = clusterHost
	slog.Info("[AntiDegradation] Harvested routing cookie successfully", "cluster_host", clusterHost, "proxy", proxyURL)

	// 3. 收集需要受保护的目标账号
	targetIDs := s.getTargetAccountIDs(ctx)
	renewedCount := 0

	now := time.Now().UTC()
	// Cookie 预期 1 小时失效，票据留存 2 小时
	cookieExpiresAt := now.Add(1 * time.Hour)
	stateExpiresAt := now.Add(2 * time.Hour)

	for _, targetID := range targetIDs {
		targetAcc, err := s.accountRepo.GetByID(ctx, targetID)
		if err != nil || targetAcc == nil {
			slog.Warn("[AntiDegradation] Skip target account: not found", "account_id", targetID)
			continue
		}
		if !targetAcc.IsOpenAIOAuthLike() {
			continue
		}

		targetToken := targetAcc.GetCredential("access_token")
		targetAccountUID := targetAcc.GetCredential("account_id")
		if targetToken == "" {
			continue
		}

		targetProxy := s.resolveProxyURL(ctx, targetAcc)
		if targetProxy == "" {
			targetProxy = proxyURL
		}

		// 使用收获的 Cookie 引导目标账号生成自身专属的 780 字节满血票据
		turnState, freshCookie, err := s.bootstrapAccount(ctx, targetToken, targetAccountUID, harvestedCookie, targetProxy)
		if err != nil {
			slog.Warn("[AntiDegradation] Bootstrap failed for account", "account_id", targetID, "error", err)
			continue
		}

		finalCookie := harvestedCookie
		if freshCookie != "" {
			finalCookie = freshCookie
		}

		// 4. 持久化存入账号 extra
		if err := s.persistAntiDegradationData(ctx, targetID, turnState, finalCookie, targetProxy, now, stateExpiresAt, cookieExpiresAt); err != nil {
			slog.Error("[AntiDegradation] Failed to persist data for account", "account_id", targetID, "error", err)
			continue
		}

		renewedCount++
		slog.Info("[AntiDegradation] Account renewed successfully",
			"account_id", targetID,
			"account_name", targetAcc.Name,
			"turn_state_len", len(turnState),
			"cluster", clusterHost,
		)
	}

	s.lastSyncTime = time.Now()
	s.lastRenewedCounts = renewedCount
	s.lastSyncStatus = fmt.Sprintf("ok (renewed %d accounts in %v)", renewedCount, time.Since(startTime))
	slog.Info("[AntiDegradation] Renewal cycle completed", "renewed_count", renewedCount, "duration", time.Since(startTime))
	return nil
}

func (s *OpenAICodexAntiDegradationService) harvestRoutingCookie(
	ctx context.Context,
	token, accountUID, proxyURL string,
) (string, string, error) {
	for attempt := 1; attempt <= MaxHarvestAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		cookieStr, clusterHost, text, cflbSuffix, err := s.tryHarvestTakaichiCookie(ctx, token, accountUID, proxyURL)
		if err != nil {
			slog.Warn("[AntiDegradation] Harvest attempt encountered network/upstream error",
				"attempt", attempt,
				"error", err,
			)
			time.Sleep(1 * time.Second)
			continue
		}

		if strings.Contains(text, TakaichiExpectedKeyword) {
			slog.Info("[AntiDegradation] SUCCESS: Harvested verified Takaichi Sanae cookie!",
				"attempt", attempt,
				"cflb_suffix", cflbSuffix,
				"cluster_host", clusterHost,
				"ans_snippet", truncateString(text, 60),
			)
			return cookieStr, clusterHost, nil
		}

		tier := "Tier 1 (Degraded/Kishida)"
		if strings.Contains(text, "石破") {
			tier = "Tier 2 (Pruned/Ishiba)"
		}
		slog.Info("[AntiDegradation] Discarded non-Takaichi cluster instance, continuing search for Takaichi Sanae...",
			"attempt", attempt,
			"tier", tier,
			"cflb_suffix", cflbSuffix,
			"ans_snippet", truncateString(text, 50),
		)

		// 每次重试前留出短暂间隔，促使负载均衡器分流至不同后端机
		time.Sleep(1200 * time.Millisecond)
	}

	return "", "", fmt.Errorf("failed to harvest verified Takaichi Sanae cookie after %d attempts", MaxHarvestAttempts)
}

func (s *OpenAICodexAntiDegradationService) tryHarvestTakaichiCookie(
	ctx context.Context,
	token, accountUID, proxyURL string,
) (string, string, string, string, error) {
	client, err := s.buildHTTPClient(proxyURL, 20*time.Second)
	if err != nil {
		return "", "", "", "", err
	}

	probePayload := map[string]any{
		"model": DefaultAntiDegradationProbeModel,
		"input": []map[string]any{
			{"role": "user", "content": []map[string]any{{"type": "input_text", "text": TakaichiProbePrompt}}},
		},
		"stream":       true,
		"store":        false,
		"instructions": "You are a coding assistant.",
	}
	bodyBytes, _ := json.Marshal(probePayload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, CodexResponsesEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", "", "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if accountUID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountUID)
	}
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("User-Agent", DefaultAntiDegradationUserAgent)
	req.Header.Set("Version", DefaultAntiDegradationClientVersion)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", "", "", fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	cookies := parseCookiesFromHeader(resp.Header)
	oailb, hasOailb := cookies["__oailb"]
	if !hasOailb || oailb == "" {
		return "", "", "", "", fmt.Errorf("no __oailb in upstream response cookies")
	}

	cflb := cookies["__cflb"]
	var cookieParts []string
	if cflb != "" {
		cookieParts = append(cookieParts, "__cflb="+cflb)
	}
	cookieParts = append(cookieParts, "__oailb="+oailb)
	cookieStr := strings.Join(cookieParts, "; ")
	clusterHost := extractClusterHostFromOailb(oailb)

	cflbSuffix := ""
	if len(cflb) > 12 {
		cflbSuffix = cflb[len(cflb)-12:]
	} else {
		cflbSuffix = cflb
	}

	// 消费 SSE 流并提取回答文本判定是否为高市早苗
	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			msgType := gjson.Get(data, "type").String()
			if msgType == "response.output_text.delta" {
				sb.WriteString(gjson.Get(data, "delta").String())
			} else if gjson.Get(data, "delta.text").Exists() {
				sb.WriteString(gjson.Get(data, "delta.text").String())
			}
		}
	}

	return cookieStr, clusterHost, sb.String(), cflbSuffix, nil
}

func (s *OpenAICodexAntiDegradationService) bootstrapAccount(
	ctx context.Context,
	token, accountUID, cookieStr, proxyURL string,
) (string, string, error) {
	client, err := s.buildHTTPClient(proxyURL, 25*time.Second)
	if err != nil {
		return "", "", err
	}

	probePayload := map[string]any{
		"model": DefaultAntiDegradationProbeModel,
		"input": []map[string]any{
			{"role": "user", "content": []map[string]any{{"type": "input_text", "text": TakaichiProbePrompt}}},
		},
		"stream":       true,
		"store":        false,
		"instructions": "You are a coding assistant.",
	}
	bodyBytes, _ := json.Marshal(probePayload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, CodexResponsesEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if accountUID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountUID)
	}
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("User-Agent", DefaultAntiDegradationUserAgent)
	req.Header.Set("Version", DefaultAntiDegradationClientVersion)
	req.Header.Set("Cookie", cookieStr)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	turnState := strings.TrimSpace(resp.Header.Get("x-codex-turn-state"))

	// 消费流并尝试从 response.completed 兜底提取 turn_state
	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			msgType := gjson.Get(data, "type").String()
			if msgType == "response.output_text.delta" {
				sb.WriteString(gjson.Get(data, "delta").String())
			} else if msgType == "response.completed" {
				if turnState == "" {
					turnState = strings.TrimSpace(gjson.Get(data, "response.turn_state").String())
				}
			}
		}
	}

	if turnState == "" {
		return "", "", fmt.Errorf("upstream returned empty turn-state (status %d)", resp.StatusCode)
	}
	if len(turnState) < 600 {
		return "", "", fmt.Errorf("upstream returned degraded turn-state (len=%d, expected ~780)", len(turnState))
	}

	respCookies := parseCookiesFromHeader(resp.Header)
	freshCookie := ""
	if newOailb, ok := respCookies["__oailb"]; ok && newOailb != "" {
		var parts []string
		if newCflb, ok := respCookies["__cflb"]; ok && newCflb != "" {
			parts = append(parts, "__cflb="+newCflb)
		}
		parts = append(parts, "__oailb="+newOailb)
		freshCookie = strings.Join(parts, "; ")
	}

	ans := sb.String()
	slog.Info("[AntiDegradation] Target account bootstrapped on Takaichi cluster",
		"account_uid", accountUID,
		"ans_snippet", truncateString(ans, 50),
		"turn_state_len", len(turnState),
	)

	return turnState, freshCookie, nil
}


func (s *OpenAICodexAntiDegradationService) persistAntiDegradationData(
	ctx context.Context,
	accountID int64,
	turnState, cookieStr, proxyURL string,
	now, stateExpiresAt, cookieExpiresAt time.Time,
) error {
	nowISO := now.Format(time.RFC3339Nano)
	stateExpISO := stateExpiresAt.Format(time.RFC3339Nano)
	cookieExpISO := cookieExpiresAt.Format(time.RFC3339Nano)

	pinnedRoutingCookie := map[string]any{
		"cookie":     cookieStr,
		"proxy":      proxyURL,
		"updated_at": nowISO,
		"expires_at": cookieExpISO,
	}

	pinnedTurnStates := make(map[string]any, len(s.models))
	for _, m := range s.models {
		pinnedTurnStates[strings.ToLower(m)] = map[string]any{
			"state":             turnState,
			"state_len":         len(turnState),
			"cookie":            cookieStr,
			"cookie_expires_at": cookieExpISO,
			"updated_at":        nowISO,
			"expires_at":        stateExpISO,
		}
	}

	updates := map[string]any{
		PinnedCodexRoutingCookieExtraKey: pinnedRoutingCookie,
		PinnedCodexTurnStatesExtraKey:    pinnedTurnStates,
	}

	return s.accountRepo.UpdateExtra(ctx, accountID, updates)
}

func (s *OpenAICodexAntiDegradationService) getTargetAccountIDs(ctx context.Context) []int64 {
	seen := make(map[int64]bool)
	var ids []int64
	for _, id := range s.targetAccountIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *OpenAICodexAntiDegradationService) resolveProxyURL(ctx context.Context, acc *Account) string {
	if acc == nil || acc.ProxyID == nil || s.proxyRepo == nil {
		return ""
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *acc.ProxyID)
	if err != nil || proxy == nil {
		return ""
	}
	return proxy.URL()
}

func (s *OpenAICodexAntiDegradationService) buildHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: false},
		ForceAttemptHTTP2: true,
	}
	if strings.TrimSpace(proxyURL) != "" {
		pu, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}, nil
}

func parseCookiesFromHeader(header http.Header) map[string]string {
	result := make(map[string]string)
	for _, sc := range header["Set-Cookie"] {
		part := strings.Split(sc, ";")[0]
		if eq := strings.IndexByte(part, '='); eq > 0 {
			k := strings.TrimSpace(part[:eq])
			v := strings.TrimSpace(part[eq+1:])
			result[k] = v
		}
	}
	return result
}

func extractClusterHostFromOailb(oailb string) string {
	parts := strings.Split(oailb, ".")
	if len(parts) < 2 {
		return "unknown"
	}
	payloadB64 := parts[1]
	if rem := len(payloadB64) % 4; rem != 0 {
		payloadB64 += strings.Repeat("=", 4-rem)
	}
	data, err := base64.URLEncoding.DecodeString(payloadB64)
	if err != nil {
		data, err = base64.RawURLEncoding.DecodeString(parts[1])
	}
	if err == nil {
		return gjson.GetBytes(data, "host").String()
	}
	return "unknown"
}

// GetStatus 返回当前防降智守护服务的运行状态信息。
func (s *OpenAICodexAntiDegradationService) GetStatus() map[string]any {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	return map[string]any{
		"interval":            s.interval.String(),
		"source_account_id":   s.sourceAccountID,
		"target_account_ids":  s.targetAccountIDs,
		"last_sync_time":      s.lastSyncTime.Format(time.RFC3339),
		"last_sync_status":    s.lastSyncStatus,
		"last_harvest_host":   s.lastHarvestHost,
		"last_renewed_counts": s.lastRenewedCounts,
	}
}
