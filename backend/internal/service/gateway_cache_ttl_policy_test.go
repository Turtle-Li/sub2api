package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newTTLTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	return c
}

func ttlAt(t *testing.T, body []byte, path string) string {
	t.Helper()
	node := gjson.GetBytes(body, path)
	require.True(t, node.Exists(), "path %s missing in %s", path, string(body))
	return node.String()
}

const ttlMixedBody = `{
  "model": "claude-opus-5-5",
  "system": [
    {"type": "text", "text": "sys-no-ttl", "cache_control": {"type": "ephemeral"}},
    {"type": "text", "text": "sys-5m", "cache_control": {"type": "ephemeral", "ttl": "5m"}}
  ],
  "tools": [
    {"name": "read", "cache_control": {"type": "ephemeral"}}
  ],
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "hi", "cache_control": {"type": "ephemeral"}}]}
  ]
}`

func TestApplyDefaultCacheTTLKeepsClientTTL(t *testing.T) {
	out := applyDefaultCacheTTL([]byte(ttlMixedBody), cacheTTLTarget5m)

	require.Equal(t, cacheTTLTarget5m, ttlAt(t, out, "system.0.cache_control.ttl"), "missing ttl should get the default")
	require.Equal(t, cacheTTLTarget5m, ttlAt(t, out, "system.1.cache_control.ttl"), "client ttl must survive")
	require.Equal(t, cacheTTLTarget5m, ttlAt(t, out, "tools.0.cache_control.ttl"), "tool without ttl should get the default")
	require.Equal(t, cacheTTLTarget5m, ttlAt(t, out, "messages.0.content.0.cache_control.ttl"), "message without ttl should get the default")
}

func TestApplyDefaultCacheTTLNeverOverridesClientOneHour(t *testing.T) {
	body := `{"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral","ttl":"1h"}}],
	          "messages":[{"role":"user","content":"hi"}]}`
	out := applyDefaultCacheTTL([]byte(body), cacheTTLTarget5m)
	require.Equal(t, cacheTTLTarget1h, ttlAt(t, out, "system.0.cache_control.ttl"), "client 1h must win over a 5m default")
}

func TestForceEphemeralCacheControlTTLStillOverrides(t *testing.T) {
	out := forceEphemeralCacheControlTTL([]byte(ttlMixedBody), cacheTTLTarget1h)
	require.Equal(t, cacheTTLTarget1h, ttlAt(t, out, "system.1.cache_control.ttl"), "legacy force mode rewrites every block")
}

func TestNormalizeCacheTTL(t *testing.T) {
	cases := map[string]string{
		"5m": cacheTTLTarget5m, "5MIN": cacheTTLTarget5m, " 300 ": cacheTTLTarget5m, "300s": cacheTTLTarget5m,
		"1h": cacheTTLTarget1h, "60min": cacheTTLTarget1h, "3600s": cacheTTLTarget1h, "1HOUR": cacheTTLTarget1h,
		"": "", "2h": "", "5h": "", "garbage": "",
	}
	for in, want := range cases {
		got, ok := NormalizeCacheTTL(in)
		if want == "" {
			require.False(t, ok, "NormalizeCacheTTL(%q) should be rejected", in)
			continue
		}
		require.True(t, ok, "NormalizeCacheTTL(%q) should be accepted", in)
		require.Equal(t, want, got)
	}
}

func TestResolveClientCacheTTLReadsHeaderAndBody(t *testing.T) {
	c := newTTLTestContext(t)
	c.Request.Header.Set(CacheTTLHeader, "1h")

	got := ResolveClientCacheTTL(c, []byte(ttlMixedBody))
	require.Equal(t, cacheTTLTarget1h, got.HeaderTTL)
	require.True(t, got.BodyHasExplicit)
	require.Equal(t, cacheTTLTarget5m, got.BodyTTL, "body carries exactly one ttl value")
	require.Equal(t, cacheTTLTarget1h, got.Preferred(), "header wins over body")
	require.True(t, got.Present())

	bare := ResolveClientCacheTTL(c, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	require.Equal(t, cacheTTLTarget1h, bare.HeaderTTL)
	require.False(t, bare.BodyHasExplicit)

	c.Request.Header.Del(CacheTTLHeader)
	none := ResolveClientCacheTTL(c, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	require.False(t, none.Present())
}

func TestBodyCacheTTLDetectsSingleValue(t *testing.T) {
	explicit, ttl := bodyCacheTTL([]byte(ttlMixedBody))
	require.True(t, explicit)
	require.Equal(t, cacheTTLTarget5m, ttl)

	withoutTTL := `{"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}]}`
	explicit, ttl = bodyCacheTTL([]byte(withoutTTL))
	require.False(t, explicit)
	require.Empty(t, ttl)

	mixed := `{"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral","ttl":"1h"}}],
	           "messages":[{"role":"user","content":[{"type":"text","text":"b","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`
	explicit, ttl = bodyCacheTTL([]byte(mixed))
	require.True(t, explicit)
	require.Empty(t, ttl, "mixed values must not be promoted to a default")

	explicit, ttl = bodyCacheTTL(nil)
	require.False(t, explicit)
	require.Empty(t, ttl)
}

// ttlSettingRepoStub 是只读设置仓库桩，避免依赖 -tags unit 下的测试辅助文件。
type ttlSettingRepoStub struct {
	values map[string]string
}

func (s *ttlSettingRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (s *ttlSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}

func (s *ttlSettingRepoStub) Set(ctx context.Context, key, value string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *ttlSettingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (s *ttlSettingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	for k, v := range settings {
		_ = s.Set(ctx, k, v)
	}
	return nil
}

func (s *ttlSettingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	return s.values, nil
}

func (s *ttlSettingRepoStub) Delete(ctx context.Context, key string) error { return nil }

func newTTLTestGateway(t *testing.T, injection1h bool) *GatewayService {
	t.Helper()
	value := "false"
	if injection1h {
		value = "true"
	}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := NewSettingService(&ttlSettingRepoStub{values: map[string]string{
		SettingKeyEnableAnthropicCacheTTL1hInjection: value,
	}}, &config.Config{})
	return &GatewayService{settingService: svc}
}

func TestResolveDefaultCacheTTLPrecedence(t *testing.T) {
	ctx := context.Background()
	oauth := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	withAccount := func(target string) *Account {
		return &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{
			"cache_ttl_override_enabled": true,
			"cache_ttl_override_target":  target,
		}}
	}

	// 1. 客户端请求头最高，压过账号与全局。
	svc := newTTLTestGateway(t, true)
	require.Equal(t, cacheTTLTarget5m,
		svc.resolveDefaultCacheTTL(ctx, withAccount(cacheTTLTarget1h), ClientCacheTTL{HeaderTTL: cacheTTLTarget5m}))

	// 2. 客户端只给了请求体 ttl 时，同样压过账号与全局。
	svc = newTTLTestGateway(t, true)
	require.Equal(t, cacheTTLTarget5m,
		svc.resolveDefaultCacheTTL(ctx, withAccount(cacheTTLTarget1h), ClientCacheTTL{BodyHasExplicit: true, BodyTTL: cacheTTLTarget5m}))

	// 3. 无客户端声明时，账号 override 高于全局开关。
	svc = newTTLTestGateway(t, true)
	require.Equal(t, cacheTTLTarget5m,
		svc.resolveDefaultCacheTTL(ctx, withAccount(cacheTTLTarget5m), ClientCacheTTL{}))

	// 4. 只有全局开关时用 1h。
	svc = newTTLTestGateway(t, true)
	require.Equal(t, cacheTTLTarget1h, svc.resolveDefaultCacheTTL(ctx, oauth, ClientCacheTTL{}))

	// 5. 全局关闭且无账号 override 时不注入。
	offSvc := newTTLTestGateway(t, false)
	require.Empty(t, offSvc.resolveDefaultCacheTTL(ctx, oauth, ClientCacheTTL{}))

	// 6. 非 OAuth 账号不吃全局开关。
	svc = newTTLTestGateway(t, true)
	require.Empty(t, svc.resolveDefaultCacheTTL(ctx, &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, ClientCacheTTL{}))

	// 7. 请求体 ttl 混合时无法推断意图，回落到账号设置。
	svc = newTTLTestGateway(t, true)
	require.Equal(t, cacheTTLTarget1h,
		svc.resolveDefaultCacheTTL(ctx, withAccount(cacheTTLTarget1h), ClientCacheTTL{BodyHasExplicit: true}))
}

func TestResolveCacheTTLUsageOverrideSkipsClientTTL(t *testing.T) {
	svc := newTTLTestGateway(t, true)
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{
		"cache_ttl_override_enabled": true,
		"cache_ttl_override_target":  cacheTTLTarget1h,
	}}

	if target, ok := svc.resolveCacheTTLUsageOverrideTarget(context.Background(), account); !ok || target != cacheTTLTarget1h {
		require.Failf(t, "account override should apply without client intent", "got %q,%v", target, ok)
	}

	for name, clientTTL := range map[string]ClientCacheTTL{
		"header": {HeaderTTL: cacheTTLTarget5m},
		"body":   {BodyHasExplicit: true, BodyTTL: cacheTTLTarget5m},
	} {
		ctx := WithClientCacheTTL(context.Background(), clientTTL)
		target, ok := svc.resolveCacheTTLUsageOverrideTarget(ctx, account)
		require.False(t, ok, "%s: usage override must be skipped", name)
		require.Empty(t, target)
	}
}

// 客户端要 5m、全局默认 1h 时，补默认值不能把任何一个块变成 1h，
// 否则 normalizeCacheControlTTLOrder 会把前面的 5m 抬成 1h（2 倍价）。
func TestClientFiveMinutesSurvivesTTLOrderNormalization(t *testing.T) {
	svc := newTTLTestGateway(t, true)
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	client := ClientCacheTTL{HeaderTTL: cacheTTLTarget5m}

	body := applyDefaultCacheTTL([]byte(ttlMixedBody), svc.resolveDefaultCacheTTL(context.Background(), account, client))
	body = normalizeCacheControlTTLOrder(body)

	_, messages, tools, system := collectCacheControlPaths(body)
	for _, path := range append(append(tools, system...), messages...) {
		require.Equal(t, cacheTTLTarget5m, gjson.GetBytes(body, path+".ttl").String(),
			"every breakpoint must stay 5m, got %s in %s", path, string(body))
	}
}

func TestForwardResultCarriesClientTTLFlag(t *testing.T) {
	result := ForwardResult{CacheTTLFromClient: true}
	require.True(t, result.CacheTTLFromClient)
}
