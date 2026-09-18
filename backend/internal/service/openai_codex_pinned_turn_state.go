package service

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

const PinnedCodexTurnStatesExtraKey = "pinned_codex_turn_states"

type PinnedCodexTurnStateEntry struct {
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	StateLen  int        `json:"state_len,omitempty"`
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

// GetPinnedCodexTurnState 返回匹配指定 model 的未过期 pinned state。
// 若无配置、已过期、非 OpenAI OAuth 账号或不匹配则返回空字符串。
func (a *Account) GetPinnedCodexTurnState(model string) string {
	if a == nil || !a.IsOpenAIOAuthLike() || a.Extra == nil {
		return ""
	}
	normModel := strings.ToLower(strings.TrimSpace(model))
	if normModel == "" {
		return ""
	}
	states := a.GetPinnedCodexTurnStates()
	if len(states) == 0 {
		return ""
	}

	// 1. 精确匹配（最高优先级）
	if entry, ok := states[normModel]; ok {
		if isPinnedStateActive(entry) {
			return entry.State
		}
		return ""
	}

	// 2. 严格的带版本/日期快照别名匹配（按 pattern 长度降序，最长确定性命中）
	// 例如：配置了 gpt-6-astra，请求模型为 gpt-6-astra-20260301（后缀为数字日期）可以安全命中；
	// 但配置 gpt-6 绝不能命中 gpt-6-astra，配置 gpt-5 绝不能命中 gpt-5-codex。
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
	if len(matches) == 0 {
		return ""
	}
	// 按 pattern 长度降序排序（最长最精确的优先）
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i].pattern) > len(matches[j].pattern)
	})
	for _, m := range matches {
		if isPinnedStateActive(m.entry) {
			return m.entry.State
		}
	}
	return ""
}

func matchesPinnedModelPattern(model, pattern string) bool {
	if strings.EqualFold(model, pattern) {
		return true
	}
	// 标签匹配：model 为 pattern:tag（例如 gpt-6-astra:latest）
	if strings.HasPrefix(model, pattern+":") {
		return true
	}
	// 日期/版本快照匹配：model 必须以 pattern + "-" 开头，且紧随其后的字符必须是数字（如 -20260301）
	// 严格杜绝 gpt-6 匹配 gpt-6-astra，或 gpt-5 匹配 gpt-5-codex 的跨模型泄漏！
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
// 则将其强制写入请求头 x-codex-turn-state。
func applyPinnedCodexTurnState(headers http.Header, account *Account, model string) bool {
	if headers == nil || account == nil || strings.TrimSpace(model) == "" {
		return false
	}
	pinnedState := account.GetPinnedCodexTurnState(model)
	if pinnedState == "" {
		return false
	}
	// 防御性检查：确保 header 值不含非法字符（如换行符），杜绝任何导致上游请求硬失败的自伤风险
	if !httpguts.ValidHeaderFieldValue(pinnedState) {
		return false
	}
	headers.Set(openAICodexTurnStateHeader, pinnedState)
	return true
}
