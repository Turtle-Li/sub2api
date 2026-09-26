package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// bpsCodexAutoCompactTokenLimit 是下发给含 BPS 账号分组的 Codex 自动压缩上限，也是回报用量提示压缩的门槛。
// BPS 在约 207k（按 BPS 计数，含约 25k 工具目录）处静默不产出；Codex 以上游回报的
// 用量比较该上限，BPS 轮次回报的正是 BPS 计数。须低于 bpsContextTokenLimit：Codex 的压缩请求
// 是携带完整上下文的普通请求，提示门槛与跳过门槛相同时它必然被跳过、落到缓存冷的原路径（实测 75~170s）；
// 留出单轮增长（实测约 11k）的余量，让压缩请求仍由 BPS 以热缓存完成。
// 仅作用于 BPS 可承接的模型，且只下调不上调；未含 BPS 账号的分组不受影响。
const bpsCodexAutoCompactTokenLimit = 185_000

// bpsCodexCompactHintTokens 是回给客户端的用量下限，不低于任何 Codex 模型的上下文窗口，
// 使客户端按"窗口已满"立即压缩。Codex 0.158 对 API Key 自定义 provider 不拉取 /models，
// 上面的 manifest 上限到不了客户端，只能靠回报用量触发压缩。
const bpsCodexCompactHintTokens = 1_050_000

const bpsCodexCompactHintContextKey = "openai_bps_codex_compact_hint"

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

// bpsCodexCompactHint 标记本请求由 BPS 账号承接 BPS 模型：无论本轮走 BPS 还是原路径，
// 会话上下文达到 BPS 上限时都应让客户端压缩。BPS 关闭或账号移出列表后自然不再标记。
// 记录账号 ID：同一请求故障转移到其他账号时标记不随之生效。
type bpsCodexCompactHint struct {
	accountID int64
	// contextLimitScope 非空表示本轮因上下文过大跳过了 BPS，原路径的用量用来判断能否恢复。
	contextLimitScope string
}

func markBPSCodexCompactHint(c *gin.Context, accountID int64) *bpsCodexCompactHint {
	hint := &bpsCodexCompactHint{accountID: accountID}
	if c != nil {
		c.Set(bpsCodexCompactHintContextKey, hint)
	}
	return hint
}

// clearBPSCodexCompactHint 清除上一次尝试留下的标记，每次判定都按本次账号与模型重新决定。
func clearBPSCodexCompactHint(c *gin.Context) {
	if c == nil {
		return
	}
	if _, ok := c.Get(bpsCodexCompactHintContextKey); ok {
		c.Set(bpsCodexCompactHintContextKey, (*bpsCodexCompactHint)(nil))
	}
}

func bpsCodexCompactHintFor(c *gin.Context, account *Account) *bpsCodexCompactHint {
	if c == nil || account == nil {
		return nil
	}
	value, _ := c.Get(bpsCodexCompactHintContextKey)
	hint, _ := value.(*bpsCodexCompactHint)
	if hint == nil || hint.accountID != account.ID {
		return nil
	}
	return hint
}

// observeBPSSkippedContext 在因上下文过大跳过 BPS 的原路径轮次成功后，按实际用量判断会话是否已压缩。
// 请求体字节数不可靠（图片、加密推理内容占大头时压缩后也缩不到阈值），以原路径上报的 token 为准。
func observeBPSSkippedContext(c *gin.Context, account *Account, usage *OpenAIUsage) {
	hint := bpsCodexCompactHintFor(c, account)
	if hint == nil || hint.contextLimitScope == "" || usage == nil {
		return
	}
	bpsSessionContexts.releaseAfterNative(hint.contextLimitScope, bpsUsageContextTokens(usage))
}

// applyBPSCodexCompactHintToSSELine 在已标记请求的终止事件中，若上报输入达到压缩门槛，
// 抬高回给客户端的 input/total tokens 以触发 Codex 自动压缩。计费用量取自原始事件，不受影响。
// 只看本轮上报用量、不看会话记录：客户端压缩后用量回落即停止，不会反复压缩。
func applyBPSCodexCompactHintToSSELine(c *gin.Context, account *Account, line, eventType string) string {
	if (eventType != "response.completed" && eventType != "response.done") || bpsCodexCompactHintFor(c, account) == nil {
		return line
	}
	prefix, data, ok := strings.Cut(line, "data:")
	if !ok || strings.TrimSpace(prefix) != "" {
		return line
	}
	data = strings.TrimLeft(data, " ")
	usage := gjson.Get(data, "response.usage")
	input := usage.Get("input_tokens").Int()
	if !usage.Exists() || input < bpsCodexAutoCompactTokenLimit {
		return line
	}
	delta := int64(bpsCodexCompactHintTokens) - input
	if delta <= 0 {
		return line
	}
	patched, err := sjson.Set(data, "response.usage.input_tokens", input+delta)
	if err != nil {
		return line
	}
	total := usage.Get("total_tokens").Int()
	if total <= 0 {
		total = input + usage.Get("output_tokens").Int()
	}
	if patched, err = sjson.Set(patched, "response.usage.total_tokens", total+delta); err != nil {
		return line
	}
	return "data: " + patched
}
