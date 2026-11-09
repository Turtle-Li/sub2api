# 提示缓存 TTL 优先级契约（2026-11-10）

## 契约

Anthropic 提示缓存的 `cache_control` 是**逐块**字段，TTL 在写入那一刻定死，之后只会被读取续命、不会升级。
一次请求的 TTL 按下列顺序解析，先命中者生效：

| 顺序 | 来源 | 作用范围 |
| --- | --- | --- |
| 1 | 请求体里已有的 `cache_control.ttl` | 该块，永不覆盖 |
| 2 | `X-Cache-TTL` 请求头（`5m`/`5min`/`300`、`1h`/`60min`/`3600`） | 本次请求所有缺省块 |
| 3 | 账号 `extra.cache_ttl_override_target`（仅 Anthropic OAuth/SetupToken） | 缺省块 |
| 4 | 全局 `enable_anthropic_cache_ttl_1h_injection` | 缺省块 |
| — | 以上都没有 | 不注入，保持客户端原样 |

两条实现约束：

1. **缺省块跟随客户端的档位**。客户端一旦表态（头或请求体），本轮所有缺省块都用同一档。
   混用 5m 与 1h 会被 `normalizeCacheControlTTLOrder` 抬升成 1h（Anthropic 拒绝 1h 断点跟在 5m 断点之后），
   那等于把客户端的 5m 偷换成 2 倍价，必须避免。
2. **客户端声明过 TTL 的请求跳过用量改写**。`resolveCacheTTLUsageOverrideTarget` 返回空，
   计费直接采用上游回报的 `cache_creation` 明细；否则会出现"请求跑 5m、账按 1h 记"的错账。

## 客户端接入

Claude Code（≥ v2.1.227）用环境变量带头，不改代码：

```bash
export ANTHROPIC_CUSTOM_HEADERS="X-Cache-TTL: 5m"     # 连续交互、任务一个接一个
export ANTHROPIC_CUSTOM_HEADERS="X-Cache-TTL: 1h"     # 长任务、中途要走开或等审核
```

也可写在 `~/.claude/settings.json` 的 `env` 块里做默认值。自定义客户端直接在 `cache_control` 上写 `ttl` 即可。

## 选档依据

盈亏平衡（以 opus-5-5 计费卡为例）：`5min < 间隔 ≤ 1h` 的读回 token ÷ 全部写入 token 需超过 **0.625**。

- 密集连续交互（95% 请求间隔 < 5 分钟）：5m，1h 只是每写一个 token 多付 0.75×P。
- 断断续续（中间停 5–60 分钟）：1h，救回的是整段前缀重写。
- 停超过 1 小时：两档都过期，1h 更贵。
- 会话中途不要来回切档：切换会让旧前缀整体失效并重写。

## 测试

- `backend/internal/service/gateway_cache_ttl_policy_test.go`：客户端 ttl 保留、缺省补齐、
  旧 `forceEphemeralCacheControlTTL` 兼容、header 归一化、请求体单值/混合探测、四层优先级、
  计费跳过、5m 请求经 `normalizeCacheControlTTLOrder` 后仍全为 5m。
- `go test ./internal/service/`、`go test -tags unit ./internal/service/`、`go test ./internal/handler/` 全绿；
  `go vet`、`gofmt -l` 无输出。

## 状态

提交于 fork `main`（`fe840dc34`），**未发布**。发布前的行为差异：

- 账号级 override 以前只改计费、不改请求体；现在也改请求体。核对是否有账号设了
  `target=5m` 却一直依赖全局 1h 的请求行为。
- `rewrite_message_cache_control`（默认 false）若开启，会先剥离 messages 断点再按 5m 重建，
  与"客户端优先"冲突；开之前需要再改一版。
