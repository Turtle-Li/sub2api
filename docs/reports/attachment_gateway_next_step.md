# Sub2 Attachment Gateway 最终建议

日期：2026-07-21
状态：method 0 与 R2 URL MVP 已发布并完成 scoped HTTP/WS canary；API Key 27 与
admin user 1 可用，普通用户仍关闭。Responses Caddy 原始入站为 256,000,000 B，其他 API
仍为 16,000,000 B；16,000,000 B 上游保险覆盖 HTTP 与 WS 首帧，但 `http_bridge`
后续上下文 replay 目前不受它覆盖。

> 2026-07-21 00:20 补充：R2/URL 化已进入“本地实现和自动回归完成、待真实 canary”阶段。
> 新增实现使用独立加密设置和管理后台，不复用备份/异步生图凭证；尚未 push 或部署，
> 因此本文下方“现在不需要”的历史生产决策应理解为“不应在无真实验证时直接全量接生产”。

> 2026-07-21 05:46 补充：`ctx_pool` stale retry 本地修复已完成。安全重试强制新拨、
> `store=false + strict` 无 sticky 时强制新拨、stale binding compare-and-delete，以及池内
> 两条 stale 后必须拨第三条 fresh 的回归均通过；WS 后续 turn Attachment hook 也已补齐。
> 这些改动尚未发布，生产仍保持 `http_bridge`，结论需以隔离 canary 为准。

> 2026-07-20 补充：请求级附件预算、聚合小图策略与“256MB Responses 入站 / 16MB
> 实际上游转发保险”已完成生产 canary。设计、真实小图流量与切换顺序见
> `docs/reports/attachment_gateway_request_budget.md`。

## 决策

当前结论是 **“条件 Go”**：

- Go：继续保持 API Key 27 + admin user 1 的 scoped rewrite，观察目标用户下一笔真实大请求；
- Go：PNG/WebP 正缓存、q85/q90 与透明 PNG lossless 继续作为候选；
- Go：暂时保留 Responses 256,000,000 B 入站与 HTTP/WS 首帧 16,000,000 B 上游保险；
- No-Go：全量用户、默认开启、`allow_unscoped=true`；
- No-Go：把 R2 扩到现有精确 scope 之外，或改变 HTTP/WS 路由默认值。

允许内部 canary 的原因：默认关闭和空 scope 都是 no-op；dry-run、5 秒时间预算、
decoded bytes/pixels/图片数/并发限制、缓存 TTL/容量清理、panic/超时 fail-open 已补齐；
HTTP、WS passthrough、WS ctx_pool、本地 413 限流、重复 hash、进程重启命中和坏缓存恢复
均有自动化证据。CGO0 构建已固定 `embed nodynamic`，linux/amd64 最终 Alpine 镜像
可启动且为静态二进制；不可取消的 WebP worker 在请求超时后也会继续占用独立并发槽，
不会因连续超时突破编码并发上限。根/部署 Dockerfile、backend Makefile 和完整/简化
GoReleaser 的 CGO0 构建入口都已覆盖，避免其他发布产物回退到动态装载路径。

仍不允许普通用户灰度的原因：生产样本量尚小；目标 Key 27 在 rewrite 后还没有真实图片
大包；负结果缓存尚未发布 canary；透明 lossless 存在低收益/超时样本；`ctx_pool` 仍有
stale connection 1011，而稳定的 `http_bridge` 会重复上传长上下文。

## 生产 canary 新证据

method 6 第一轮因 JPEG/透明 PNG 达到 5 秒预算而停止并回 `off`。method 0 保持同一
质量参数，仅降低 libwebp 搜索强度；生产四格式冷处理降到 1.08–3.15 秒，0 超时、0 错误。

| 项目 | 生产结果 |
|---|---:|
| PNG HTTP | 1,013,836 → 112,653 B；冷 1,078.8 ms，热 87.0 ms |
| JPEG HTTP | 754,909 B 原样；再次编码不足 5% 节省 |
| WebP HTTP | 1,759,087 → 408,591 B；冷 1,326.9 ms，热 154.1 ms |
| 透明 PNG lossless | 3,305,473 → 2,908,030 B；冷 3,149.9 ms，热 347.0 ms |
| 5 图 HTTP | 7,662,531 → 4,112,226 B；4 hits；2,602.7 ms |
| 8 张相同 WebP HTTP | 13,749,545 → 2,945,577 B；8 hits；984.4 ms |
| 12 张相同 WebP / 旧门以上 | 17,485,303 → 3,985,423 B；10 hits；2,705.1 ms；HTTP 200 |
| 8 张小 UI PNG | 3,058,674 → 272,922 B；8 hits；OCR 精确 |

真实 Codex 原图/rewrite 的已知答案 A/B 全部逐字段一致。发布后 active container healthy，
发布审计 app 5xx、fatal、Caddy 5xx 均为 0。完整数据见
`docs/reports/data/attachment_gateway_production_canary_20260720.json`。

台式机直连模型质量补测中，PNG、JPEG、WebP 压缩后均准确识别乌龟、电路、对话气泡和
颜色；聚合小图 q90 后仍准确读取品牌、邮箱与中文 UI 字段。该结果验证的是模型实际读取
压缩后 payload，不只是像素指标。

## Phase 1.1 method 6 本地历史结果

| 项目 | 当时结果 |
|---|---:|
| 单张大 PNG body | 14,590,515 → 1,577,556 B（-89.19%） |
| 5 图 body | 12,241,951 → 1,340,404 B（-89.05%） |
| 大图 + 1 MiB context | 15,639,091 → 2,626,132 B（-83.21%） |
| WS 首帧 fixture | 713,641 → 19,134 B（-97.32%） |
| 1 MiB 模拟上游限制 | 原图 413；优化后 204 |
| 重复 hash | 冷 685 ms；热 43 ms（中型 fixture） |
| 生产标签基准 | `nodynamic` 三组冷 3.31–4.01 s，body 与压缩率不变 |
| amd64 最终 Alpine 镜像 | 可启动、静态；2,448,643 → 267,360 B，热缓存 62.6 ms（模拟环境） |
| 代码 OCR | q85=0.9799，q90=0.9698 |
| UI 小字 OCR | q85=0.9871，q90=1.0000 |
| UI edge F1 | q85/q90=1.0000 |
| 照片 PSNR | q85=29.03 dB，q90=29.31 dB |

三次相同大图 failover 的理论请求重放量从 43,771,545 B 降至 4,732,668 B。
这能降低 upstream bandwidth protection 和重放压力，但真实 OpenAI 指标要在 canary
中确认。

## Caddy 413 的硬边界

Sub2 Attachment Gateway 位于 Caddy 之后，因此生产已把 Responses 专用原始入站门独立
提高到十进制 256,000,000 B；非 Responses 仍为 16,000,000 B。精确边界验证为：

- Responses `256,000,001 B` → Caddy 413；
- 非 Responses `16,000,001 B` → Caddy 413；
- Responses `17,485,303 B` → 进入 Sub2，压到 `3,985,423 B`，模型 HTTP 200。

这解决了旧 16 MB Caddy 门阻止优化器运行的问题，但不代表 256 MB 原始请求必然成功。
压缩失败、不可支持附件或压后仍超过 16,000,000 B 时，HTTP 与 WS 首帧会在
上游/failover 前拒绝。`http_bridge` 后续 replay 是已确认例外：生产已成功转发最高
18.90MB 的累积 body，需单独增加 observe-only 预算与修复方案。

## 第一版应保留的功能

- 只处理 Responses JSON 中 PNG/JPEG/WebP Base64 data URL；
- `<512 KiB` 原样；普通 q85、代码/UI q90、透明 lossless；
- 不缩放、不改 `detail`；
- decoded bytes SHA-256、本地 `.webp` + `.json` 缓存；
- 原子写、0700/0600、singleflight、TTL 与容量清理；
- “压缩不足 5%”负结果缓存（独立指标、policy/encoder 版本、24h TTL、条目上限）；
- API key/group allowlist、dry-run、超时和 fail-open；
- HTTP 与 WS 首轮；后续 WS turn 暂不改写；
- 只记录大小、计数、耗时与缓存结果，不记录内容/hash/path。

第一版不加入 PDF、音频、视频、任意 URL 下载或公开静态地址。R2 短时签名 URL 已作为
独立 Phase 1.2 能力进入精确 scoped canary，不改变第一版默认关闭和 fail-open 边界。

## 是否需要 R2 / URL 化

现阶段不应直接全量启用。data URL 已在生产重复大请求中把上游 body 降低 78.58%，R2
仍不能解决 Client → Caddy 的第一次上传，但可以继续降低 Sub2 → OpenAI 的 5Mbps 上行、
覆盖复杂透明图/已压缩图等“压不小但可 URL 化”的场景，并让重复 hash 跨重启复用对象。

私有对象存储与短时签名 URL 已完成真实 scoped canary：R2 Put→签名 GET→Delete、OpenAI
拉取、HTTP 图片矩阵和 WS `http_bridge` 均通过，当前仍只对 API Key 27 + admin user 1
开启。R2 不需要扩大权限或 scope；下一门槛是负结果缓存 canary 与目标 Key 27 的真实大包。

## ctx_pool 是否继续

继续作为独立研发方向；**当前生产仍保持 `http_bridge`，本地修复进入待隔离 canary**。
Attachment Gateway 解决图片 payload；ctx_pool 解决长上下文、连接复用和响应链状态，
两者仍互补。

本地 handler→WS upstream 已通过以下四项：

| 附件状态 | WS 模式 | 结果 |
|---|---|---|
| dry-run | passthrough | 通过，原 frame 转发 |
| rewrite | passthrough | 通过，WebP frame 转发 |
| dry-run | ctx_pool | 通过，原 frame 转发 |
| rewrite | ctx_pool | 通过，WebP frame 转发 |

全仓既有 ctx_pool 多 turn、failover、HTTP bridge 和生命周期测试也通过。本地候选已经
把附件 hook 扩展到 `ctx_pool`、`http_bridge` 和 passthrough 后续 turn，但生产版本仍只有
首轮能力。生产 canary 必须继续观察 `read_upstream`、timeout、TTFT 与连接复用。

生产 A/B 随后证明：`ctx_pool` 文本 5-turn 和新建 R2 图片连接可成功，但两次已有图片
连接复用均以 1011 `keepalive ping timeout` 结束，并出现 `read_upstream` retry；retry
没有可靠换成新连接。账号 2 已恢复 `http_bridge`，恢复后同连接 2-turn smoke 通过。
本地已修 stale-connection checkout/换连逻辑；下一步必须用隔离账号重测，不能直接据此
切换承载真实流量的统一默认。

恢复后的 bridge 虽然稳定，但 `store=false` 长会话的首帧约 42.9KB、第二 turn replay
已达 16.33MB，随后最高 18.90MB；27 个 ≥16MB turn 均完成，首 token 多为 32–43 秒。
因此短期保持 bridge 只代表“稳定优先”，不代表它解决了上行压力。不能直接加 16MB
中途拒绝；应先观察 bridge 后续 turn 预算，同时优先修复 ctx_pool 的 stale 换连。

OpenAI 官方文档在最终复核时仍明确支持 Responses `input_image` 的 Base64 data URL、
HTTPS URL、多图输入及非动画 WebP。压缩层继续保留 `data:image/webp;base64,...` 能力并
原样保留 `detail`；只有 scoped R2 外置层再把候选替换成短时签名 URL。

## 后续扩量条件

HTTP body、质量、cache hit、资源收敛和一键关闭已通过；真实 WS 与目标 Key 27 的下一笔
大请求仍待观察。只有同时满足以下条件才建议扩大到更多内部 key：

- rewrite 后 body 降幅 ≥70%；
- 原图/WebP 的真实模型 OCR、代码、UI、照片结构化评分无业务可见退化；
- 重复 hash 出现 cache hit，热耗时显著低于冷耗时；
- HTTP 与生产默认 `http_bridge` 均无新增 timeout、异常 close 或 `read_upstream` retry；
- bridge 后续 turn 的 replay 大小、TTFT 与上行消耗有明确预算，不能再依赖首帧 16MB cap；
- `ctx_pool` 单独完成 stale-connection 修复与隔离回归，不能作为 Attachment 扩量的默认
  传输模式；
- 单实例 CPU/RSS/磁盘在预算内，`timed_out`/`errors` 不触发停止线；
- 生产宿主机原生 amd64 的冷/热 p50/p95 与模拟环境方向一致，且没有超时后 CPU worker
  堆积；
- 一键关闭后请求立即恢复原行为。

在这些数据产生前，`attachment_optimizer_enabled` 的生产默认值必须保持 `false`。
