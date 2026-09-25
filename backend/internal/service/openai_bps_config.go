package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	bpsSettingCacheTTL  = 15 * time.Second
	bpsSettingErrorTTL  = 5 * time.Second
	bpsSettingDBTimeout = 3 * time.Second
	bpsMaxAccountIDs    = 1000
)

// OpenAIBPSUpstreamConfig 是 BPS 上游的动态配置：总开关与参与账号列表，
// 均保存在 settings 表中，由降智修复面板维护。
type OpenAIBPSUpstreamConfig struct {
	Enabled    bool    `json:"enabled"`
	AccountIDs []int64 `json:"account_ids"`
}

type cachedBPSUpstreamConfig struct {
	enabled    bool
	accountIDs map[int64]struct{}
	expiresAt  int64
}

func (c *cachedBPSUpstreamConfig) public() OpenAIBPSUpstreamConfig {
	ids := make([]int64, 0, len(c.accountIDs))
	for id := range c.accountIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return OpenAIBPSUpstreamConfig{Enabled: c.enabled, AccountIDs: ids}
}

var bpsUpstreamConfigCache atomic.Value // *cachedBPSUpstreamConfig
var bpsUpstreamConfigSF singleflight.Group

func newCachedBPSUpstreamConfig(cfg OpenAIBPSUpstreamConfig, ttl time.Duration) *cachedBPSUpstreamConfig {
	ids := make(map[int64]struct{}, len(cfg.AccountIDs))
	for _, id := range cfg.AccountIDs {
		ids[id] = struct{}{}
	}
	return &cachedBPSUpstreamConfig{enabled: cfg.Enabled, accountIDs: ids, expiresAt: time.Now().Add(ttl).UnixNano()}
}

// parseBPSAccountIDs 解析账号 ID 列表：去重、剔除非正数并排序。
func parseBPSAccountIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return normalizeBPSAccountIDs(ids), nil
}

func normalizeBPSAccountIDs(ids []int64) []int64 {
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			result = append(result, id)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// loadOpenAIBPSUpstreamConfig 读取配置，进程内缓存 15s；读取失败按关闭处理。
func (s *SettingService) loadOpenAIBPSUpstreamConfig(ctx context.Context) *cachedBPSUpstreamConfig {
	if s == nil || s.settingRepo == nil {
		return &cachedBPSUpstreamConfig{}
	}
	if cached, ok := bpsUpstreamConfigCache.Load().(*cachedBPSUpstreamConfig); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached
	}
	result, _, _ := bpsUpstreamConfigSF.Do("bps_upstream_config", func() (any, error) {
		if cached, ok := bpsUpstreamConfigCache.Load().(*cachedBPSUpstreamConfig); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached, nil
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bpsSettingDBTimeout)
		defer cancel()
		// 逐个 GetValue：与现有设置读取路径一致，15s 才读一次。
		values := make(map[string]string, 2)
		var err error
		for _, key := range []string{SettingKeyOpenAIBPSUpstreamEnabled, SettingKeyOpenAIBPSUpstreamAccountIDs} {
			value, getErr := s.settingRepo.GetValue(dbCtx, key)
			if getErr != nil && !errors.Is(getErr, ErrSettingNotFound) {
				err = getErr
				break
			}
			values[key] = value
		}
		if err != nil {
			slog.Warn("failed to get openai bps upstream settings", "error", err)
			cached := newCachedBPSUpstreamConfig(OpenAIBPSUpstreamConfig{}, bpsSettingErrorTTL)
			bpsUpstreamConfigCache.Store(cached)
			return cached, nil
		}
		ids, err := parseBPSAccountIDs(values[SettingKeyOpenAIBPSUpstreamAccountIDs])
		if err != nil {
			slog.Warn("invalid openai_bps_upstream_account_ids setting", "error", err)
		}
		cfg := OpenAIBPSUpstreamConfig{Enabled: strings.TrimSpace(values[SettingKeyOpenAIBPSUpstreamEnabled]) == "true", AccountIDs: ids}
		cached := newCachedBPSUpstreamConfig(cfg, bpsSettingCacheTTL)
		bpsUpstreamConfigCache.Store(cached)
		return cached, nil
	})
	cached, _ := result.(*cachedBPSUpstreamConfig)
	if cached == nil {
		return &cachedBPSUpstreamConfig{}
	}
	return cached
}

// GetOpenAIBPSUpstreamConfig 返回当前生效的 BPS 上游配置。
func (s *SettingService) GetOpenAIBPSUpstreamConfig(ctx context.Context) OpenAIBPSUpstreamConfig {
	return s.loadOpenAIBPSUpstreamConfig(ctx).public()
}

// isOpenAIBPSUpstreamAccount 判断总开关已开启且账号在参与列表中。
func (s *SettingService) isOpenAIBPSUpstreamAccount(ctx context.Context, accountID int64) (enabled, listed bool) {
	cfg := s.loadOpenAIBPSUpstreamConfig(ctx)
	_, listed = cfg.accountIDs[accountID]
	return cfg.enabled, listed
}

// UpdateOpenAIBPSUpstreamConfig 保存配置并立即在本进程生效；其他实例在缓存到期（15s）后生效。
func (s *SettingService) UpdateOpenAIBPSUpstreamConfig(ctx context.Context, cfg OpenAIBPSUpstreamConfig) (OpenAIBPSUpstreamConfig, error) {
	if s == nil || s.settingRepo == nil {
		return OpenAIBPSUpstreamConfig{}, fmt.Errorf("setting service unavailable")
	}
	cfg.AccountIDs = normalizeBPSAccountIDs(cfg.AccountIDs)
	if len(cfg.AccountIDs) > bpsMaxAccountIDs {
		return OpenAIBPSUpstreamConfig{}, fmt.Errorf("too many accounts (max %d)", bpsMaxAccountIDs)
	}
	encoded, err := json.Marshal(cfg.AccountIDs)
	if err != nil {
		return OpenAIBPSUpstreamConfig{}, err
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyOpenAIBPSUpstreamEnabled:    strconv.FormatBool(cfg.Enabled),
		SettingKeyOpenAIBPSUpstreamAccountIDs: string(encoded),
	}); err != nil {
		return OpenAIBPSUpstreamConfig{}, err
	}
	refreshOpenAIBPSUpstreamConfigCache(cfg)
	return s.GetOpenAIBPSUpstreamConfig(ctx), nil
}

func refreshOpenAIBPSUpstreamConfigCache(cfg OpenAIBPSUpstreamConfig) {
	bpsUpstreamConfigSF.Forget("bps_upstream_config")
	bpsUpstreamConfigCache.Store(newCachedBPSUpstreamConfig(cfg, bpsSettingCacheTTL))
}
