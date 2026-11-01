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
	"sync/atomic"
	"time"
)

// codex-proxy-rs（cpr）额度同步。
//
// cpr 会剥掉上游的 x-codex-* 额度头，sub2 的 OpenAI API Key 账号指向 cpr 时拿不到 5h/7d 额度。
// 这里用 cpr 管理端的 admin API key 读取 cpr 已存的额度快照（GET /api/admin/accounts/quota，
// 不触发上游请求），转换成和 OAuth 账号相同的 codex_* extra 字段，复用现有的额度展示、
// 自动暂停与 429 冷却逻辑。
//
// 配置是全局的（系统设置 cpr_quota_settings）：cpr 地址 + 管理 API Key（admin-<64 hex>）。
// base_url（去掉末尾 /v1）与该地址相同的 OpenAI API Key 账号自动启用同步；对应的 cpr 账号
// 按账号的 api_key 在 cpr 中反查（Client Key → 唯一分组 → 唯一账号），无需逐个配置。
const (
	SettingKeyCPRQuotaSettings = "cpr_quota_settings"

	cprQuotaResolvedAccountIDExtra = "cpr_quota_account_id"
	cprQuotaSyncedAtExtra          = "cpr_quota_synced_at"
	cprQuotaLastErrorExtra         = "cpr_quota_last_error"

	// 用量面板读取时快照的新鲜度；读的是 cpr 本地快照，代价很小。
	cprQuotaStaleAfter = time.Minute
	// 非强制同步的最小间隔（请求成功后的异步刷新也走这个节流）。
	cprQuotaSyncMinInterval = 30 * time.Second
	cprQuotaRequestTimeout  = 10 * time.Second
	cprQuotaResolveCacheTTL = 30 * time.Minute
	cprQuotaSettingsTTL     = 30 * time.Second
	cprQuotaMaxBodyBytes    = 2 << 20
)

var (
	cprQuotaHTTPClient = &http.Client{
		Timeout: cprQuotaRequestTimeout,
		// 管理 Key 不跟随重定向发往别处。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cprQuotaSettingsSource atomic.Pointer[SettingService]
	cprQuotaSettingsCache  atomic.Pointer[cachedCPRQuotaSettings]
	cprQuotaSyncThrottle   sync.Map // account id -> time.Time
	cprQuotaSyncThrottleMu sync.Mutex
	cprQuotaResolveCache   sync.Map // account id -> cprQuotaResolvedAccount
)

type cprQuotaResolvedAccount struct {
	fingerprint string
	cprID       string
	at          time.Time
}

// CPRQuotaSettings 是全局 cpr 额度同步配置。
type CPRQuotaSettings struct {
	BaseURL     string `json:"base_url"`
	AdminAPIKey string `json:"admin_api_key"`
}

func (c CPRQuotaSettings) enabled() bool {
	return c.BaseURL != "" && c.AdminAPIKey != ""
}

type cachedCPRQuotaSettings struct {
	settings  CPRQuotaSettings
	expiresAt time.Time
}

// registerCPRQuotaSettingsSource 让无 SettingService 依赖的调用点（DTO、限流、网关）读取全局配置。
func registerCPRQuotaSettingsSource(s *SettingService) {
	cprQuotaSettingsSource.Store(s)
	cprQuotaSettingsCache.Store(nil)
}

// normalizeCPRBaseURL 统一成不带 /v1、不带结尾斜杠的小写 scheme://host[/path] 形式。
func normalizeCPRBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	base = strings.TrimRight(strings.TrimSuffix(base, "/v1"), "/")
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + parsed.EscapedPath()
}

// GetCPRQuotaSettings 读取全局 cpr 额度同步配置。
func (s *SettingService) GetCPRQuotaSettings(ctx context.Context) (CPRQuotaSettings, error) {
	if s == nil || s.settingRepo == nil {
		return CPRQuotaSettings{}, nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyCPRQuotaSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return CPRQuotaSettings{}, nil
		}
		return CPRQuotaSettings{}, fmt.Errorf("get cpr quota settings: %w", err)
	}
	var settings CPRQuotaSettings
	if strings.TrimSpace(value) != "" {
		if err := json.Unmarshal([]byte(value), &settings); err != nil {
			return CPRQuotaSettings{}, fmt.Errorf("decode cpr quota settings: %w", err)
		}
	}
	settings.BaseURL = normalizeCPRBaseURL(settings.BaseURL)
	settings.AdminAPIKey = strings.TrimSpace(settings.AdminAPIKey)
	return settings, nil
}

// UpdateCPRQuotaSettings 保存全局配置；adminAPIKey 为 nil 时保留原 Key，空串表示清除。
func (s *SettingService) UpdateCPRQuotaSettings(ctx context.Context, baseURL string, adminAPIKey *string) (CPRQuotaSettings, error) {
	if s == nil || s.settingRepo == nil {
		return CPRQuotaSettings{}, errors.New("cpr quota settings service is unavailable")
	}
	current, err := s.GetCPRQuotaSettings(ctx)
	if err != nil {
		return CPRQuotaSettings{}, err
	}
	next := CPRQuotaSettings{BaseURL: normalizeCPRBaseURL(baseURL), AdminAPIKey: current.AdminAPIKey}
	if strings.TrimSpace(baseURL) != "" && next.BaseURL == "" {
		return CPRQuotaSettings{}, fmt.Errorf("invalid cpr base url %q", baseURL)
	}
	if adminAPIKey != nil {
		next.AdminAPIKey = strings.TrimSpace(*adminAPIKey)
	}
	data, err := json.Marshal(next)
	if err != nil {
		return CPRQuotaSettings{}, fmt.Errorf("marshal cpr quota settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyCPRQuotaSettings, string(data)); err != nil {
		return CPRQuotaSettings{}, fmt.Errorf("set cpr quota settings: %w", err)
	}
	cprQuotaSettingsCache.Store(&cachedCPRQuotaSettings{settings: next, expiresAt: time.Now().Add(cprQuotaSettingsTTL)})
	cprQuotaResolveCache.Range(func(key, _ any) bool {
		cprQuotaResolveCache.Delete(key)
		return true
	})
	return next, nil
}

func currentCPRQuotaSettings() CPRQuotaSettings {
	now := time.Now()
	if cached := cprQuotaSettingsCache.Load(); cached != nil && now.Before(cached.expiresAt) {
		return cached.settings
	}
	source := cprQuotaSettingsSource.Load()
	if source == nil {
		return CPRQuotaSettings{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	settings, err := source.GetCPRQuotaSettings(ctx)
	if err != nil {
		slog.Warn("cpr_quota_settings_load_failed", "error", err)
		// 读失败时短暂沿用旧值，避免每次调用都打 DB。
		if cached := cprQuotaSettingsCache.Load(); cached != nil {
			settings = cached.settings
		}
	}
	cprQuotaSettingsCache.Store(&cachedCPRQuotaSettings{settings: settings, expiresAt: now.Add(cprQuotaSettingsTTL)})
	return settings
}

// IsCPRQuotaSyncAccount 判断账号是否指向已配置额度同步的 codex-proxy-rs。
func IsCPRQuotaSyncAccount(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() || account.IsShadow() {
		return false
	}
	settings := currentCPRQuotaSettings()
	if !settings.enabled() {
		return false
	}
	return normalizeCPRBaseURL(account.GetOpenAIBaseURL()) == settings.BaseURL
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

func cprQuotaFingerprint(account *Account, client cprQuotaClient) string {
	sum := sha256.Sum256([]byte(client.baseURL + "\x00" + account.GetOpenAIApiKey() + "\x00" + client.adminKey))
	return hex.EncodeToString(sum[:])
}

// resolveCPRAccountID 找到本 sub2 账号对应的 cpr 账号 ID。
func resolveCPRAccountID(ctx context.Context, client cprQuotaClient, account *Account, now time.Time) (string, error) {
	fingerprint := cprQuotaFingerprint(account, client)
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
		return "", fmt.Errorf("cpr client key %s is bound to %d groups, expected exactly 1", matched.ID, len(matched.Groups))
	}

	var page struct {
		Items []cprAdminAccount `json:"items"`
	}
	query := url.Values{"groupId": {matched.Groups[0].ID}, "page": {"1"}, "pageSize": {"10"}}
	if err := client.get(ctx, "/api/admin/accounts", query, &page); err != nil {
		return "", err
	}
	if len(page.Items) != 1 {
		return "", fmt.Errorf("cpr group %s has %d accounts, expected exactly 1", matched.Groups[0].ID, len(page.Items))
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
	settings := currentCPRQuotaSettings()
	if !settings.enabled() {
		return nil, "", errors.New("cpr quota sync is not configured")
	}
	client := cprQuotaClient{baseURL: settings.BaseURL, adminKey: settings.AdminAPIKey}
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
