package service

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/net/http/httpguts"
)

const (
	PinnedCodexTurnStatesExtraKey    = "pinned_codex_turn_states"
	PinnedCodexRoutingCookieExtraKey = "pinned_codex_routing_cookie"
	// PinnedCodexCookieRequireSameEgressExtraKey 为 true 时，只注入在账号当前代理
	// （host:port）上采集的路由 Cookie；来源出口未知或不一致的 Cookie 一律不注入。
	// 默认关闭：Cookie 是否绑定出口 IP 尚无实测结论，按账号灰度开启。
	PinnedCodexCookieRequireSameEgressExtraKey = "pinned_codex_cookie_require_same_egress"
)

type PinnedCodexTurnStateEntry struct {
	State           string     `json:"state"`
	Cookie          string     `json:"cookie,omitempty"`
	CookieExpiresAt *time.Time `json:"cookie_expires_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
	StateLen        int        `json:"state_len,omitempty"`
	// ProxyEndpoint 为采集该 Cookie 时的代理出口 host:port（不含凭据）。
	ProxyEndpoint string `json:"proxy_endpoint,omitempty"`
}

// GetPinnedCodexTurnStates 返回该账号配置的所有 pinned turn-state 映射表。
func (a *Account) GetPinnedCodexTurnStates() map[string]PinnedCodexTurnStateEntry {
	if a == nil || !a.IsOpenAIOAuthLike() || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra[PinnedCodexTurnStatesExtraKey]
	if !ok || raw == nil {
		return nil
	}
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]PinnedCodexTurnStateEntry, len(rawMap))
	for k, v := range rawMap {
		modelKey := strings.ToLower(strings.TrimSpace(k))
		if modelKey == "" {
			continue
		}
		entry := parsePinnedCodexTurnStateEntry(v)
		if entry.State != "" {
			result[modelKey] = entry
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// GetActivePinnedCodexRoutingCookie 返回当前账号级别有效的活 Cookie（用于跨模型粘性路由兜底）。
func (a *Account) GetActivePinnedCodexRoutingCookie() string {
	if a == nil || !a.IsOpenAIOAuthLike() || a.Extra == nil {
		return ""
	}
	// 1. 优先读取账号级独立路由 Cookie
	if raw, ok := a.Extra[PinnedCodexRoutingCookieExtraKey]; ok && raw != nil {
		if rawMap, ok := raw.(map[string]any); ok {
			if cookie, ok := rawMap["cookie"].(string); ok && strings.TrimSpace(cookie) != "" {
				exp := parseFlexibleTime(rawMap["expires_at"])
				if (exp == nil || time.Now().UTC().Before(*exp)) && a.pinnedCookieEgressAllowed(parsePinnedProxyEndpoint(rawMap)) {
					return strings.TrimSpace(cookie)
				}
			}
		}
	}
	// 2. 回退读取同账号未过期模型的活 Cookie：取过期时间最晚者，同值按模型名排序，保证确定性
	states := a.GetPinnedCodexTurnStates()
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var best *PinnedCodexTurnStateEntry
	for _, k := range keys {
		entry := states[k]
		if !a.isPinnedCookieUsable(entry) {
			continue
		}
		if best == nil || pinnedCookieOutlives(entry, *best) {
			e := entry
			best = &e
		}
	}
	if best == nil {
		return ""
	}
	return best.Cookie
}

// pinnedCookieOutlives 判断 a 的 Cookie 是否比 b 更晚过期；未设置过期时间视为最弱。
func pinnedCookieOutlives(a, b PinnedCodexTurnStateEntry) bool {
	if a.CookieExpiresAt == nil {
		return false
	}
	if b.CookieExpiresAt == nil {
		return true
	}
	return a.CookieExpiresAt.After(*b.CookieExpiresAt)
}

func (a *Account) isPinnedCookieUsable(entry PinnedCodexTurnStateEntry) bool {
	return isPinnedCookieActive(entry) && a.pinnedCookieEgressAllowed(entry.ProxyEndpoint)
}

// pinnedCookieEgressAllowed 在账号开启同出口约束时，要求 Cookie 的采集出口与账号当前代理一致。
func (a *Account) pinnedCookieEgressAllowed(endpoint string) bool {
	if a == nil || a.Extra == nil {
		return true
	}
	if required, _ := a.Extra[PinnedCodexCookieRequireSameEgressExtraKey].(bool); !required {
		return true
	}
	if endpoint == "" || a.Proxy == nil || a.Proxy.Host == "" || a.Proxy.Port <= 0 {
		return false
	}
	return strings.EqualFold(endpoint, net.JoinHostPort(a.Proxy.Host, strconv.Itoa(a.Proxy.Port)))
}

// parsePinnedProxyEndpoint 读取 proxy_endpoint；兼容旧数据的完整代理 URL 字段 proxy（仅取 host:port，丢弃凭据）。
func parsePinnedProxyEndpoint(m map[string]any) string {
	if ep, ok := m["proxy_endpoint"].(string); ok && strings.TrimSpace(ep) != "" {
		return strings.TrimSpace(ep)
	}
	raw, ok := m["proxy"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// GetPinnedCodexTurnState 返回匹配指定 model 的未过期 pinned state。
// 若无配置、已过期、非 OpenAI OAuth 账号或不匹配则返回空字符串。
func (a *Account) GetPinnedCodexTurnState(model string) string {
	state, _ := a.GetPinnedCodexTurnStateAndCookie(model)
	return state
}

// GetPinnedCodexTurnStateAndCookie 返回匹配指定 model 的未过期 pinned state 及其关联的有效路由 Cookie。
func (a *Account) GetPinnedCodexTurnStateAndCookie(model string) (string, string) {
	if a == nil || !a.IsOpenAIOAuthLike() || a.Extra == nil {
		return "", ""
	}
	normModel := strings.ToLower(strings.TrimSpace(model))
	if normModel == "" {
		return "", ""
	}
	states := a.GetPinnedCodexTurnStates()
	if len(states) == 0 {
		return "", ""
	}

	var matchedEntry *PinnedCodexTurnStateEntry

	// 1. 精确匹配（最高优先级）
	if entry, ok := states[normModel]; ok {
		if isPinnedStateActive(entry) {
			matchedEntry = &entry
		}
	}

	// 2. 严格的带版本/日期快照别名匹配（按 pattern 长度降序，最长确定性命中）
	if matchedEntry == nil {
		type candidate struct {
			pattern string
			entry   PinnedCodexTurnStateEntry
		}
		var matches []candidate
		for key, entry := range states {
			if matchesPinnedModelPattern(normModel, key) {
				matches = append(matches, candidate{pattern: key, entry: entry})
			}
		}
		if len(matches) > 0 {
			sort.Slice(matches, func(i, j int) bool {
				return len(matches[i].pattern) > len(matches[j].pattern)
			})
			for _, m := range matches {
				if isPinnedStateActive(m.entry) {
					e := m.entry
					matchedEntry = &e
					break
				}
			}
		}
	}

	if matchedEntry == nil {
		return "", ""
	}

	// 提取路由 Cookie：优先该模型自身的 Cookie；若已过期，回退至账号级活 Cookie
	cookie := ""
	if a.isPinnedCookieUsable(*matchedEntry) {
		cookie = matchedEntry.Cookie
	} else {
		cookie = a.GetActivePinnedCodexRoutingCookie()
	}

	return matchedEntry.State, cookie
}

func matchesPinnedModelPattern(model, pattern string) bool {
	if strings.EqualFold(model, pattern) {
		return true
	}
	if strings.HasPrefix(model, pattern+":") {
		return true
	}
	if strings.HasPrefix(model, pattern+"-") {
		rem := strings.TrimPrefix(model, pattern+"-")
		if rem != "" && rem[0] >= '0' && rem[0] <= '9' {
			return true
		}
	}
	return false
}

func isPinnedStateActive(entry PinnedCodexTurnStateEntry) bool {
	if entry.State == "" {
		return false
	}
	if entry.ExpiresAt != nil && !entry.ExpiresAt.IsZero() {
		if time.Now().After(*entry.ExpiresAt) {
			return false
		}
	}
	return true
}

func isPinnedCookieActive(entry PinnedCodexTurnStateEntry) bool {
	if entry.Cookie == "" {
		return false
	}
	if entry.CookieExpiresAt != nil && !entry.CookieExpiresAt.IsZero() {
		if time.Now().After(*entry.CookieExpiresAt) {
			return false
		}
	}
	return true
}

func parsePinnedCodexTurnStateEntry(v any) PinnedCodexTurnStateEntry {
	switch val := v.(type) {
	case string:
		state := strings.TrimSpace(val)
		return PinnedCodexTurnStateEntry{
			State:    state,
			StateLen: len(state),
		}
	case map[string]any:
		state, _ := val["state"].(string)
		state = strings.TrimSpace(state)
		entry := PinnedCodexTurnStateEntry{
			State:    state,
			StateLen: len(state),
		}
		if cookie, ok := val["cookie"].(string); ok {
			entry.Cookie = strings.TrimSpace(cookie)
		} else if cookiesMap, ok := val["cookies"].(map[string]any); ok {
			var parts []string
			for ck, cv := range cookiesMap {
				if s, ok := cv.(string); ok && s != "" {
					parts = append(parts, ck+"="+s)
				}
			}
			sort.Strings(parts)
			entry.Cookie = strings.Join(parts, "; ")
		}
		if exp := parseFlexibleTime(val["cookie_expires_at"]); exp != nil {
			entry.CookieExpiresAt = exp
		}
		if sl, ok := val["state_len"].(float64); ok && sl > 0 {
			entry.StateLen = int(sl)
		} else if sl, ok := val["state_len"].(int); ok && sl > 0 {
			entry.StateLen = sl
		}
		if exp := parseFlexibleTime(val["expires_at"]); exp != nil {
			entry.ExpiresAt = exp
		}
		if upd := parseFlexibleTime(val["updated_at"]); upd != nil {
			entry.UpdatedAt = upd
		}
		entry.ProxyEndpoint = parsePinnedProxyEndpoint(val)
		return entry
	default:
		return PinnedCodexTurnStateEntry{}
	}
}

func parseFlexibleTime(v any) *time.Time {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case time.Time:
		return &t
	case *time.Time:
		return t
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return &parsed
		}
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			return &parsed
		}
	case float64:
		if t > 0 {
			tm := time.Unix(int64(t), 0)
			return &tm
		}
	case int64:
		if t > 0 {
			tm := time.Unix(t, 0)
			return &tm
		}
	}
	return nil
}

// applyPinnedCodexTurnState 若账号在对应 model 上配置了有效 pinned state，
// 则在客户端未自带 turn-state 时将其写入请求头 x-codex-turn-state，并在有活 Cookie 时安全注入路由 Cookie。
// 客户端已自带 turn-state（多轮对话进行中）时保持原样，避免覆盖会话自身状态。
func applyPinnedCodexTurnState(headers http.Header, account *Account, model string) bool {
	if headers == nil || account == nil || strings.TrimSpace(model) == "" {
		return false
	}
	if headers.Get(openAICodexTurnStateHeader) != "" {
		return false
	}
	pinnedState, cookie := account.GetPinnedCodexTurnStateAndCookie(model)
	if pinnedState == "" {
		return false
	}
	// 防御性检查：确保 header 值不含非法字符（如换行符），杜绝任何导致上游请求硬失败的自伤风险
	if !httpguts.ValidHeaderFieldValue(pinnedState) {
		return false
	}
	headers.Set(openAICodexTurnStateHeader, pinnedState)

	// 若存在有效的负载均衡路由凭证（__cflb, __oailb），注入 Cookie 标头（若客户端有旧的路由 cookie，予以更新替换）
	if cookie != "" && httpguts.ValidHeaderFieldValue(cookie) {
		existing := headers.Get("Cookie")
		headers.Set("Cookie", mergeRoutingCookie(existing, cookie))
	}
	return true
}

func mergeRoutingCookie(existing, fresh string) string {
	existing = strings.TrimSpace(existing)
	fresh = strings.TrimSpace(fresh)
	if existing == "" {
		return fresh
	}
	if fresh == "" {
		return existing
	}
	freshMap := make(map[string]bool)
	var freshParts []string
	for _, part := range strings.Split(fresh, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		freshParts = append(freshParts, part)
		if eq := strings.IndexByte(part, '='); eq > 0 {
			k := strings.TrimSpace(part[:eq])
			freshMap[k] = true
		}
	}

	var keptParts []string
	for _, part := range strings.Split(existing, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if eq := strings.IndexByte(part, '='); eq > 0 {
			k := strings.TrimSpace(part[:eq])
			if freshMap[k] {
				continue
			}
		}
		keptParts = append(keptParts, part)
	}
	keptParts = append(keptParts, freshParts...)
	return strings.Join(keptParts, "; ")
}

// pinnedCodexTurnStatesPayload 将 pinned 映射序列化回 extra JSON，保留全部字段（含 Cookie 与采集出口）。
func pinnedCodexTurnStatesPayload(states map[string]PinnedCodexTurnStateEntry) map[string]any {
	payload := make(map[string]any, len(states))
	for k, v := range states {
		m := map[string]any{
			"state":     v.State,
			"state_len": v.StateLen,
		}
		if v.Cookie != "" {
			m["cookie"] = v.Cookie
		}
		if v.CookieExpiresAt != nil {
			m["cookie_expires_at"] = v.CookieExpiresAt.Format(time.RFC3339)
		}
		if v.ExpiresAt != nil {
			m["expires_at"] = v.ExpiresAt.Format(time.RFC3339)
		}
		if v.UpdatedAt != nil {
			m["updated_at"] = v.UpdatedAt.Format(time.RFC3339)
		}
		if v.ProxyEndpoint != "" {
			m["proxy_endpoint"] = v.ProxyEndpoint
		}
		payload[k] = m
	}
	return payload
}

func (s *adminServiceImpl) GetPinnedCodexTurnStates(ctx context.Context, accountID int64) (map[string]PinnedCodexTurnStateEntry, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return account.GetPinnedCodexTurnStates(), nil
}

func (s *adminServiceImpl) SetPinnedCodexTurnState(ctx context.Context, accountID int64, model string, entry PinnedCodexTurnStateEntry) (*Account, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !account.IsOpenAIOAuthLike() {
		return nil, infraerrors.BadRequest("ACCOUNT_NOT_OPENAI_OAUTH", "pinned codex turn state is only supported for OpenAI OAuth accounts")
	}

	normModel := strings.ToLower(strings.TrimSpace(model))
	if normModel == "" {
		return nil, infraerrors.BadRequest("INVALID_MODEL", "model must not be empty")
	}
	state := strings.TrimSpace(entry.State)
	if state == "" {
		return nil, infraerrors.BadRequest("INVALID_STATE", "turn state must not be empty")
	}
	if len(state) > 8192 {
		return nil, infraerrors.BadRequest("STATE_TOO_LONG", "turn state must not exceed 8192 characters")
	}
	if !httpguts.ValidHeaderFieldValue(state) {
		return nil, infraerrors.BadRequest("INVALID_STATE_HEADER", "turn state contains invalid characters for HTTP header")
	}
	if entry.Cookie != "" && !httpguts.ValidHeaderFieldValue(entry.Cookie) {
		return nil, infraerrors.BadRequest("INVALID_COOKIE_HEADER", "cookie contains invalid characters for HTTP header")
	}
	entry.State = state
	if entry.StateLen == 0 {
		entry.StateLen = len(entry.State)
	}
	now := time.Now().UTC()
	entry.UpdatedAt = &now

	currentStates := account.GetPinnedCodexTurnStates()
	if currentStates == nil {
		currentStates = make(map[string]PinnedCodexTurnStateEntry)
	}
	currentStates[normModel] = entry

	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		PinnedCodexTurnStatesExtraKey: pinnedCodexTurnStatesPayload(currentStates),
	}); err != nil {
		return nil, err
	}
	return s.accountRepo.GetByID(ctx, accountID)
}

func (s *adminServiceImpl) SetPinnedCodexTurnStates(ctx context.Context, accountID int64, entries map[string]PinnedCodexTurnStateEntry) (*Account, error) {
	if len(entries) == 0 {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "states must not be empty")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !account.IsOpenAIOAuthLike() {
		return nil, infraerrors.BadRequest("ACCOUNT_NOT_OPENAI_OAUTH", "pinned codex turn state is only supported for OpenAI OAuth accounts")
	}

	for k, entry := range entries {
		normModel := strings.ToLower(strings.TrimSpace(k))
		if normModel == "" {
			return nil, infraerrors.BadRequest("INVALID_MODEL", "model must not be empty")
		}
		state := strings.TrimSpace(entry.State)
		if state == "" {
			return nil, infraerrors.BadRequest("INVALID_STATE", "turn state must not be empty")
		}
		if len(state) > 8192 {
			return nil, infraerrors.BadRequest("STATE_TOO_LONG", "turn state must not exceed 8192 characters")
		}
		if !httpguts.ValidHeaderFieldValue(state) {
			return nil, infraerrors.BadRequest("INVALID_STATE_HEADER", "turn state contains invalid characters for HTTP header")
		}
		if entry.Cookie != "" && !httpguts.ValidHeaderFieldValue(entry.Cookie) {
			return nil, infraerrors.BadRequest("INVALID_COOKIE_HEADER", "cookie contains invalid characters for HTTP header")
		}
	}

	currentStates := account.GetPinnedCodexTurnStates()
	if currentStates == nil {
		currentStates = make(map[string]PinnedCodexTurnStateEntry)
	}
	now := time.Now().UTC()
	for k, entry := range entries {
		normModel := strings.ToLower(strings.TrimSpace(k))
		entry.State = strings.TrimSpace(entry.State)
		if entry.StateLen == 0 {
			entry.StateLen = len(entry.State)
		}
		entry.UpdatedAt = &now
		currentStates[normModel] = entry
	}

	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		PinnedCodexTurnStatesExtraKey: pinnedCodexTurnStatesPayload(currentStates),
	}); err != nil {
		return nil, err
	}
	return s.accountRepo.GetByID(ctx, accountID)
}

func (s *adminServiceImpl) DeletePinnedCodexTurnState(ctx context.Context, accountID int64, model string) (*Account, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	currentStates := account.GetPinnedCodexTurnStates()
	if len(currentStates) == 0 {
		return account, nil
	}
	normModel := strings.ToLower(strings.TrimSpace(model))
	if normModel == "all" || normModel == "*" {
		currentStates = make(map[string]PinnedCodexTurnStateEntry)
	} else {
		delete(currentStates, normModel)
	}

	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		PinnedCodexTurnStatesExtraKey: pinnedCodexTurnStatesPayload(currentStates),
	}); err != nil {
		return nil, err
	}
	return s.accountRepo.GetByID(ctx, accountID)
}
