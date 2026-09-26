package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// bpsCodexAutoCompactTokenLimit 是下发给含 BPS 账号分组的 Codex 自动压缩上限。
// BPS 在约 207k（按 BPS 计数，含约 25k 工具目录）处静默不产出；Codex 以上游回报的
// 用量比较该上限，BPS 轮次回报的正是 BPS 计数，留约 7k 余量让 Codex 在卡住前自行压缩。
// 仅作用于 BPS 可承接的模型，且只下调不上调；未含 BPS 账号的分组不受影响。
const bpsCodexAutoCompactTokenLimit = 200_000

// FinalizeCodexModelsManifest 在分组 manifest 定稿后应用 BPS 压缩上限，再做 ETag 协商。
// 调用方构建 manifest 时须传空 If-None-Match，否则旧 ETag 会让客户端一直沿用未下调的目录。
func (s *OpenAIGatewayService) FinalizeCodexModelsManifest(ctx context.Context, group *Group, manifest *OpenAIModelsResponse, ifNoneMatch string) error {
	if s == nil || manifest == nil || manifest.NotModified {
		return nil
	}
	if len(manifest.Body) > 0 {
		body, changed, err := capCodexAutoCompactForBPS(manifest.Body, s.bpsCodexModelsForGroup(ctx, group))
		if err != nil {
			return err
		}
		if changed {
			manifest.Body = body
			manifest.ETag = codexModelsManifestBodyETag(body)
		}
	}
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		manifest.Body = nil
		manifest.NotModified = true
	}
	return nil
}

// bpsCodexModelsForGroup 返回分组内会被 BPS 账号承接的模型判定；分组无 BPS 账号时返回 nil。
func (s *OpenAIGatewayService) bpsCodexModelsForGroup(ctx context.Context, group *Group) func(slug string) bool {
	if s.settingService == nil || s.accountRepo == nil || group == nil {
		return nil
	}
	cfg := s.settingService.GetOpenAIBPSUpstreamConfig(ctx)
	if !cfg.Enabled || len(cfg.AccountIDs) == 0 {
		return nil
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, cfg.AccountIDs)
	if err != nil {
		return nil
	}
	var members []*Account
	for _, account := range accounts {
		if account != nil && account.IsActive() && slices.Contains(account.GroupIDs, group.ID) {
			members = append(members, account)
		}
	}
	if len(members) == 0 {
		return nil
	}
	return func(slug string) bool {
		for _, account := range members {
			if !account.IsModelSupported(slug) {
				continue
			}
			_, upstream := resolveOpenAIForwardMappedModels(account, slug, false)
			if isBPSUpstreamModel(normalizeCodexModel(upstream)) {
				return true
			}
		}
		return false
	}
}

// capCodexAutoCompactForBPS 将 BPS 模型的 auto_compact_token_limit 下调到 BPS 上限，保留其余字段。
func capCodexAutoCompactForBPS(body []byte, isBPSModel func(slug string) bool) ([]byte, bool, error) {
	if isBPSModel == nil {
		return body, false, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false, err
	}
	var models []map[string]json.RawMessage
	if raw, ok := root["models"]; !ok || json.Unmarshal(raw, &models) != nil {
		return body, false, nil
	}
	limit, _ := json.Marshal(bpsCodexAutoCompactTokenLimit)
	changed := false
	for _, model := range models {
		var slug string
		if json.Unmarshal(model["slug"], &slug) != nil || !isBPSModel(strings.TrimSpace(slug)) {
			continue
		}
		var current int64
		if json.Unmarshal(model["auto_compact_token_limit"], &current) == nil && current > 0 && current <= bpsCodexAutoCompactTokenLimit {
			continue
		}
		model["auto_compact_token_limit"] = limit
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return nil, false, err
	}
	root["models"] = encoded
	body, err = json.Marshal(root)
	return body, err == nil, err
}
