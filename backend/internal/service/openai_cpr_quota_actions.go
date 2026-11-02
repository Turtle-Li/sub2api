package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// codex-proxy-rs 账号的额度查询 / 重置卡操作。
//
// OpenAI API Key 账号指向 cpr 时没有 ChatGPT 凭证，OpenAIQuotaService 改走 cpr 管理端：
//   - POST /api/admin/accounts/quota/refresh：让 cpr 立即查一次上游 /wham/usage（窗口 + 点数）
//   - GET  /api/admin/accounts/reset-credits：重置卡列表（cpr 原样透传上游 status/reset_type）
//   - POST /api/admin/accounts/reset-credits：消费一张重置卡
//
// 结果投影成与 /wham/usage 相同的 OpenAIQuotaUsage / OpenAIQuotaResetResult，
// 让账号列表的点数、重置次数、到期时间、手动重置与自动用卡沿用 OAuth 账号的整套逻辑。

// loadCPRQuotaAccount 返回指向 cpr 的账号；其他账号返回 nil，调用方继续走 ChatGPT 直连。
func (s *OpenAIQuotaService) loadCPRQuotaAccount(ctx context.Context, accountID int64) *Account {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || !IsCPRQuotaSyncAccount(account) {
		return nil
	}
	return account
}

func cprQuotaUpstreamError(code string, err error) error {
	status := http.StatusBadGateway
	var httpErr *cprQuotaHTTPError
	if errors.As(err, &httpErr) && httpErr.status >= 400 && httpErr.status < 500 && httpErr.status != http.StatusUnauthorized && httpErr.status != http.StatusForbidden {
		// cpr 自身的参数/状态错误（如无可用卡、账号不支持）原样告知管理员。
		status = http.StatusBadRequest
	}
	return infraerrors.Newf(status, code, "codex-proxy-rs: %v", err)
}

// queryCPRQuotaUsage 查询 cpr 账号的额度、点数与重置卡。
func (s *OpenAIQuotaService) queryCPRQuotaUsage(ctx context.Context, account *Account) (*OpenAIQuotaUsage, error) {
	now := time.Now()
	client, cprID, err := resolveCPRClient(ctx, account, now)
	if err != nil {
		return nil, cprQuotaUpstreamError("OPENAI_QUOTA_UPSTREAM_ERROR", err)
	}

	var data struct {
		Account cprAdminAccount `json:"account"`
	}
	// 主动查询需要上游最新值；cpr 刷新失败（例如上游暂时不可用）时退回 cpr 已存快照。
	if err := client.post(ctx, "/api/admin/accounts/quota/refresh", map[string]string{"accountId": cprID}, &data); err != nil {
		forgetCPRAccountOnNotFound(account.ID, err)
		slog.Warn("cpr_quota_refresh_failed", "account_id", account.ID, "error", err)
		if err := client.get(ctx, "/api/admin/accounts/quota", url.Values{"accountId": {cprID}}, &data); err != nil {
			forgetCPRAccountOnNotFound(account.ID, err)
			return nil, cprQuotaUpstreamError("OPENAI_QUOTA_UPSTREAM_ERROR", err)
		}
	}

	usage := buildCPRQuotaUsage(data.Account, now)

	var rawCredits json.RawMessage
	if err := client.get(ctx, "/api/admin/accounts/reset-credits", url.Values{"accountId": {cprID}}, &rawCredits); err != nil {
		// 与直连一致：重置卡明细失败不影响额度结果，缓存层会拒绝写入不完整的卡快照。
		slog.Warn("cpr_quota_reset_credit_details_failed", "account_id", account.ID, "error", err)
	} else if details, parseErr := parseOpenAIRateLimitResetCreditDetails(rawCredits); parseErr != nil {
		slog.Warn("cpr_quota_reset_credit_details_parse_failed", "account_id", account.ID, "error", parseErr)
	} else {
		usage.autoResetCandidates = details.AutoResetCandidates
		usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{Credits: details.Credits}
		if details.AvailableCount != nil {
			usage.RateLimitResetCredits.AvailableCount = *details.AvailableCount
		} else {
			usage.RateLimitResetCredits.AvailableCount = details.AvailableCreditCount
		}
	}
	// 与直连一致保持只读：点数/重置卡由 RefreshQuota 缓存，5h/7d 窗口由用量面板同步 cpr 快照。
	return usage, nil
}

// buildCPRQuotaUsage 把 cpr 账号视图投影成 /wham/usage 形状。
func buildCPRQuotaUsage(account cprAdminAccount, now time.Time) *OpenAIQuotaUsage {
	usage := &OpenAIQuotaUsage{
		Email:     derefCPRString(account.Email),
		AccountID: derefCPRString(account.AccountID),
		UserID:    derefCPRString(account.UserID),
		PlanType:  derefCPRString(account.PlanType),
		FetchedAt: now.Unix(),
	}
	quota := account.Quota
	loc := cprQuotaLocation(quota)

	toWindow := func(window *cprAdminQuotaWindow) *OpenAIRateLimitWindow {
		if window == nil {
			return nil
		}
		out := &OpenAIRateLimitWindow{}
		if window.UsedPercent != nil {
			out.UsedPercent = *window.UsedPercent
		}
		if window.WindowSeconds != nil {
			out.LimitWindowSeconds = *window.WindowSeconds
		}
		if resetAt, ok := parseCPRDisplayTime(window.ResetAtDisplay, loc); ok {
			out.ResetAt = resetAt.Unix()
			if after := int64(resetAt.Sub(now).Seconds()); after > 0 {
				out.ResetAfterSeconds = after
			}
		}
		return out
	}

	// 按 limitId 分桶：codex 主额度进 rate_limit，其余（如 codex_bengalfox）进 additional_rate_limits。
	type bucket struct {
		name          string
		limitReached  bool
		short, long   *cprAdminQuotaWindow
		hasWindowData bool
	}
	order := make([]string, 0, 2)
	buckets := make(map[string]*bucket)
	for i := range quota.Windows {
		window := &quota.Windows[i]
		limitID := cprWindowLimitID(window)
		b, ok := buckets[limitID]
		if !ok {
			b = &bucket{name: limitID}
			buckets[limitID] = b
			order = append(order, limitID)
		}
		if window.LimitName != nil && strings.TrimSpace(*window.LimitName) != "" {
			b.name = strings.TrimSpace(*window.LimitName)
		}
		b.limitReached = b.limitReached || window.LimitReached
		if window.WindowSeconds == nil {
			continue
		}
		b.hasWindowData = true
		if *window.WindowSeconds <= 6*3600 {
			if b.short == nil {
				b.short = window
			}
		} else if b.long == nil {
			b.long = window
		}
	}
	for _, limitID := range order {
		b := buckets[limitID]
		if !b.hasWindowData {
			continue
		}
		rateLimit := &OpenAIRateLimit{
			Allowed:         !b.limitReached,
			LimitReached:    b.limitReached,
			PrimaryWindow:   toWindow(b.short),
			SecondaryWindow: toWindow(b.long),
		}
		if limitID == "codex" {
			if quota.LimitReached {
				rateLimit.LimitReached = true
				rateLimit.Allowed = false
			}
			usage.RateLimit = rateLimit
			continue
		}
		usage.AdditionalRateLimits = append(usage.AdditionalRateLimits, OpenAIAdditionalRateLimit{
			LimitName:      b.name,
			MeteredFeature: limitID,
			RateLimit:      rateLimit,
		})
	}

	if quota.Credits != nil {
		usage.Credits = &OpenAICredits{
			HasCredits: quota.Credits.HasCredits,
			Unlimited:  quota.Credits.Unlimited,
			Balance:    quota.Credits.Balance,
		}
	}
	return usage
}

// cprQuotaLocation 取 cpr 部署时区（refreshedAt 的 RFC3339 偏移），用于解析 resetAtDisplay。
func cprQuotaLocation(quota cprAdminQuota) *time.Location {
	if quota.RefreshedAt != nil {
		if refreshed, err := time.Parse(time.RFC3339, *quota.RefreshedAt); err == nil {
			return refreshed.Location()
		}
	}
	return time.Local
}

func cprWindowLimitID(window *cprAdminQuotaWindow) string {
	if window.LimitID != nil && strings.TrimSpace(*window.LimitID) != "" {
		return strings.ToLower(strings.TrimSpace(*window.LimitID))
	}
	if idx := strings.Index(window.Key, ":"); idx > 0 {
		return strings.ToLower(window.Key[:idx])
	}
	return strings.ToLower(window.Key)
}

func derefCPRString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// cprRedeemRequestID 把任意幂等键稳定映射成 cpr 要求的 UUID v4 形式（同输入同输出，保持重试幂等）。
func cprRedeemRequestID(redeemRequestID string) string {
	if parsed, err := uuid.Parse(strings.TrimSpace(redeemRequestID)); err == nil && parsed.Version() == 4 {
		return parsed.String()
	}
	sum := sha256.Sum256([]byte("cpr-redeem:" + redeemRequestID))
	var id uuid.UUID
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}

// resetCPRCredit 通过 cpr 消费一张重置卡；creditID 为空时由 cpr/上游自选。
func (s *OpenAIQuotaService) resetCPRCredit(ctx context.Context, account *Account, creditID, redeemRequestID string) (*OpenAIQuotaResetResult, error) {
	client, cprID, err := resolveCPRClient(ctx, account, time.Now())
	if err != nil {
		return nil, cprQuotaUpstreamError("OPENAI_QUOTA_RESET_UPSTREAM_ERROR", err)
	}
	body := map[string]string{
		"accountId":       cprID,
		"redeemRequestId": cprRedeemRequestID(redeemRequestID),
	}
	if creditID = strings.TrimSpace(creditID); creditID != "" {
		body["creditId"] = creditID
	}
	var data struct {
		Code   string `json:"code"`
		Credit *struct {
			ID        string  `json:"id"`
			Status    *string `json:"status"`
			ExpiresAt *string `json:"expiresAt"`
			ResetType *string `json:"resetType"`
		} `json:"credit"`
	}
	if err := client.post(ctx, "/api/admin/accounts/reset-credits", body, &data); err != nil {
		forgetCPRAccountOnNotFound(account.ID, err)
		slog.Warn("cpr_quota_reset_failed", "account_id", account.ID, "error", err)
		return nil, cprQuotaUpstreamError("OPENAI_QUOTA_RESET_UPSTREAM_ERROR", err)
	}
	result := &OpenAIQuotaResetResult{Code: data.Code}
	if data.Credit != nil {
		result.Credit = &OpenAIQuotaResetCredit{
			ID:        data.Credit.ID,
			Status:    derefCPRString(data.Credit.Status),
			ExpiresAt: derefCPRString(data.Credit.ExpiresAt),
			ResetType: derefCPRString(data.Credit.ResetType),
		}
	}
	slog.Info("openai_quota_reset_success", "account_id", account.ID, "code", result.Code, "source", "cpr")
	return result, nil
}
