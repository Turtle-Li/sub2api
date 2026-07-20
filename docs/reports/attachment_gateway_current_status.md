# Sub2 Attachment Gateway 当前进度与问题报告

更新时间：2026-07-20 21:03 CST
范围：生产只读复核、admin 台式机 HTTP canary、最近 24 小时日志与本地报告整理。

> 2026-07-21 00:20 本地开发补充：独立 Attachment Gateway R2 配置、管理后台专用入口和
> 私有 presigned URL 外置 MVP 已完成自动回归；尚未 push、部署、填写真实凭证或改动
> 生产开关。下面的“生产状态”和历史结论仍描述当前线上版本，线上仍只发送压缩后的
> Base64，不使用 R2 URL。

## 1. 当前结论

Attachment Gateway 已证明能够显著降低可压缩图片请求的 Sub2→OpenAI 上行体积，
当前适合继续保持 **API Key 27 + admin user 1 的 scoped rewrite**，但暂不适合全量开启。

当前架构为“入口放宽、出口收紧”：

```text
Responses 原始请求：Caddy 最多 256,000,000 B
  -> 原始 body 安全/重复请求检查
  -> scoped 图片优化与 decoded-hash 缓存
  -> 模型映射
  -> 实际上游 body 最多 16,000,000 B
  -> OpenAI / failover
```

这已经解决“旧 16MB Caddy 门让优化器根本看不到大请求”的问题；但不代表任意
256MB 请求都一定能压到 16MB 以下。不可压缩、格式不支持、图片过多或压缩超时的请求，
仍会在 Sub2→OpenAI 之前返回 413，从而保护服务器上行。

目前没有发现 Attachment Gateway 引发的线上 5xx、panic 或 `read_upstream` 事件。
最大的未闭环项是：目标 API Key 27 在 rewrite 开启后尚未再次发送真实图片大包，因此
还不能声称该用户的原始故障已经由真实流量验证消失。

## 2. 生产状态

| 项目 | 当前值 |
|---|---|
| active upstream | `sub2api-blue:8080` |
| active image | `sub2api:auto-20260720-1857-08a77b31` |
| active health | `healthy` |
| Attachment feature commit | `888badf71608` |
| rollout mode | `rewrite` |
| scope | API Key 27、admin user 1 |
| `allow_unscoped` | `false` |
| Responses Caddy ingress | `256,000,000 B` |
| 非 Responses Caddy ingress | `16,000,000 B` |
| Responses 实际上游硬限制 | `16,000,000 B` |
| request budget | observe 开启；enforce 关闭 |
| 编码并发 / 请求预算 | 1 / 5 秒 |
| 缓存 | 1 天 TTL、512MiB 上限 |

代码默认仍是 `attachment_optimizer_enabled=false`；生产通过显式配置和白名单开启，
普通用户不会进入图片优化分支。Caddy 路径和 HTTP/WS 路由没有改变。

回滚材料：

- Caddy：`/opt/sub2api/Caddyfile.bak-attachment-20260720T111904Z`
- 应用配置：`/var/lib/docker/volumes/sub2api_sub2api_data/_data/config.yaml.bak-attachment-20260720T103648Z`
- 发布日志：`/var/log/sub2api-release/auto-20260720-184045-2433145`

## 3. 已完成实现

- 检测 Responses JSON 中 `input_image.image_url`、兼容 `image_url` 和
  `data:image/*;base64,...`。
- 当前只改写 PNG、JPEG、WebP；不处理 PDF、Office、音频、视频、远程 URL 或
  `file_id`。
- `<512KiB` 单图默认原样；普通图片 WebP q85；疑似代码/UI WebP q90；透明图片
  lossless；不缩放分辨率、不改变 `detail`。
- 8 张支持图片或 decoded 总量达到 4MiB 时进入聚合小图模式，候选阈值降到 128KiB。
- SHA-256 基于 decoded 原始图片 bytes，而不是 Base64 字符串；缓存采用原子写、
  metadata、TTL、容量清理与进程内 singleflight。
- 当前不会把附件改成公网文件 URL。输入 `data:image/png;base64,...` 优化后仍作为
  `data:image/webp;base64,...` 内嵌在 JSON 中发送给 OpenAI；本地 hash 缓存不对外暴露，
  没有使用 R2/S3、Caddy 静态 URL 或 `file_id`。
- 每请求最多优化 10 张图；单图、总 decoded bytes、像素数、Base64/decode 与编码并发
  均有护栏；失败和超时按单图/请求 fail-open。
- 只记录大小、计数、耗时和命中数，不记录 Base64、图片、prompt、hash 或缓存路径。
- HTTP 与 WS 首帧具备 hook；WS 后续 turn 保持原样。

## 4. 与重复请求保护的关系

两套能力不会互相覆盖，执行顺序是有意设计的：

1. 鉴权、安全审计、计费与异常重复请求保护使用 **原始 body**；
2. 只有通过前述检查的请求才进入 Attachment Gateway；
3. 优化后的 `forwardBody` 在账号 failover 循环之前只生成一次；
4. 每次 failover 复用同一份已优化 body，不会每次重新压缩；
5. 后续相同图片请求再通过 decoded-hash 命中本地缓存。

因此，短时间完全相同的异常请求可以先被原始请求保护拦截，避免浪费压缩 CPU；正常的
重复请求则可从图片 hash 缓存获益；进入 failover 后也不会恢复成原始大 payload。

## 5. 生产 canary 数据

### 5.1 入口、压缩、缓存与模型质量

| 场景 | 原 body → 上游 body | 降幅 | 优化耗时 | 结果 |
|---|---:|---:|---:|---|
| 12 张相同 WebP | 17,485,303 → 3,985,423 B | 77.2% | 2,705 ms | HTTP 200；模型 marker 正确；10 hits |
| PNG logo | 1,969,222 → 113,463 B | 94.2% | 331 ms | HTTP 200；语义识别正确 |
| JPEG logo | 1,513,435 → 114,659 B | 92.4% | 243 ms | HTTP 200；语义识别正确 |
| WebP logo | 1,457,431 → 107,443 B | 92.6% | 260 ms | HTTP 200；语义识别正确 |
| 8 张小 UI PNG | 3,058,674 → 272,922 B | 91.1% | 409 ms | HTTP 200；中文/UI OCR 正确；8 hits |
| 复杂透明 PNG | 2,158,409 → 原样 | 0% | 5,004 ms | 超时 fail-open；图片未损坏 |

12 图请求只优化前 10 张，另外 2 张由 CPU 护栏跳过，但最终仍低于 4MB。模型质量验证
不是只比较像素：压缩后的 PNG/JPEG/WebP 均正确识别乌龟、电路节点、对话气泡和颜色；
聚合小 UI 图仍准确读取品牌、邮箱、中文验证提示与“登录”。

Caddy 边界验证：

- Responses `256,000,001 B` → 413；
- 非 Responses `16,000,001 B` → 413；
- Responses `17,485,303 B` → 进入 Sub2、压缩、模型 HTTP 200。

### 5.2 最近 24 小时观测

| 指标 | 结果 |
|---|---:|
| Attachment 事件 | 30 |
| admin user 1 | 20 |
| API Key 27 | 10 |
| admin rewritten | 16 |
| admin 累计 cache hits | 78 |
| admin 超过旧 16MB 的 canary | 5 |
| Attachment timeout | 1（透明 PNG，安全 fail-open） |
| Attachment errors / panic | 0 / 0 |
| `read_upstream` | 0 |

API Key 27 的 10 个事件都没有图片，最大 body 约 110,909 B，未触发改写、超时或错误。
所以当前只有 admin canary 证明了大图链路，目标用户的下一笔真实大请求仍待观察。

当前缓存约 2.75MB，共 7 对 WebP/metadata，远低于 512MiB 上限。一次只读运行快照为
CPU 23.55%、RSS 182.4MiB；该快照包含正常线上流量，不能单独归因于附件优化，也不能
替代 p95/p99 资源监控。

最近 24 小时共有 11 条 `openai.forward_failed`，均已归因：7 条是早期测试缺少官方
客户端指纹，3 条是早期 canary 使用了上游不接受的参数，1 条是无关用户的上游 503；
修正后的端到端图片 canary 全部为 200，未发现压缩导致的 forward failure。

### 5.3 服务器真实公网下载测试

只测试公网→服务器 RX，响应内容直接丢弃，不写磁盘，也没有测试服务器 5Mbps TX：

| 来源/模式 | 数据量 | 结果 |
|---|---:|---:|
| Cloudflare 单连接 1 | 50MB | 9.3Mbps |
| Cloudflare 单连接 2 | 50MB | 9.3Mbps |
| Cloudflare 单连接 3 | 50MB | 9.3Mbps |
| Vultr 东京单连接 | 50MB | 9.0Mbps |
| Cloudflare 4 连接聚合 | 40MB | 8.6Mbps |

两家来源与多连接聚合都落在 8.6–9.3Mbps，说明瓶颈更像整机约 10Mbps 公网下行档，
不是单一 CDN 或单 TCP 连接限速。真实用户→Sub2 路径还会受用户上行和跨境路由影响，
通常只会更慢。

按约 9Mbps 估算，200,000,000 B 单请求仅传入就约需 178 秒；256,000,000 B 约需
228 秒。因此建议把 Responses 公网绝对入口从当前 256MB 收紧到十进制 200MB；这仍是
非常宽松的灾备上限，不应被理解为“Sub2 承诺成功处理 200MB 附件”。本次测速没有修改
生产限制。

## 6. 当前问题与风险

### P1：目标用户尚无真实大包闭环

API Key 27 已在白名单，但开启 rewrite 后没有再次出现图片大请求。当前不能用 admin
测试替代该用户的真实结果。需要在下一次大请求出现时确认：原/优化 body、cache hit、
上游状态、retry、TTFT 和最终 413 是否消失。

### P1：当前 256MB 入站会放大超时、内存与滥用风险

实测服务器 RX 只有约 9Mbps，而且解析成本也不为零。Responses handler 仍需读取
JSON/Base64，极端并发
大包可能同时占用 body、decoded raster、WebP 和重建后的 JSON。256MB 是绝对上限，
不是无限；16MB 上游保险只保护 TX，不能消除入口内存压力。扩大普通用户 scope 前，
建议先把公网入口收紧到 200MB，并增加大 body 并发/速率护栏和 RSS 告警。

### P1：真实客户端 WS 尚未完成生产 A/B

HTTP 已真实验证；现有 Codex 台式机没有可用的 Responses WebSocket feature，生产也没有
可归因的端到端 WS canary。自动化 passthrough/ctx_pool × dry-run/rewrite 矩阵通过，
但不能据此宣称真实 WS 已稳定验证。当前 hook 也只覆盖 WS 首帧。

### P2：复杂透明图可能耗尽 5 秒预算

透明 PNG lossless 有一次 5 秒超时并原样转发。fail-open 能保证内容不损坏，但如果原 body
超过 16MB，最终仍会被上游 body 硬限制拒绝。建议在扩量前增加透明复杂度/熵快速判断，
对明显低收益图片直接跳过或采用更快策略。

在当前支持的 PNG/JPEG/WebP 中，这是唯一已经复现的编码超时问题。普通不透明 PNG、
JPEG 和 WebP 的内容质量与压缩链路均已通过；高度压缩或噪声图片可能因节省不足 5%
而安全保留原图，这属于预期策略，不是内容损坏。

### P2：很多小文件仍不是“全部可压缩”

聚合策略已经覆盖“8 张以上、单张 128–512KiB”的 PNG/JPEG/WebP，并在 8 张 UI 图上
把 body 降低 91.1%。但以下情况仍可能压后超过 16MB：

- 大量低于 128KiB 的图片；
- 超过每请求 10 张优化上限的不同图片；
- 已高度压缩、随机噪声或透明 lossless 低收益图片；
- PDF、Office、音频、视频、remote URL、`file_id` 等未处理附件；
- 很长的非附件上下文本身已接近 16MB。

全局 16MB forward cap 会在 failover 前阻止这些请求继续占用上行，但用户仍会收到 413。

### P2：请求级预算尚未 enforce

`request_budget_enabled=true` 目前只观察，`request_budget_enforce=false`。候选附件上限
（32 个、12MiB 内联数据、14MiB candidate body）还不会主动拒绝；真正生效的是全局
16MB forward cap。应先积累目标用户的 `budget_would_reject` 数据，再决定是否只对当前
scope 开 enforcement，避免误伤有效请求。

### P3：缓存与多实例边界

磁盘缓存可跨进程复用，但 singleflight 只在单进程内生效；蓝绿切换或多个活跃实例可能
同时压缩同一 cold hash。当前流量和缓存规模很小，不是即时故障，但全量前应评估跨进程
锁或对象存储。缓存是图片内容衍生数据，仍需持续执行 TTL、容量、权限与磁盘告警。

## 7. 是否容易再达到 16MB

对当前故障类型——重复、可压缩的 Base64 PNG/JPEG/WebP——压缩后再次超过 16MB 的概率
已经显著下降：实测 17.49MB 可降到 3.99MB，单图和聚合小图通常下降 91%–94%。

但不能把它表述成“很难达到 16MB”这一绝对保证。已压缩媒体、未支持附件、透明复杂图、
大量不同图片、超过 10 张优化上限或超长文本仍可能达到。当前 16MB forward cap 是最后
保险，不是压缩成功率指标。

## 8. 建议与下一步

当前建议：**保持生产现状继续观察，不全量、不立即接生产 R2、不打开 request-budget enforce。**

1. 等 API Key 27 下一笔真实图片大请求，记录压缩率、cache hit、状态、retry 与 TTFT。
2. 有可控客户端时补 HTTP / ctx_pool 的真实 A/B；此前不宣称 WS 生产验证完成。
3. 为复杂透明图增加快速跳过/低收益判定，并考虑缓存“压不小”的负结果。
4. 将 Responses 公网绝对入口从 256MB 收紧到 200MB，并在扩大 scope 前增加大 body
   并发、速率和 RSS 告警。
5. 累积 `budget_would_reject` 样本后，再评估仅对当前 scope 开启请求级 enforce。
6. R2/URL 化进入下一阶段实验候选，优先覆盖复杂透明图、压不动的图片和未来客户端直传；
   先做私有 bucket + 短时签名 URL canary，不直接接生产。
7. ctx_pool 继续独立推进；它解决上下文/连接复用，不能替代附件压缩。

### 8.1 21:03 补充：HTTP 入站、透明 PNG、R2 与视频

- Caddy 网络层可边收边代理，但 Responses handler 会先完整读取 JSON；服务端图片压缩
  不能缩短用户第一次上传到 Sub2 的时间，`stream=true` 只影响 SSE 响应。
- 本地透明图策略比较证明：`Exact=false` 对“完全透明像素含隐藏 RGB”的样本可在 alpha
  与黑/白背景合成误差为 0 时，把 8.41MB 降到 2.17MB，并把编码从约 491ms 降到
  197ms。本地候选已修改 encoder ID，普通与 `nodynamic` 全量测试均通过，尚未发布生产。
- 高熵半透明样本 lossless 基本无收益；q90/q95 虽下降约 57%-60%，可见 RGB 误差过高，
  不适合作为默认规则。该类应原样 + R2 URL，而不是强制有损。
- Responses 支持图片 URL 和文件 URL，但没有原生 `input_video`；大视频应直传 R2 后异步
  抽帧和 ASR，再发送帧与字幕。R2 只存原视频并不能让 Codex 原生观看。
- 详细数据与方案见 `docs/reports/attachment_gateway_transport_r2_video.md`。

## 9. 关联报告

- `docs/reports/attachment_gateway_phase1_report.md`
- `docs/reports/attachment_gateway_request_budget.md`
- `docs/reports/attachment_gateway_next_step.md`
- `docs/reports/attachment_optimizer_test.md`
- `docs/reports/data/attachment_gateway_production_canary_20260720.json`
- `docs/reports/attachment_gateway_transport_r2_video.md`
- `docs/reports/data/attachment_gateway_transparent_benchmark_20260720.json`
- `docs/reports/attachment_gateway_r2_url_mvp.md`
