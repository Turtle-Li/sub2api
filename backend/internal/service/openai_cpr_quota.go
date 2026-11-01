package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// codex-proxy-rs（cpr）额度同步。
//
// cpr 会剥掉上游的 x-codex-* 额度头，sub2 的 OpenAI API Key 账号指向 cpr 时拿不到 5h/7d 额度。
// 这里用 cpr 管理端的 admin API key 读取 cpr 已存的额度快照（GET /api/admin/accounts/quota，
// 不触发上游请求），转换成和 OAuth 账号相同的 codex_* extra 字段，复用现有的额度展示、
// 自动暂停与 429 冷却逻辑。
//
// 配置：
//   - credentials.cpr_admin_api_key：cpr 管理 API Key（admin-<64 hex>），敏感字段，不回显前端。
//   - extra.cpr_account_id：可选，cpr 账号 ID；留空时按本账号的 api_key 在 cpr 中反查
//     （Client Key → 唯一分组 → 唯一账号）。
//   - extra.cpr_admin_base_url：可选，cpr 管理端地址；留空时取 base_url 去掉末尾 /v1。
const (
	cprQuotaAdminKeyCredential = "cpr_admin_api_key"
	cprQuotaAccountIDExtra     = "cpr_account_id"
	cprQuotaAdminBaseURLExtra  = "cpr_admin_base_url"

	cprQuotaResolvedAccountIDExtra = "cpr_quota_account_id"
	cprQuotaSyncedAtExtra          = "cpr_quota_synced_at"
	cprQuotaLastErrorExtra         = "cpr_quota_last_error"

	// 用量面板读取时快照的新鲜度；读的是 cpr 本地快照，代价很小。
	cprQuotaStaleAfter = time.Minute
	// 非强制同步的最小间隔（请求成功后的异步刷新也走这个节流）。
	cprQuotaSyncMinInterval = 30 * time.Second
	cprQuotaRequestTimeout  = 10 * time.Second
	cprQuotaResolveCacheTTL = 30 * time.Minute
	cprQuotaMaxBodyBytes    = 2 << 20
)

var (
	cprQuotaHTTPClient = &http.Client{
		Timeout: cprQuotaRequestTimeout,
		// 管理 Key 不跟随重定向发往别处。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cprQuotaSyncThrottle   sync.Map // account id -> time.Time
	cprQuotaSyncThrottleMu sync.Mutex
	cprQuotaResolveCache   sync.Map // account id -> cprQuotaResolvedAccount
)

type cprQuotaResolvedAccount struct {
	fingerprint string
	cprID       string
	at          time.Time
}

// IsCPRQuotaSyncAccount 判断账号是否配置了 cpr 额度同步。
func IsCPRQuotaSyncAccount(account *Account) bool {
	return account != nil && account.IsOpenAIApiKey() && !account.IsShadow() &&
		strings.TrimSpace(account.GetCredential(cprQuotaAdminKeyCredential)) != ""
}

// cprQuotaSyncResult 是一次同步得到的 extra 更新与耗尽信息。
type cprQuotaSyncResult struct {
	updates map[string]any
	// exhaustedUntil：cpr 报告主额度（codex）已耗尽时，最晚恢复时间。
	exhaustedUntil *time.Time
}

type cprAdminEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type cprAdminQuotaWindow struct {
	Key            string   `json:"key"`
	LimitID        *string  `json:"limitId"`
	WindowSeconds  *int64   `json:"windowSeconds"`
	UsedPercent    *float64 `json:"usedPercent"`
	LimitReached   bool     `json:"limitReached"`
	ResetAtDisplay string   `json:"resetAtDisplay"`
}

type cprAdminQuota struct {
	RefreshedAt      *string               `json:"refreshedAt"`
	LimitReached     bool                  `json:"limitReached"`
	RateLimitedUntil *string               `json:"rateLimitedUntil"`
	Windows          []cprAdminQuotaWindow `json:"windows"`
}

type cprAdminAccount struct {
	ID       string        `json:"id"`
	Provider string        `json:"provider"`
	Quota    cprAdminQuota `json:"quota"`
}

type cprAdminGroupRef struct {
	ID string `json:"id"`
}

type cprAdminClientKey struct {
	ID     string             `json:"id"`
	Prefix string             `json:"prefix"`
	Groups []cprAdminGroupRef `json:"groups"`
}

type cprQuotaClient struct {
	baseURL  string
	adminKey string
}

func cprQuotaAdminBaseURL(account *Account) string {
	if override := strings.TrimSpace(account.GetExtraString(cprQuotaAdminBaseURLExtra)); override != "" {
		return strings.TrimRight(override, "/")
	}
	base := strings.TrimRight(strings.TrimSpace(account.GetOpenAIBaseURL()), "/")
	return strings.TrimRight(strings.TrimSuffix(base, "/v1"), "/")
}

func (c cprQuotaClient) get(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	reqCtx, cancel := context.WithTimeout(ctx, cprQuotaRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.adminKey)
	resp, err := cprQuotaHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, cprQuotaMaxBodyBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var envelope cprAdminEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("%s: http %d, invalid json", path, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || envelope.Code != http.StatusOK {
		return &cprQuotaHTTPError{path: path, status: resp.StatusCode, code: envelope.Code, message: envelope.Message}
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("%s: decode data: %w", path, err)
	}
	return nil
}

type cprQuotaHTTPError struct {
	path    string
	status  int
	code    int
	message string
}

func (e *cprQuotaHTTPError) Error() string {
	return fmt.Sprintf("%s: http %d code %d %s", e.path, e.status, e.code, truncateForLog([]byte(e.message), 200))
}

func cprQuotaFingerprint(account *Account, baseURL string) string {
	sum := sha256.Sum256([]byte(baseURL + "\x00" + account.GetOpenAIApiKey() + "\x00" + account.GetCredential(cprQuotaAdminKeyCredential)))
	return hex.EncodeToString(sum[:])
}

// resolveCPRAccountID 找到本 sub2 账号对应的 cpr 账号 ID。
func resolveCPRAccountID(ctx context.Context, client cprQuotaClient, account *Account, now time.Time) (string, error) {
	if explicit := strings.TrimSpace(account.GetExtraString(cprQuotaAccountIDExtra)); explicit != "" {
		return explicit, nil
	}
	fingerprint := cprQuotaFingerprint(account, client.baseURL)
	if cached, ok := cprQuotaResolveCache.Load(account.ID); ok {
		if entry, ok := cached.(cprQuotaResolvedAccount); ok && entry.fingerprint == fingerprint && now.Sub(entry.at) < cprQuotaResolveCacheTTL {
			return entry.cprID, nil
		}
	}

	apiKey := strings.TrimSpace(account.GetOpenAIApiKey())
	if apiKey == "" {
		return "", errors.New("account api_key is empty")
	}
	var keys struct {
		Items []cprAdminClientKey `json:"items"`
	}
	if err := client.get(ctx, "/api/admin/client-keys", url.Values{"limit": {"200"}}, &keys); err != nil {
		return "", err
	}
	var candidates []cprAdminClientKey
	for _, key := range keys.Items {
		if key.Prefix != "" && strings.HasPrefix(apiKey, key.Prefix) {
			candidates = append(candidates, key)
		}
	}
	matched, err := pickCPRClientKey(ctx, client, candidates, apiKey)
	if err != nil {
		return "", err
	}
	if len(matched.Groups) != 1 {
		return "", fmt.Errorf("cpr client key %s is bound to %d groups, set %s explicitly", matched.ID, len(matched.Groups), cprQuotaAccountIDExtra)
	}

	var page struct {
		Items []cprAdminAccount `json:"items"`
	}
	query := url.Values{"groupId": {matched.Groups[0].ID}, "page": {"1"}, "pageSize": {"10"}}
	if err := client.get(ctx, "/api/admin/accounts", query, &page); err != nil {
		return "", err
	}
	if len(page.Items) != 1 {
		return "", fmt.Errorf("cpr group %s has %d accounts, set %s explicitly", matched.Groups[0].ID, len(page.Items), cprQuotaAccountIDExtra)
	}
	cprID := page.Items[0].ID
	cprQuotaResolveCache.Store(account.ID, cprQuotaResolvedAccount{fingerprint: fingerprint, cprID: cprID, at: now})
	return cprID, nil
}

func pickCPRClientKey(ctx context.Context, client cprQuotaClient, candidates []cprAdminClientKey, apiKey string) (cprAdminClientKey, error) {
	switch len(candidates) {
	case 0:
		return cprAdminClientKey{}, errors.New("no cpr client key matches this account api_key")
	case 1:
		return candidates[0], nil
	}
	// 前缀只有 10 位，自定义 Key 可能撞前缀：逐个 reveal 精确比对。
	for _, candidate := range candidates {
		var revealed struct {
			PlaintextKey string `json:"plaintextKey"`
		}
		if err := client.get(ctx, "/api/admin/client-keys/reveal", url.Values{"id": {candidate.ID}}, &revealed); err != nil {
			return cprAdminClientKey{}, err
		}
		if subtle.ConstantTimeCompare([]byte(revealed.PlaintextKey), []byte(apiKey)) == 1 {
			return candidate, nil
		}
	}
	return cprAdminClientKey{}, errors.New("no cpr client key matches this account api_key")
}

// fetchCPRQuota 读取 cpr 快照并转换成 codex_* extra 更新。
func fetchCPRQuota(ctx context.Context, account *Account, now time.Time) (*cprQuotaSyncResult, string, error) {
	baseURL := cprQuotaAdminBaseURL(account)
	if baseURL == "" {
		return nil, "", errors.New("cpr admin base url is empty")
	}
	if parsed, err := url.Parse(baseURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, "", fmt.Errorf("invalid cpr admin base url %q", baseURL)
	}
	client := cprQuotaClient{baseURL: baseURL, adminKey: strings.TrimSpace(account.GetCredential(cprQuotaAdminKeyCredential))}
	cprID, err := resolveCPRAccountID(ctx, client, account, now)
	if err != nil {
		return nil, "", err
	}
	var data struct {
		Account cprAdminAccount `json:"account"`
	}
	if err := client.get(ctx, "/api/admin/accounts/quota", url.Values{"accountId": {cprID}}, &data); err != nil {
		var httpErr *cprQuotaHTTPError
		if errors.As(err, &httpErr) && httpErr.status == http.StatusNotFound {
			cprQuotaResolveCache.Delete(account.ID)
		}
		return nil, cprID, err
	}
	return buildCPRQuotaSyncResult(data.Account.Quota, now), cprID, nil
}

func buildCPRQuotaSyncResult(quota cprAdminQuota, now time.Time) *cprQuotaSyncResult {
	result := &cprQuotaSyncResult{}

	loc := time.Local
	base := now
	if quota.RefreshedAt != nil {
		if refreshed, err := time.Parse(time.RFC3339, *quota.RefreshedAt); err == nil {
			base = refreshed
			loc = refreshed.Location()
		}
	}

	var short, long *cprAdminQuotaWindow
	for i := range quota.Windows {
		window := &quota.Windows[i]
		if !isCPRMainCodexWindow(window) || window.WindowSeconds == nil || window.UsedPercent == nil {
			continue
		}
		if *window.WindowSeconds <= 6*3600 {
			if short == nil {
				short = window
			}
		} else if long == nil {
			long = window
		}
	}

	snapshot := &OpenAICodexUsageSnapshot{UpdatedAt: base.UTC().Format(time.RFC3339)}
	apply := func(window *cprAdminQuotaWindow, used **float64, resetAfter **int, minutes **int) {
		if window == nil {
			return
		}
		usedPercent := *window.UsedPercent
		*used = &usedPercent
		windowMinutes := int(*window.WindowSeconds / 60)
		*minutes = &windowMinutes
		if resetAt, ok := parseCPRDisplayTime(window.ResetAtDisplay, loc); ok {
			seconds := int(math.Max(0, resetAt.Sub(base).Seconds()))
			*resetAfter = &seconds
			if window.LimitReached && resetAt.After(now) && (result.exhaustedUntil == nil || resetAt.After(*result.exhaustedUntil)) {
				resetCopy := resetAt
				result.exhaustedUntil = &resetCopy
			}
		}
	}
	apply(short, &snapshot.PrimaryUsedPercent, &snapshot.PrimaryResetAfterSeconds, &snapshot.PrimaryWindowMinutes)
	apply(long, &snapshot.SecondaryUsedPercent, &snapshot.SecondaryResetAfterSeconds, &snapshot.SecondaryWindowMinutes)

	if result.exhaustedUntil == nil && quota.LimitReached && quota.RateLimitedUntil != nil {
		if until, err := time.Parse(time.RFC3339, *quota.RateLimitedUntil); err == nil && until.After(now) {
			result.exhaustedUntil = &until
		}
	}

	if short != nil || long != nil {
		result.updates = buildCodexUsageExtraUpdates(snapshot, now)
	}
	return result
}

func isCPRMainCodexWindow(window *cprAdminQuotaWindow) bool {
	if window.LimitID != nil && strings.TrimSpace(*window.LimitID) != "" {
		return strings.EqualFold(strings.TrimSpace(*window.LimitID), "codex")
	}
	return strings.HasPrefix(strings.ToLower(window.Key), "codex:")
}

// cpr 只返回部署时区下格式化的 resetAtDisplay（"2006-01-02 15:04:05"，未知为 "—"）。
func parseCPRDisplayTime(value string, loc *time.Location) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "—" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, loc)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// cprQuotaSnapshotStale 判断用量面板读取时是否需要重新同步。
func cprQuotaSnapshotStale(account *Account, now time.Time) bool {
	raw := strings.TrimSpace(account.GetExtraString(cprQuotaSyncedAtExtra))
	if raw == "" {
		return true
	}
	syncedAt, err := parseTime(raw)
	if err != nil {
		return true
	}
	return now.Sub(syncedAt) >= cprQuotaStaleAfter
}

func allowCPRQuotaSync(accountID int64, now time.Time, force bool) bool {
	cprQuotaSyncThrottleMu.Lock()
	defer cprQuotaSyncThrottleMu.Unlock()
	if !force {
		if last, ok := cprQuotaSyncThrottle.Load(accountID); ok {
			if ts, ok := last.(time.Time); ok && now.Sub(ts) < cprQuotaSyncMinInterval {
				return false
			}
		}
	}
	cprQuotaSyncThrottle.Store(accountID, now)
	return true
}

// syncCPRQuotaSnapshot 拉取 cpr 额度并写回账号 extra（同时更新传入的 account 内存副本）。
// 被节流时返回 (nil, nil)。
func syncCPRQuotaSnapshot(ctx context.Context, repo AccountRepository, account *Account, force bool) (*cprQuotaSyncResult, error) {
	if !IsCPRQuotaSyncAccount(account) {
		return nil, nil
	}
	now := time.Now()
	if !allowCPRQuotaSync(account.ID, now, force) {
		return nil, nil
	}
	result, cprID, err := fetchCPRQuota(ctx, account, now)
	updates := map[string]any{cprQuotaSyncedAtExtra: now.UTC().Format(time.RFC3339)}
	if err != nil {
		slog.Warn("cpr_quota_sync_failed", "account_id", account.ID, "error", err)
		updates[cprQuotaLastErrorExtra] = truncateForLog([]byte(err.Error()), 300)
	} else {
		for k, v := range result.updates {
			updates[k] = v
		}
		updates[cprQuotaLastErrorExtra] = ""
		updates[cprQuotaResolvedAccountIDExtra] = cprID
	}
	mergeAccountExtra(account, updates)
	if repo != nil {
		if persistErr := repo.UpdateExtra(ctx, account.ID, updates); persistErr != nil {
			slog.Warn("cpr_quota_sync_persist_failed", "account_id", account.ID, "error", persistErr)
		} else if err == nil && len(result.updates) > 0 {
			notifyOpenAIAutoReset(account.ID)
		}
	}
	return result, err
}

// RefreshCPRQuotaSnapshotAsync 在请求成功后异步刷新 cpr 额度快照（受节流）。
func (s *OpenAIGatewayService) RefreshCPRQuotaSnapshotAsync(account *Account) {
	if s == nil || !IsCPRQuotaSyncAccount(account) {
		return
	}
	if last, ok := cprQuotaSyncThrottle.Load(account.ID); ok {
		if ts, ok := last.(time.Time); ok && time.Since(ts) < cprQuotaSyncMinInterval {
			return
		}
	}
	snapshot := *account
	snapshot.Extra = cloneAccountExtraForCPRSync(account.Extra)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*cprQuotaRequestTimeout)
		defer cancel()
		_, _ = syncCPRQuotaSnapshot(ctx, s.accountRepo, &snapshot, false)
	}()
}

func cloneAccountExtraForCPRSync(extra map[string]any) map[string]any {
	out := make(map[string]any, len(extra))
	for k, v := range extra {
		out[k] = v
	}
	return out
}
