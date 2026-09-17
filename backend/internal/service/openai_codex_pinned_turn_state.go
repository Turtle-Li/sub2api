package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
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
	if a == nil || a.Extra == nil {
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
// 若无配置、已过期或不匹配则返回空字符串。
func (a *Account) GetPinnedCodexTurnState(model string) string {
	if a == nil || a.Extra == nil {
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

	// 1. 精确匹配
	if entry, ok := states[normModel]; ok {
		if isPinnedStateActive(entry) {
			return entry.State
		}
		return ""
	}

	// 2. 前缀/别名匹配（例如请求模型为 gpt-6-astra-20260301，匹配配置的 gpt-6-astra）
	for key, entry := range states {
		if matchesPinnedModelPattern(normModel, key) {
			if isPinnedStateActive(entry) {
				return entry.State
			}
			return ""
		}
	}
	return ""
}

func matchesPinnedModelPattern(model, pattern string) bool {
	if strings.EqualFold(model, pattern) {
		return true
	}
	if strings.HasPrefix(model, pattern+"-") || strings.HasPrefix(model, pattern+":") {
		return true
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
	headers.Set(openAICodexTurnStateHeader, pinnedState)
	return true
}

func (s *adminServiceImpl) GetPinnedCodexTurnStates(ctx context.Context, accountID int64) (map[string]PinnedCodexTurnStateEntry, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return account.GetPinnedCodexTurnStates(), nil
}

func (s *adminServiceImpl) SetPinnedCodexTurnState(ctx context.Context, accountID int64, model string, entry PinnedCodexTurnStateEntry) (*Account, error) {
	normModel := strings.ToLower(strings.TrimSpace(model))
	if normModel == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "INVALID_MODEL", "model must not be empty")
	}
	if strings.TrimSpace(entry.State) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "INVALID_STATE", "turn state must not be empty")
	}
	entry.State = strings.TrimSpace(entry.State)
	if entry.StateLen == 0 {
		entry.StateLen = len(entry.State)
	}
	now := time.Now().UTC()
	entry.UpdatedAt = &now

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
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
		return nil, infraerrors.New(http.StatusBadRequest, "INVALID_INPUT", "states must not be empty")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	currentStates := account.GetPinnedCodexTurnStates()
	if currentStates == nil {
		currentStates = make(map[string]PinnedCodexTurnStateEntry)
	}
	now := time.Now().UTC()
	for k, entry := range entries {
		normModel := strings.ToLower(strings.TrimSpace(k))
		if normModel == "" || strings.TrimSpace(entry.State) == "" {
			continue
		}
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
