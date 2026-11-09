package service

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// CacheTTLHeader 是客户端显式声明本次请求缓存 TTL 的请求头。
// 取值 5m/5min/300 或 1h/60m/3600。它只作用于没有自带 cache_control.ttl 的块，
// 已经自带 ttl 的块完全不动。
const CacheTTLHeader = "X-Cache-TTL"

// ClientCacheTTL 描述一次请求里客户端表达的 TTL 意图。
//
// 优先级：客户端（逐块 cache_control.ttl / X-Cache-TTL 请求头） > 账号
// cache_ttl_override_target > 全局 1h 注入开关。
//
// 客户端一旦表态，本次请求所有缺省块都跟随客户端的档位：混用 5m 与 1h 会被
// normalizeCacheControlTTLOrder 抬升成 1h（Anthropic 不接受 1h 断点跟在 5m 断点
// 之后），那等于偷偷把客户端的 5m 换成了 2 倍价，必须避免。
type ClientCacheTTL struct {
	// HeaderTTL 是请求头解析出的 TTL（"5m"/"1h"），缺省或非法时为空。
	HeaderTTL string
	// BodyTTL 是请求体里唯一的显式 ttl 值；出现多种取值时为空。
	BodyTTL string
	// BodyHasExplicit 表示客户端请求体里至少有一个块自带 cache_control.ttl。
	BodyHasExplicit bool
}

// Preferred 返回客户端想要用的档位（请求头优先，其次请求体里唯一的取值）。
func (c ClientCacheTTL) Preferred() string {
	if c.HeaderTTL != "" {
		return c.HeaderTTL
	}
	return c.BodyTTL
}

// Present 表示客户端是否对本次请求的 TTL 做了任何显式声明。
func (c ClientCacheTTL) Present() bool { return c.HeaderTTL != "" || c.BodyHasExplicit }

type clientCacheTTLKeyType struct{}

// ClientCacheTTLContextKey 客户端 TTL 意图的上下文键。
var ClientCacheTTLContextKey = clientCacheTTLKeyType{}

// WithClientCacheTTL 把客户端 TTL 意图写入 ctx，供 Forward 与计费链路共享同一个判断。
func WithClientCacheTTL(ctx context.Context, value ClientCacheTTL) context.Context {
	return context.WithValue(ctx, ClientCacheTTLContextKey, value)
}

// ClientCacheTTLFromContext 读取客户端 TTL 意图。
func ClientCacheTTLFromContext(ctx context.Context) (ClientCacheTTL, bool) {
	if ctx == nil {
		return ClientCacheTTL{}, false
	}
	value, ok := ctx.Value(ClientCacheTTLContextKey).(ClientCacheTTL)
	return value, ok
}

// NormalizeCacheTTL 把客户端写法归一成 "5m"/"1h"。
func NormalizeCacheTTL(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "5m", "5min", "5minute", "5minutes", "300", "300s":
		return cacheTTLTarget5m, true
	case "1h", "60m", "60min", "3600", "3600s", "1hour":
		return cacheTTLTarget1h, true
	}
	return "", false
}

// ResolveClientCacheTTL 汇总本次请求的客户端 TTL 意图：
// 请求头（若合法）+ 请求体里已有的显式 ttl。
func ResolveClientCacheTTL(c *gin.Context, body []byte) ClientCacheTTL {
	out := ClientCacheTTL{}
	out.BodyHasExplicit, out.BodyTTL = bodyCacheTTL(body)
	if c != nil {
		if ttl, ok := NormalizeCacheTTL(c.GetHeader(CacheTTLHeader)); ok {
			out.HeaderTTL = ttl
		}
	}
	return out
}

// bodyCacheTTL 扫描请求体里的 cache_control 块：
// 返回是否存在显式 ttl，以及唯一取值（出现多种时返回空串）。
func bodyCacheTTL(body []byte) (bool, string) {
	if len(body) == 0 {
		return false, ""
	}
	found := false
	values := map[string]struct{}{}
	collect := func(value gjson.Result) {
		cc := value.Get("cache_control")
		if !cc.IsObject() {
			return
		}
		ttl := strings.TrimSpace(cc.Get("ttl").String())
		if ttl == "" {
			return
		}
		found = true
		values[ttl] = struct{}{}
	}

	if cc := gjson.GetBytes(body, "cache_control"); cc.IsObject() {
		collect(gjson.Result{Raw: `{"cache_control":` + cc.Raw + `}`})
	}
	for _, path := range []string{"system", "messages", "tools"} {
		node := gjson.GetBytes(body, path)
		if !node.IsArray() {
			continue
		}
		node.ForEach(func(_, value gjson.Result) bool {
			if path == "messages" {
				content := value.Get("content")
				if content.IsArray() {
					content.ForEach(func(_, block gjson.Result) bool {
						collect(block)
						return true
					})
				}
				return true
			}
			collect(value)
			return true
		})
	}

	if !found {
		return false, ""
	}
	if len(values) == 1 {
		for v := range values {
			return true, v
		}
	}
	return true, ""
}

// resolveDefaultCacheTTL 决定本次请求要注入的默认 TTL（仅用于没有自带 ttl 的块）。
// 返回空串表示不做任何注入。
func (s *GatewayService) resolveDefaultCacheTTL(ctx context.Context, account *Account, client ClientCacheTTL) string {
	if preferred := client.Preferred(); preferred != "" {
		return preferred
	}
	if account != nil && account.IsCacheTTLOverrideEnabled() {
		return account.GetCacheTTLOverrideTarget()
	}
	if s.shouldInjectAnthropicCacheTTL1h(ctx, account) {
		return cacheTTLTarget1h
	}
	return ""
}
