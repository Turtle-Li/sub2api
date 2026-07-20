# Sub2 Attachment Gateway Phase 1 实验报告

日期：2026-07-20
范围：本地/生产镜像测试、受控生产 HTTP canary 与真实 Codex 图片质量 A/B。

> **2026-07-20 生产状态更新**：method 0 候选已发布，运行态仅允许 API Key 27 与
> admin user 1，`allow_unscoped=false`。HTTP dry-run/rewrite 与真实视觉 A/B 已通过；
> 普通用户仍未开启。生产没有可用的 Responses WS 流量，因此 WS 结论仍仅来自自动化矩阵。

> **2026-07-20 19:19 入站更新**：独立 Caddy 变更已把 `/v1/responses` 与 `/responses`
> 原始入站上限从 64,000,000 B 提高到 256,000,000 B；其他 API 仍为 16,000,000 B，
> Sub2→OpenAI 实际 Responses body 仍有 16,000,000 B 硬保险。台式机直连的
> 17,485,303 B / 12 图 HTTP 请求被压到 3,985,423 B 后返回 200，模型内容 marker
> 正确，热缓存命中 10 次。PNG/JPEG/WebP 语义识别和 8 张聚合小 UI 图 OCR 均通过。

## 生产受控 canary 结果

完整结构化数据：`docs/reports/data/attachment_gateway_production_canary_20260720.json`。

### 编码器停止线与修正

第一轮生产 dry-run 使用 libwebp `method=6`。PNG 与 WebP 分别耗时 3.625 秒和
4.147 秒；JPEG 与透明 PNG 均达到 5 秒预算并 fail-open。现场 CPU 约 102%，RSS
约 421 MiB，因此按运行手册立即切回 `off`。关闭后 CPU 回到 4.5%，RSS 回到 84 MiB。

根因不是模型推理：method 6 是 libwebp 最慢的压缩搜索档位。改为保持同一
q85/q90/lossless 质量策略的 `method=0` 后，生产 dry-run 四种格式均未超时：

| 类型 | 原 body → 可转发 body | 降幅 | 冷处理 | 结果 |
|---|---:|---:|---:|---|
| 不透明 PNG / q90 | 1,013,836 → 112,653 B | 88.89% | 1,078.8 ms | 成功 |
| JPEG / q85 | 754,909 → 754,909 B | 0% | 1,819.6 ms | 不足 5% 节省，原样 |
| WebP / q85 | 1,759,087 → 408,591 B | 76.77% | 1,326.9 ms | 成功 |
| 透明 PNG / lossless | 3,305,473 → 2,908,030 B | 12.02% | 3,149.9 ms | 成功 |

JPEG 源文件在桌面为 3.88 MB，但 Codex 客户端发送前已缩放/压缩为 531,361 B；再次
转 WebP 没有达到 `min_savings_ratio=5%`，所以安全保留原图。这说明“支持 JPEG”不等于
强制改写所有 JPEG。

### HTTP rewrite、缓存与内容质量

dry-run 产生的 decoded-hash 正缓存被 rewrite 复用：

| 类型 | 冷处理 | 热缓存 | cache hit | 实际上游 body |
|---|---:|---:|---:|---:|
| PNG | 1,078.8 ms | 87.0 ms | 1 | 112,653 B |
| WebP | 1,326.9 ms | 154.1 ms | 1 | 408,591 B |
| 透明 PNG | 3,149.9 ms | 347.0 ms | 1 | 2,908,030 B |

5 图 HTTP 请求为 7,662,531 → 4,112,226 B，4 个正缓存命中，1 个 JPEG 原样，处理
2,602.7 ms。8 张相同 WebP 的重复大请求为 **13,749,545 → 2,945,577 B**
（下降 78.58%），`cache_hits=8`，处理 984.4 ms，直接证明 HTTP repeated hash 缓存有效。

真实 Codex 使用固定已知答案图片验证代码 token、大小写、标点、数字、UI 字段、普通图
数量/颜色和透明 PNG 文本。原图 dry-run 与 WebP rewrite 的全部结构化答案逐字段一致；
5 图与 8 张重复图联合请求也一致。模型端总时延与 `optimize_duration_ms` 分开记录，未把
模型生成时间误当作压缩耗时。

### WS 边界

台式机 Codex 0.145.0-alpha.18 已移除 `responses_websockets` 与
`responses_websockets_v2` feature；生产最近 2 小时的 Responses usage 也全部为 HTTP。
canary 期间 `read_upstream` / `write_upstream` 事件均为 0，但这只能表示没有观察到 WS
错误，不能证明真实 WS 稳定性。method 0 改动后的普通与 `nodynamic` 全仓测试、WS
passthrough/ctx_pool 自动化回归均通过。

## Phase 1.1 硬化结果

### 当前判定

- **允许发布“默认关闭”的代码**：是；关闭路径逐字节 no-op，且不创建缓存。
- **允许生产内部灰度**：有条件允许，只能显式 API key/user/group，先 dry-run、再 rewrite。
- **允许全量生产默认开启**：否；CPU、累计分配、真实模型语义 A/B 和生产 Linux 数据仍不够。
- **生产镜像兼容性**：已通过；CGO0 构建固定 `embed nodynamic`，linux/amd64
  最终 Alpine 镜像可启动且二进制为静态链接。
- **Caddy**：Phase 1 初始发布未改；随后作为独立受控变更把 Responses 入站提高到
  256,000,000 B，并保留 16,000,000 B 上游硬保险。
- **R2**：本阶段不做。

新增护栏：

- `attachment_optimizer_dry_run=true`：执行测量和缓存，但上游仍收到原 body；
- `allow_unscoped=false`：即使 feature enabled，空白名单仍是安全 no-op；
- API key/user/group allowlist；
- 可选 `rollout_control_file`，以 `off` / `dry_run` / `rewrite` 实时切换；缺失、超长或
  非法内容 fail-closed 为 `off`；
- `optimize_timeout_ms=5000`，超时或取消整请求 fail-open；
- 请求侧 Base64/decode 槽与 worker 侧 decode/encode 槽双重限流；即使请求超时后
  第三方编码器暂未退出，实际后台编码仍不会突破配置并发；
- 单图 64 MiB、单请求 decoded 图片总量 64 MiB、50 MP、20 图上限；
- 缓存 7 天 TTL、512 MiB 容量、10 分钟清理节流；
- 精确缓存 pair 扫描、过期删除、按最旧 entry 淘汰，未知文件与临时文件不动；
- transform panic、坏缓存、超时均回退原 payload。

### 最新性能

数据：`docs/reports/data/attachment_gateway_phase1_1_benchmark.json`。

| 场景 | body | 降幅 | 冷处理 | 热缓存 | 冷累计分配 | 热累计分配 |
|---|---:|---:|---:|---:|---:|---:|
| 单张大 PNG | 14,590,515 → 1,577,556 B | 89.19% | 4.030 s | 252 ms | 307.9 MB/op | 63.8 MB/op |
| 5 张图片 | 12,241,951 → 1,340,404 B | 89.05% | 3.296 s | 213 ms | 265.8 MB/op | 53.7 MB/op |
| 大图 + 1 MiB 上下文 | 15,639,091 → 2,626,132 B | 83.21% | 3.868 s | 259 ms | 310.0 MB/op | 65.9 MB/op |

整个 benchmark 进程（含 fixture 生成）的最大 RSS 为 415,694,848 B；它不是单请求
RSS，但再次说明不能无范围全量开启。三次 failover 重放同一大图时，理论上游请求字节
从 43,771,545 B 降到 4,732,668 B。

本地 1 MiB 上游保护模拟：

- 原 body 2,448,643 B → HTTP 413；
- 优化 body 267,360 B → HTTP 204；
- 降幅 89.08%；
- 冷处理 685 ms，重复 hash 热缓存 43 ms。

生产构建标签 `nodynamic` 的三组原生 arm64 基准与上表一致：冷处理
3.309–4.015 秒、热缓存 212–258 ms，body 字节完全相同。最终 linux/amd64 Alpine
镜像经本机 Rosetta/QEMU 功能验证时，同一 1 MiB 限制用例为 2,448,643 →
267,360 B，冷 1.250 秒、热 62.6 ms；该组只用于验证真实生产镜像内的静态
WebP 路径，不能当作生产 Xeon 性能数据。

这证明方向能消除 **Sub2 → upstream** 的 body-limit 413。初始生产门为十进制
16,000,000 B，随后已通过独立变更把 Responses 原始入站门提高到 256,000,000 B；
因此大 body 可先进入优化器，但只有压缩后不超过 16,000,000 B 才会继续上游转发。

### 缓存验证

已自动验证：

- 同图第二次请求命中；
- 新建 Gateway（模拟进程重启）仍命中磁盘缓存；
- Base64 中加入换行不改变 decoded hash；
- `.webp` hash 不符或 metadata 损坏时拒绝命中并安全重建；
- 12 个相同 hash 并发只编码一次；
- TTL 删除、容量淘汰、未知文件保护；
- 缓存目录/文件权限和原子写保持不变。

`singleflight` 仍只覆盖单进程，多副本共享目录没有跨进程锁，这是全量前风险，不阻止
单实例/单 key 内部 canary。

### 图片内容质量

| 样本 | q85 | q90 | 门槛 |
|---|---:|---:|---:|
| 代码 OCR 相似度 | 0.9799 | 0.9698 | ≥0.95 |
| UI dashboard 小字 OCR | 0.9871 | 1.0000 | ≥0.95 |
| UI edge F1 | 1.0000 | 1.0000 | ≥0.90 |
| 照片 PSNR | 29.03 dB | 29.31 dB | ≥25 dB |

离线门禁全部通过，透明 alpha 与宽高也继续通过。但这不是视觉模型语义等价证明；真实
OpenAI 原图/q85/q90 A/B 必须在内部 canary 完成后才能决定普通用户灰度。

### HTTP 与 WS/ctx_pool

- HTTP handler → capture upstream：PNG 改写为 WebP，`detail=high` 保留；
- WebSocket 首帧：passthrough/ctx_pool × dry-run/rewrite 四种组合全部完成；
- 测试 WS frame 从 713,641 B 降到 19,134 B（97.32%）；
- 全仓既有 ctx_pool 多 turn、failover、HTTP bridge、passthrough 测试继续通过；
- 已知边界不变：Attachment Gateway 只处理 WS 首轮，后续 turn 暂不改写。

### 最终验证

以下均通过：非缓存 `go test -count=1 ./...`、非缓存
`go test -tags nodynamic -count=1 ./...`、定向 `go vet`、缓存/并发/超时定向
`go test -race`、`go mod verify`、`git diff --check`。根 Dockerfile 与
`deploy/Dockerfile` 的 linux/amd64 Alpine 构建和启动均通过；`backend/Makefile`、
完整/简化 GoReleaser 的 CGO0 路径也统一使用 `nodynamic`，GoReleaser 等价参数产物
已完成 linux/amd64、linux/arm64、darwin/amd64、darwin/arm64、windows/amd64
交叉编译，两个 Linux 架构均在裸 Alpine 启动。最终镜像内还通过 413 loopback 与
“超时后台编码仍占用并发槽”测试。该轮本地门禁当时保持 `deploy/Caddyfile` 零差异，
生产连接与 OpenAI 调用均为 0；后续生产 canary 与独立 Caddy 变更见文首更新。

## 1. Phase 1 初始结论与 Phase 1.1 修正

图片优化方向在 **Sub2 → OpenAI 请求体** 上显著有效：三个目标场景的 body 降幅为 **83.21%–89.19%**，其中 14,590,515 B 可降到 1,577,556 B。

初始实现当时**不是生产候选**：冷处理 wall time 为 3.19–3.80 秒，累计分配量约
254–295 MB/op。Phase 1.1 已补齐范围控制、dry-run、资源限制、缓存治理、超时/panic
fail-open、后台编码并发约束和生产镜像验证，因此当前修正为：**可发布默认关闭代码，
可做单内部 key 条件灰度；仍不可默认开启或普通用户扩量**。热缓存仍需定位 JSON 字段、
解码 base64、计算 decoded hash、读缓存和重新编码 base64，所以不是零成本路径。

初始生产只读复核时 Caddy 门为十进制 16,000,000 B；13,749,545 B 的真实 HTTP
请求进入 Sub2 并降到 2,945,577 B。随后 Responses 入站已独立提高到 256,000,000 B，
并用 17,485,303 B → 3,985,423 B 的 HTTP 200 canary 验证。实际 OpenAI 转发仍限制在
16,000,000 B，因此本模块继续保护上游 payload、带宽和 failover 重放量。

## 2. 已实现内容

- 独立 `internal/service/attachment_gateway` 模块；
- 默认关闭的 `attachment_optimizer_enabled`；
- Responses `input_image.image_url` 与兼容 `image_url` 检测；
- PNG/JPEG/WebP Base64 data URL；
- `<512 KiB` 跳过；普通 q85、疑似代码/UI q90、透明图 lossless；
- 不缩放、不改 `detail`；
- decoded bytes SHA-256 缓存；
- `.webp` + `.json` metadata、原子写、owner-only 权限；
- 进程内 `singleflight` 与编码并发槽；
- 每图 fail-open；
- HTTP bare Responses 与 WS 首轮接入；
- 请求级隐私安全指标；
- 非图片大整数保持原值的回归保护。

未实现：PDF、Office、音频、视频、动画、URL 下载、`file_id`、R2/S3、URL 替换、
跨进程锁、WS 后续 turn 改写。

## 3. 测试环境与安全边界

```text
OS/Arch: darwin/arm64
CPU: Apple M1 Pro
Go toolchain: 项目当前工具链
网络目标: 仅 127.0.0.1 loopback mock
OpenAI 请求: 0
生产请求: 0
远程服务器连接: 0
```

未修改：

- `deploy/Caddyfile`；
- 生产配置；
- HTTP/WS 路由；
- 旧 handler/forward 分支；
- 数据库或 Redis；
- R2/S3 或签名 URL。

## 4. 请求体与性能结果

数据源：`docs/reports/data/attachment_gateway_phase1_benchmark.json`。

| 场景 | 原 body | 优化后 body | 降幅 | 冷处理 wall time | 热缓存 wall time |
|---|---:|---:|---:|---:|---:|
| Case A：单张大 PNG | 14,590,515 B | 1,577,556 B | 89.19% | 3.783 s | 183 ms |
| Case B：5 张图片 | 12,241,951 B | 1,340,404 B | 89.05% | 3.186 s | 150 ms |
| Case C：大图 + 1 MiB 上下文 | 15,639,091 B | 2,626,132 B | 83.21% | 3.799 s | 184 ms |

### 4.1 分配量

`benchmem` 的 B/op 是一次操作的**累计分配量**，不是峰值 RSS：

| 场景 | 冷处理 B/op | 热缓存 B/op |
|---|---:|---:|
| 单张大 PNG | 293,295,848 | 49,237,416 |
| 5 张图片 | 254,116,632 | 41,500,968 |
| 大图 + 1 MiB 上下文 | 295,384,384 | 51,334,632 |

这仍足以说明当前实现会对小规格实例造成显著 GC 和并发内存压力。尚未采集峰值 RSS、CPU profile 或真实生产 Xeon 数据。

### 4.2 5 Mbps 理论上传下限

仅按 `bytes × 8 / 5,000,000` 估算：

| 场景 | 原始 | 优化后 |
|---|---:|---:|
| 单张大 PNG | 23.34 s | 2.52 s |
| 5 张图片 | 19.59 s | 2.14 s |
| 大图 + 1 MiB 上下文 | 25.02 s | 4.20 s |

这不是端到端延迟预测；未包含 TCP/TLS、代理、排队、上游处理和响应时间。

### 4.3 loopback forward

向 `127.0.0.1` HTTP sink 发送同一单图请求，5 次样本：

- 原 body：5.922 ms/op；
- 优化后 body：0.758 ms/op。

该结果只证明本地 socket 写入更小，不代表真实 OpenAI 上游 TTFT 或转发耗时。

## 5. 图片质量验证

### 5.1 离线代理结果

| 类型 | q85 | q90 | 门槛/解释 |
|---|---:|---:|---|
| 代码截图 Tesseract OCR 相似度 | 0.9799 | 0.9698 | 均 ≥ 0.95 |
| UI edge F1 | 1.0000 | 1.0000 | 均 ≥ 0.90 |
| 照片 PSNR | 29.03 dB | 29.31 dB | 均 ≥ 25 dB |
| 代码样本 WebP 大小 | 22,558 B | 25,078 B | q90 更大 |
| 照片样本 WebP 大小 | 73,460 B | 120,828 B | q90 更大、PSNR略高 |

q90 的 OCR 分数在该单一样本上略低于 q85，说明 quality 数字与 OCR 结果并非严格单调；两者都通过门槛，但不能据此宣称 q90 对所有代码图更好。

透明 PNG 使用 lossless WebP，测试确认 alpha 保留；代码/UI heuristic 测试确认使用 q90；所有策略均保持原宽高。

### 5.2 未完成项

没有调用真实视觉模型，因此以下只能判为**未验证**：

- 模型对代码截图的语义理解是否等价；
- UI 元素与坐标理解是否退化；
- 普通图片描述是否改变；
- `detail=high/original` 场景的细粒度损失。

在进入任何生产灰度前，需要固定模型、固定 prompt、盲测顺序和结构化评分的真实 A/B。

## 6. 缓存验证

已验证：

- 相同 decoded image bytes 命中同一 SHA-256 entry；
- 第二次请求复用 `.webp` 和 `.json`；
- 12 个并发相同请求只执行一次编码；
- metadata 校验 policy、optimizer、过期时间、大小和优化后 hash；
- 写入使用临时文件、`fsync` 和 rename；
- 目录 `0700`、文件 `0600`。

Phase 1.1 已自动验证 TTL、512 MiB 容量上限、最旧 pair 淘汰、损坏 pair 重建、
未知文件保护和进程重启命中。仍未完成：

- 多进程/多实例 stampede 与跨进程锁；
- 磁盘告警和生产数据保留审批/删除 SLA；
- 进程崩溃时 `.webp` / `.json` 两文件的完整事务一致性；
- 需要强租户隔离时的独立 cache namespace。

## 7. HTTP 与 WS/ctx_pool 验证

### 7.1 HTTP

真实 `Responses` handler 通过本地 capture upstream 验证：

- OpenAI bare Responses 中 PNG 被替换为 WebP data URL；
- `detail` 等字段保留；
- 关闭时优化器不被调用；
- 原 body 继续用于审计、重试保护和 session hash；
- 优化 body 在账号 failover 前生成一次并复用。

生产 HTTP canary 已连接真实 Codex 上游并确认改写 body 可正常生成结果；本轮没有触发
上游 413、bandwidth protection 或 failover，因此这些错误率的下降仍需目标 Key 的真实
大请求继续观察。

### 7.2 WebSocket / ctx_pool

代码已接入首轮 `response.create`，并在 ctx_pool、passthrough、HTTP bridge 与账号 failover 间复用首轮优化结果。安全审计与 session hash 使用原 frame。

后续 turn 未改写；当前 hook 只能校验，不能替换 payload。没有连接真实 WS 上游，所以本阶段不能声称 `read_upstream retry`、timeout 或 TTFT 已下降。

测试矩阵状态：

| 附件优化 | 模式 | 当前证据 |
|---|---|---|
| 关闭 | HTTP | handler baseline 与真实桌面关闭态 smoke 通过 |
| 关闭 | ctx_pool | 现有 handler/service 测试覆盖，未做真实上游测量 |
| 开启 | HTTP | 真实 Codex dry-run/rewrite、5 图、近 14 MB 重复图通过 |
| 开启 | ctx_pool | 自动化首轮矩阵通过；生产没有真实 WS 流量 |

## 8. Caddy 413 边界

2026-07-20 19:19 生产当前配置为：

```caddyfile
@responses_request_body path /v1/responses /v1/responses/* /responses /responses/*
request_body @responses_request_body {
    max_size 256MB
}
@non_responses_request_body not path /v1/responses /v1/responses/* /responses /responses/*
request_body @non_responses_request_body {
    max_size 16MB
}
```

Responses 在 `Content-Length > 256000000` 时提前返回 413；其他 API 仍在
`Content-Length > 16000000` 时提前返回 413。精确预检已经得到
256,000,001 B Responses → 413、16,000,001 B 非 Responses → 413。

台式机真实 HTTP 已验证 17,485,303 B 的 12 图请求穿过 Caddy，在 Sub2 压到
3,985,423 B 后到达模型并返回 200。应用层 `openai_responses_max_forward_body_size`
仍为 16,000,000 B，因此“入口放宽”不会重新把超大未压缩 payload 上传到 OpenAI。

因此应把错误来源区分为：

- Client → Caddy 413：Responses 仅在超过 256,000,000 B 时仍会发生；
- Sub2 应用 body limit：本模块到达得太晚；
- Sub2 → OpenAI/账号代理 413、bandwidth protection：本模块可能有效；
- failover 重放放大：本模块可减少每次重放的上游 bytes。

该 Caddy 调整是 Attachment Gateway 发布与 canary 完成后的独立变更，有单独备份、
配置校验、热重载和边界 smoke，不改变 HTTP/WS 路由。

## 9. 测试与静态检查

已通过：

```text
go test -count=1 ./...
go test -tags nodynamic -count=1 ./...
go test ./internal/service/attachment_gateway ./internal/config ./internal/handler
go vet ./internal/service/attachment_gateway ./internal/config ./internal/handler
go test -race ./internal/service/attachment_gateway \
  -run 'TestConcurrentRequestsSingleflightOneEncode|TestTimedOutBackgroundEncodeStillHoldsConcurrencySlot|TestCacheHitReusesContentAddressedEntry|TestDiskCacheHitSurvivesGatewayRestart|TestDecodedHashIgnoresBase64Whitespace|TestCorruptCacheEntryIsRejectedAndRebuilt' \
  -count=1
go mod verify
```

全量 `go test ./...` 包括 `internal/service`、`internal/handler`、`internal/service/openai_ws_v2`、routes、repository 和 migrations，全部通过。

覆盖包括：PNG/JPEG/WebP、threshold、缓存命中、透明 alpha、代码/UI 策略、remote URL、`file_id`、PDF/GIF 跳过、坏 base64 fail-open、最大图片数、12 并发 singleflight、默认关闭、日志隐私、HTTP 本地集成和大整数保持。

完整包 `go test -race ./internal/service/attachment_gateway` 的早期尝试曾因竞态构建下
WebP fixture 编码极慢而超时；没有 race 报告或断言失败。最终采用与风险匹配的缓存、
singleflight、超时及后台编码并发定向 race 门禁并通过。生产实际使用的纯 Go
`nodynamic` 路径已完成全仓非 race 测试，并在 linux/amd64 最终 Alpine 镜像内完成
启动、413 loopback、热缓存和超时 worker 并发测试。

## 10. 风险

1. method 0 冷处理仍为约 1.08–3.15 秒；透明 lossless 会增加 TTFT 和队头阻塞。
2. 累计分配量仍高；Base64/decode 与后台 encode 均已限流，但单次大图仍会同时持有
   decoded bytes、raster、WebP 与 JSON 片段。
3. 热缓存仍需完整解码和 hash，成本不低。
4. 缓存已有 TTL/容量清理，但多实例没有跨进程锁，且生产磁盘告警尚未接入。
5. 优化图与 hash 均属于敏感数据衍生物，需要数据保留策略。
6. `singleflight` 仅进程内，多副本会重复压缩。
7. WS 只覆盖首轮。
8. HTTP 固定答案质量已验证；真实 WS、目标用户 upstream 413/retry 与 TTFT 尚待流量。
9. 最终 linux/amd64 Alpine 功能已验证，但 Rosetta/QEMU 数据不能外推为生产 Xeon
   p95/p99。
10. Responses 入站已放宽到 256,000,000 B，但超大请求仍会增加 JSON/Base64 解码内存；
    不能把入口上限误当作所有 256 MB 请求都一定能压到 16 MB 以下。

## 11. Phase 1 初始判定（已由文首 Phase 1.1 更新取代）

- 技术方向：**有效，值得继续实验**；
- 当前代码：**可保留为默认关闭的实验能力**；
- 当时的生产候选：**否**；Phase 1.1 后调整为“可发布默认关闭代码、可做单内部 key
  条件灰度”，并已补齐 CGO0/Alpine 生产镜像门禁；
- R2/URL 化：**暂不需要**；
- ctx_pool：**继续推进，与附件优化互补**；
- 下一步：先降低内存/同步 CPU，补缓存清理与真实 A/B，再做非生产环境灰度。
