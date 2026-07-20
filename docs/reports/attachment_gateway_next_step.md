# Sub2 Attachment Gateway 最终建议

日期：2026-07-20
状态：method 0 候选已发布并完成 scoped HTTP dry-run/rewrite；API Key 27 与 admin user 1
可用，普通用户仍关闭。

## 决策

当前结论是 **“条件 Go”**：

- Go：继续保持 API Key 27 + admin user 1 的 scoped rewrite，观察目标用户下一笔真实大请求；
- Go：PNG/WebP 正缓存、q85/q90 与透明 PNG lossless 继续作为候选；
- No-Go：全量用户、默认开启、`allow_unscoped=true`；
- No-Go：与本次发布捆绑修改 Caddy、接 R2 或改 HTTP/WS 路由。

允许内部 canary 的原因：默认关闭和空 scope 都是 no-op；dry-run、5 秒时间预算、
decoded bytes/pixels/图片数/并发限制、缓存 TTL/容量清理、panic/超时 fail-open 已补齐；
HTTP、WS passthrough、WS ctx_pool、本地 413 限流、重复 hash、进程重启命中和坏缓存恢复
均有自动化证据。CGO0 构建已固定 `embed nodynamic`，linux/amd64 最终 Alpine 镜像
可启动且为静态二进制；不可取消的 WebP worker 在请求超时后也会继续占用独立并发槽，
不会因连续超时突破编码并发上限。根/部署 Dockerfile、backend Makefile 和完整/简化
GoReleaser 的 CGO0 构建入口都已覆盖，避免其他发布产物回退到动态装载路径。

仍不允许普通用户灰度的原因：生产样本量尚小；目标 Key 27 在 rewrite 后还没有新请求；
JPEG“不更小”的负结果尚未缓存；透明 lossless 只节省 12% 且冷处理约 3.15 秒；生产
没有真实 Responses WS 流量可做稳定性 A/B。

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

真实 Codex 原图/rewrite 的已知答案 A/B 全部逐字段一致。发布后 active container healthy，
发布审计 app 5xx、fatal、Caddy 5xx 均为 0。完整数据见
`docs/reports/data/attachment_gateway_production_canary_20260720.json`。

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

Sub2 Attachment Gateway 位于 Caddy 之后。2026-07-20 生产只读复核确认当前 Responses
ingress 前置门为十进制 16,000,000 bytes，因此约 14.0 MB 的请求可以进入 Sub2 并由
优化器处理；超过该门的请求仍会在到达优化器前被拒绝。

生产 canary 已验证 13,749,545 B 的授权 admin HTTP 请求可进入 Sub2 并降到
2,945,577 B。对 16 MB 以上的 Client → Caddy 413，仍需另立方案：
优先客户端/CCSwitch 本地预处理；受控提高 Caddy Responses 上限只能单独评审，不能借
本次功能顺带上线。

## 第一版应保留的功能

- 只处理 Responses JSON 中 PNG/JPEG/WebP Base64 data URL；
- `<512 KiB` 原样；普通 q85、代码/UI q90、透明 lossless；
- 不缩放、不改 `detail`；
- decoded bytes SHA-256、本地 `.webp` + `.json` 缓存；
- 原子写、0700/0600、singleflight、TTL 与容量清理；
- API key/group allowlist、dry-run、超时和 fail-open；
- HTTP 与 WS 首轮；后续 WS turn 暂不改写；
- 只记录大小、计数、耗时与缓存结果，不记录内容/hash/path。

第一版不加入 PDF、音频、视频、URL 下载、URL 替换、R2/S3 或公开静态地址。

## 是否需要 R2 / URL 化

现在不需要。data URL 已在生产重复大请求中把上游 body 降低 78.58%，R2 会新增凭据、签名
URL、对象生命周期、访问日志、区域延迟、上游拉取失败和隐私边界，却不能解决
Client → Caddy 的上传问题。

只有内部 canary 证明压缩有效，并且多实例缓存或 Sub2 出站成为新瓶颈时，再单独评估
私有对象存储与短时签名 URL。

## ctx_pool 是否继续

继续。Attachment Gateway 解决图片 payload；ctx_pool 解决长上下文、连接复用和响应链
状态，两者互补。

本地 handler→WS upstream 已通过以下四项：

| 附件状态 | WS 模式 | 结果 |
|---|---|---|
| dry-run | passthrough | 通过，原 frame 转发 |
| rewrite | passthrough | 通过，WebP frame 转发 |
| dry-run | ctx_pool | 通过，原 frame 转发 |
| rewrite | ctx_pool | 通过，WebP frame 转发 |

全仓既有 ctx_pool 多 turn、failover、HTTP bridge 和生命周期测试也通过。当前附件 hook
只覆盖首轮，不能把“首轮通过”误写成“后续 turn 已压缩”。生产 canary 必须继续观察
`read_upstream`、timeout、TTFT 与连接复用。

OpenAI 官方文档在最终复核时仍明确支持 Responses `input_image` 的 Base64 data URL、
多图输入及非动画 WebP，因此第一版继续输出 `data:image/webp;base64,...`，并原样保留
`detail`；不需要为协议兼容提前引入 R2。

## 后续扩量条件

HTTP body、质量、cache hit、资源收敛和一键关闭已通过；真实 WS 与目标 Key 27 的下一笔
大请求仍待观察。只有同时满足以下条件才建议扩大到更多内部 key：

- rewrite 后 body 降幅 ≥70%；
- 原图/WebP 的真实模型 OCR、代码、UI、照片结构化评分无业务可见退化；
- 重复 hash 出现 cache hit，热耗时显著低于冷耗时；
- HTTP 与 ctx_pool 均无新增 timeout、异常 close 或 `read_upstream` retry；
- 单实例 CPU/RSS/磁盘在预算内，`timed_out`/`errors` 不触发停止线；
- 生产宿主机原生 amd64 的冷/热 p50/p95 与模拟环境方向一致，且没有超时后 CPU worker
  堆积；
- 一键关闭后请求立即恢复原行为。

在这些数据产生前，`attachment_optimizer_enabled` 的生产默认值必须保持 `false`。
