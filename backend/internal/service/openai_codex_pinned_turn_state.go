package service

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/net/http/httpguts"
)

const (
	PinnedCodexTurnStatesExtraKey    = "pinned_codex_turn_states"
	PinnedCodexRoutingCookieExtraKey = "pinned_codex_routing_cookie"
)

type PinnedCodexTurnStateEntry struct {
	State           string     `json:"state"`
	Cookie          string     `json:"cookie,omitempty"`
	CookieExpiresAt *time.Time `json:"cookie_expires_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
	StateLen        int        `json:"state_len,omitempty"`
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
				if exp == nil || time.Now().UTC().Before(*exp) {
					return strings.TrimSpace(cookie)
				}
			}
		}
	}
	// 2. 回退读取同账号任一未过期模型的活 Cookie
	states := a.GetPinnedCodexTurnStates()
	for _, entry := range states {
		if entry.Cookie != "" {
			if isPinnedCookieActive(entry) {
				return entry.Cookie
			}
		}
	}
	return ""
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
	if isPinnedCookieActive(*matchedEntry) {
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
// 则在客户端未自带 turn-state 时（第 1 轮冷启动）将其安全写入请求头 x-codex-turn-state。
// 若客户端已自带 turn-state（多轮对话持续中），必须保留客户端自身的会话状态，避免会话错位导致上游降级。
// 无论是否注入 turn-state，只要账号存在有效路由 Cookie，均予以安全合并注入以保证流量锁定在正确片区。
func applyPinnedCodexTurnState(headers http.Header, account *Account, model string) bool {
	if headers == nil || account == nil || strings.TrimSpace(model) == "" {
		return false
	}
	pinnedState, cookie := account.GetPinnedCodexTurnStateAndCookie(model)
	if cookie == "" {
		cookie = account.GetActivePinnedCodexRoutingCookie()
	}

	applied := false
	// 防御性检查：确保 header 值不含非法字符（如换行符），杜绝任何导致上游请求硬失败的自伤风险。
	// 仅在客户端未自带 turn-state 时注入（第 1 轮冷启动防降智）；多轮对话保留客户端自有 turn-state。
	if headers.Get(openAICodexTurnStateHeader) == "" {
		if pinnedState != "" && httpguts.ValidHeaderFieldValue(pinnedState) {
			headers.Set(openAICodexTurnStateHeader, pinnedState)
			applied = true
		}
	}

	// 若存在有效的负载均衡路由凭证（__cflb, __oailb），注入 Cookie 标头（若客户端有旧的路由 cookie，予以更新替换）
	if cookie != "" && httpguts.ValidHeaderFieldValue(cookie) {
		existing := headers.Get("Cookie")
		headers.Set("Cookie", mergeRoutingCookie(existing, cookie))
		applied = true
	}
	return applied
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

	payloadMap := make(map[string]any, len(currentStates))
	for k, v := range currentStates {
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
		payloadMap[k] = m
	}

	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		PinnedCodexTurnStatesExtraKey: payloadMap,
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

	payloadMap := make(map[string]any, len(currentStates))
	for k, v := range currentStates {
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
		payloadMap[k] = m
	}

	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		PinnedCodexTurnStatesExtraKey: payloadMap,
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

	payloadMap := make(map[string]any, len(currentStates))
	for k, v := range currentStates {
		m := map[string]any{
			"state":     v.State,
			"state_len": v.StateLen,
		}
		if v.ExpiresAt != nil {
			m["expires_at"] = v.ExpiresAt.Format(time.RFC3339)
		}
		if v.UpdatedAt != nil {
			m["updated_at"] = v.UpdatedAt.Format(time.RFC3339)
		}
		payloadMap[k] = m
	}

	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		PinnedCodexTurnStatesExtraKey: payloadMap,
	}); err != nil {
		return nil, err
	}
	return s.accountRepo.GetByID(ctx, accountID)
}
