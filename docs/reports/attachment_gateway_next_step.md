# Sub2 Attachment Gateway 最终建议

日期：2026-07-20
状态：Phase 1.1 本地硬化与生产镜像门禁完成；尚未发布、push 或连接生产。

## 决策

当前结论是 **“条件 Go”**：

- Go：把代码作为默认关闭的生产候选发布；
- Go：用户醒来并确认后，仅对一个内部 API key/group 做受控 canary；
- No-Go：全量用户、默认开启、`allow_unscoped=true`；
- No-Go：与本次发布捆绑修改 Caddy、接 R2 或改 HTTP/WS 路由。

允许内部 canary 的原因：默认关闭和空 scope 都是 no-op；dry-run、5 秒时间预算、
decoded bytes/pixels/图片数/并发限制、缓存 TTL/容量清理、panic/超时 fail-open 已补齐；
HTTP、WS passthrough、WS ctx_pool、本地 413 限流、重复 hash、进程重启命中和坏缓存恢复
均有自动化证据。CGO0 构建已固定 `embed nodynamic`，linux/amd64 最终 Alpine 镜像
可启动且为静态二进制；不可取消的 WebP worker 在请求超时后也会继续占用独立并发槽，
不会因连续超时突破编码并发上限。根/部署 Dockerfile、backend Makefile 和完整/简化
GoReleaser 的 CGO0 构建入口都已覆盖，避免其他发布产物回退到动态装载路径。

仍不允许普通用户灰度的原因：大图冷处理仍为 3.3–4.0 秒，累计分配约
266–310 MB/op；热缓存仍为 213–259 ms、54–66 MB/op；生产 Linux/Xeon 的 p95/p99
和真实视觉模型语义 A/B 尚未完成。

## 已达到的效果

| 项目 | 最新结果 |
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

生产 canary 可直接观察当前约 14.0 MB 的授权目标请求，验证 Sub2 → upstream 压缩、
质量、cache hit 和 WS 稳定性。对 16 MB 以上的 Client → Caddy 413，仍需另立方案：
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

现在不需要。data URL 已经在本地把上游 body 降低 83%–89%，R2 会新增凭据、签名
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

## 明日完成条件

按 `attachment_gateway_production_canary.md` 执行。只有同时满足以下条件才建议扩大到
更多内部 key：

- rewrite 后 body 降幅 ≥70%；
- 原图/WebP 的真实模型 OCR、代码、UI、照片结构化评分无业务可见退化；
- 重复 hash 出现 cache hit，热耗时显著低于冷耗时；
- HTTP 与 ctx_pool 均无新增 timeout、异常 close 或 `read_upstream` retry；
- 单实例 CPU/RSS/磁盘在预算内，`timed_out`/`errors` 不触发停止线；
- 生产宿主机原生 amd64 的冷/热 p50/p95 与模拟环境方向一致，且没有超时后 CPU worker
  堆积；
- 一键关闭后请求立即恢复原行为。

在这些数据产生前，`attachment_optimizer_enabled` 的生产默认值必须保持 `false`。
