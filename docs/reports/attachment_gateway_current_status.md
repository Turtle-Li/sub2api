# Sub2 Attachment Gateway 当前进度与问题报告

更新时间：2026-07-21 05:46 CST
范围：生产 scoped HTTP/WS canary、真实 R2、图片质量、缓存、错误与资源观测。

> 2026-07-21 更新：R2 URL MVP 已发布到生产版本 `7affb6a03`，真实 R2
> Put→presigned GET→Delete 探针通过，运行态仅对 API Key 27 与 admin user 1 的
> `rewrite` 请求生效。HTTP 与当前真实 WS `http_bridge` canary 均通过；普通用户没有进入
> Attachment Gateway。`ctx_pool` 对比已完成并复现 stale pooled connection 的 1011 /
> `read_upstream`，账号 2 已恢复 `http_bridge`；恢复后同连接文本与图片 cold/hot 再次通过。
>
> 05:46 本地候选更新（尚未发布）：已修复 `ctx_pool` 安全重试再次命中第二条 stale
> connection 的问题，并落实 `store=false + strict` 新会话强制新建连接、失败连接粘连绑定
> compare-and-delete、失效连接禁止重新绑定。多 stale、strict、fail-close、pool/state-store
> 定向回归、完整 backend 测试、完整 service race 与 `go vet ./...` 均通过。生产账号 2
> 仍保持 `http_bridge`，等待蓝绿候选和隔离 canary 后再决定是否切默认。

## 1. 当前结论

Attachment Gateway 已证明能够显著降低可压缩图片请求的 Sub2→OpenAI 上行体积，
当前适合继续保持 **API Key 27 + admin user 1 的 scoped rewrite**，但暂不适合全量开启。

对达到 URL 门槛的图片，当前会把压缩结果或安全保留的原图写入私有 R2，并把上游 JSON
中的 data URL 换成短时签名 HTTPS URL。5 图 HTTP 已从 13,929,029 B 降到 3,090 B；
WS `http_bridge` 首帧已从 1,261,586 B 降到 861 B，OpenAI 成功拉取并保持识别结果一致。

WS 生产默认暂时继续使用 `http_bridge`。`ctx_pool` 的已知 stale retry 根因已有本地修复，
但生产默认切换仍需先通过隔离 canary；在发布与真实多轮验证完成前，不能把本地回归等同于
生产稳定性结论。

当前架构为“入口放宽、出口收紧”：

```text
Responses 原始请求：Caddy 最多 256,000,000 B
  -> 原始 body 安全/重复请求检查
  -> scoped 图片优化与 decoded-hash 缓存
  -> 模型映射
  -> HTTP 与 WS 首帧实际转发最多 16,000,000 B
  -> OpenAI / failover
```

这已经解决“旧 16MB Caddy 门让优化器根本看不到大请求”的问题；但不代表任意
256MB 请求都一定能压到 16MB 以下。不可压缩、格式不支持、图片过多或压缩超时的请求，
仍会在 Sub2→OpenAI 之前返回 413，从而保护服务器上行。需要注意：`http_bridge` 在第二
turn 以后为 `store=false` 会话重建的 HTTP body 目前不经过首帧 Attachment hook 与该
forward cap；本轮已观测到 18.90MB 的 bridge replay 成功发往上游，这是待修边界。

目前没有发现 Attachment Gateway 压缩/R2 本身引发的线上 5xx 或 panic；`ctx_pool` 的两次
`read_upstream` 已由 stale pooled connection 归因，图片 URL 随新连接成功，故不归因于
压缩内容或 R2。
最大的未闭环项是：目标 API Key 27 在 rewrite 开启后尚未再次发送真实图片大包，因此
还不能声称该用户的原始故障已经由真实流量验证消失。

## 2. 生产状态

| 项目 | 当前值 |
|---|---|
| active upstream | `sub2api-blue:8080` |
| active image | `sub2api:auto-20260721-0206-7affb6a0` |
| active health | `healthy` |
| Attachment/R2 feature commit | `7affb6a03` |
| rollout mode | `rewrite` |
| scope | API Key 27、admin user 1 |
| `allow_unscoped` | `false` |
| R2 URL rewrite | 已启用，仅继承上述 scope |
| 账号 2 WS 模式 | `http_bridge`（`ctx_pool` 对比后已恢复） |
| Responses Caddy ingress | `256,000,000 B` |
| 非 Responses Caddy ingress | `16,000,000 B` |
| Responses 实际上游硬限制 | HTTP 与 WS 首帧 `16,000,000 B`；`http_bridge` 后续 replay 尚有缺口 |
| request budget | observe 开启；enforce 关闭 |
| 编码并发 / 请求预算 | 1 / 5 秒 |
| 缓存 | 1 天 TTL、512MiB 上限 |
| 负结果缓存候选 | 本地已实现；24h TTL、10,000 条上限；尚未发布 |

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
- 本地生产候选已增加“压缩不足 5%”负结果缓存：命中时保留原图进入后续 R2 URL 化，
  跳过 raster decode/WebP encode；正/负命中分别记录。负条目绑定 encoder/policy 指纹，
  默认 24h TTL、10,000 条上限，并计入共享 512MiB 磁盘预算。该改动尚未发布到生产
  `7affb6a03`。
- 达到 URL 门槛且 R2 就绪时，会把优化结果或安全保留的原图写入私有 R2，并将
  `image_url` 改为短时 presigned HTTPS GET URL；小图或 R2 失败仍安全保留 data URL。
  对象、URL、hash、凭证和图片内容均不写日志；没有使用 `file_id`。
- 每请求最多优化 10 张图；单图、总 decoded bytes、像素数、Base64/decode 与编码并发
  均有护栏；失败和超时按单图/请求 fail-open。
- 只记录大小、计数、耗时和命中数，不记录 Base64、图片、prompt、hash 或缓存路径。
- 本地候选已把 Attachment hook 扩展到 WS 后续 turn：`http_bridge`、`ctx_pool` 与
  passthrough 的后续 `response.create` 都执行同一压缩/hash/R2 流程；bridge 最终 replay
  body 另记录隐私安全的大小与附件计数。该部分尚未发布。

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
| Attachment / `http_bridge` `read_upstream` | 0 |
| `ctx_pool` admin canary `read_upstream` | 2（stale connection，已恢复 bridge） |

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

### 5.4 R2 URL 与 WS `http_bridge` canary

真实 R2 探针完成 Put→presigned GET→Delete。随后 HTTP canary 得到：

- JPEG 5,020,854 → 789 B；首次上传 1 次，同图后两次均
  `url_upload_count=0`、`url_cache_hits=1`；
- WebP 2,064,342 → 789 B；复杂透明 PNG 5,911,495 → 779 B；
- 5 图 13,929,029 → 3,090 B，热请求 5/5 URL cache hit；
- 长上下文+图 5,086,850 → 66,785 B；
- 所有模型答案与原图 baseline 一致，`url_errors=0`、`errors=0`、
  `url_timed_out=false`。

当前真实 WS 调度模式为 `http_bridge`。已有图片连接与多轮复用连接全部完成：

| 场景 | 首帧 → 上游 | 首文字 | 完成 | 缓存/结果 |
|---|---:|---:|---:|---|
| 已有 R2 对象 1 | 1,260,210 → 861 B | 4.446s | 5.380s | URL hit；识别正确 |
| 已有 R2 对象 2 | 1,260,210 → 861 B | 4.939s | 5.839s | URL hit；识别正确 |
| 新对象冷上传 | 1,261,586 → 861 B | 8.038s | 9.016s | upload=1；识别正确 |
| 同对象热请求 | 1,261,586 → 861 B | 4.797s | 5.612s | upload=0、URL hit=1；识别正确 |
| 3.88MB JPEG cold | 5,173,889 → 916 B | 15.905s | 17.570s | encode + upload；已知答案正确 |
| 同 JPEG hot | 5,173,889 → 916 B | 9.818s | 11.253s | 正缓存 + URL hit；已知答案正确 |

恢复后再次执行的同一连接 5 turn 也全部 `response.completed`，首事件 0.665–0.886 秒，
完成 1.341–1.793 秒并以 1000 正常关闭。3.88MB JPEG 图片原始字节压到 1.14MB，随后
R2 URL 化使上游 JSON 降为 916 B；第二次 `cache_hits=1`、`url_cache_hits=1`、不再上传，
图片编号和红圆/蓝方块数量均识别正确。恢复后的 8 条 bridge 连接、38 个完成 turn 中，
没有 `read_upstream`、`write_upstream`、retry、keepalive timeout、1011 或 panic。
完整结构化数据见
`docs/reports/data/attachment_gateway_r2_canary_20260721.json`。

### 5.5 `ctx_pool` 对比与恢复验证

账号 2 临时切到 `ctx_pool` 后，合法 `stream=true` 请求得到以下结果。该账号仍绑定多个
真实分组，并非理想隔离 canary，因此复现 stale failure 后没有继续加压，随后立即恢复：

- 同一连接连续 5 turn 全部完成，首文字约 1.23–2.09 秒；
- 新建 R2 图片对象的 cold/hot 请求均成功，模型识别与原图一致；
- 但两次“已有 R2 对象”图片请求分别复用了两条已失效 pooled connection，均收到
  1011 `keepalive ping timeout`；服务端各出现一次
  `ingress_ws_turn_retry reason=read_upstream`，retry 后仍关闭，未可靠切到新连接；
- 两条 stale-connection 失败都来自 admin 测试 Key 7。同窗口没有普通用户的
  `read_upstream` / keepalive failure；另有 API Key 26 的既有 tool-output 400，在切换前
  已持续出现，属于 `http_bridge` 会话重放兼容问题，不归因于本次 `ctx_pool` 测试。

这说明 R2 签名 URL 与图片内容没有损坏，失败点在 `ctx_pool` 的 stale connection 健康
判断/重试换连。账号 2 已于 04:20 恢复 `http_bridge`。恢复后从授权桌面发起的同一原生
WS 连接连续 2 turn 均 `response.completed`，marker 正确，首事件 0.63–0.72 秒、完整
turn 1.37–1.60 秒、1000 正常关闭；新日志中 `read_upstream=0`、`write_upstream=0`、
keepalive timeout=0。05:08 截止的累计窗口已有 8 条 bridge 连接、38 个完成 turn，仍是
`ctx_pool` confirm=0、retry/read/write/keepalive=0。窗口内 3 条 proxy 400 中，两条是
本 canary 修正前的非法测试帧，另一条是 API Key 26 既有 tool-output 重放问题。

同一恢复窗口还暴露了 `http_bridge` 的独立代价：一条首帧仅约 42.9KB 的 `store=false`
长会话，从第二 turn 起因上下文 replay 迅速升至 16.33MB，最大 18.90MB；27 个 ≥16MB
turn 都完成，但首 token 多为 32–43 秒。这里的 `payload_bytes` 是 Sub2 重建后发给 OpenAI
的 HTTP body，不是客户端重新上传 18.90MB。当前 16MB forward cap 只覆盖 HTTP handler
和 WS 首帧，没有覆盖 bridge 后续 replay，不能再把它描述成所有路径的全局硬保险。

本地 `ctx_pool` 修复随后完成：`read_upstream` / `write_upstream` 在尚未向客户端输出时只
重试一次，并显式 `ForceNewConn`，不再从空闲池挑第二条连接；`store=false + strict` 且无
有效 sticky connection 时同样强制新建。需要 `previous_response_id` 的严格 tool
continuation 继续 fail-close，不会为了可用性把 tool output 发到错误会话。失败连接对应的
response/session 绑定采用 compare-and-delete，避免旧失败删除并发建立的新绑定；已经从池
中驱逐的连接也不会重新写入 sticky state。新增回归在池中预置第二条 stale connection，
确认 retry 新拨第三条 fresh connection 且第二条 stale 的写次数为 0。

### 5.6 负结果缓存本地验证

本地候选使用 decoded source bytes 的 SHA-256，并以
`<hash>.negative.json` 原子保存 `not_smaller` 结果；metadata 包含原始/候选大小、MIME、
宽高、质量、lossless、policy、optimizer、创建与过期时间，不含图片、Base64、URL 或
prompt。命中时仍保留原 data URL 交给后续 R2 URL 化，但不再 raster decode/encode。

- 真实 libwebp 579,454 B WebP：cold 463.789ms → warm 16.304ms，优化阶段耗时下降约
  96.5%；warm 为 `negative_cache_hits=1`、正 `cache_hits=0`；
- 相同 decoded bytes 即使 Base64 换行不同仍命中；
- 同进程热命中、进程重启命中、12 并发 cold singleflight、TTL 到期、损坏 metadata、
  policy 变化、负条目上限清理和未知文件保护均通过；
- 定向 `go test -race`、附件/config/handler 测试、`go test -tags nodynamic ./...` 全仓、
  `go vet -tags nodynamic ./...`、`git diff --check` 与结构化 JSON 校验均通过。

该结果只证明本地候选正确；生产仍是 `7affb6a03`，尚未产生真实
`negative_cache_hits`。

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

### P1：`ctx_pool` stale connection 修复等待生产隔离 canary

`ctx_pool` 已完成同条件生产对比，历史上两条失效复用连接均在图片首轮触发 1011。本地
已闭环 retry 强制新建、多 stale 回归和 stale binding 失效，但尚未发布并从授权桌面复测；
因此生产继续保持 `http_bridge`。本地 Attachment hook 已覆盖后续 turn，生产版本仍只有
首帧能力。

### P1：`http_bridge` 长会话会重复发送大上下文且绕过首帧 forward cap

生产已观测到首帧约 42.9KB 的 bridge 会话在第二 turn 重建成 16.33MB，随后增长到
18.90MB；27 个 ≥16MB turn 虽均成功，但首 token 多为 32–43 秒，并持续占用服务器约
5Mbps 上行。代码路径确认 `prepareResponsesAttachments` 与 `openAIResponsesMaxForwardBodyBytes`
只在 WS 首帧执行，后续 `buildOpenAIWSReplayInputSequence` 生成的 body 直接进入 HTTP upstream。

不能简单补 16MB 拒绝，否则会把当前“慢但成功”的长会话变成中途断线。短期继续保持
bridge 稳定性；下一步应先增加后续 turn observe-only 预算指标，再并行修复 `ctx_pool`
stale checkout/retry。只有 ctx_pool 稳定或 bridge 能安全做状态化/压缩后，才能真正消除
长上下文的重复上行。

### P2：负结果缓存已实现，待发布 canary

生产 `7affb6a03` 上，同一张“重编码不足 5% 节省”的 WebP 第二次请求虽命中 R2 URL
cache，仍又花 2.274 秒编码。本地候选现已实现有 TTL、容量与 policy/encoder 版本约束的
负结果缓存；真实 libwebp 的 579,454 B WebP 测试从 cold 463.8ms 降到 warm 16.3ms，
减少约 96.5% 的优化阶段耗时。还需要发布到既有精确 scope 后，用原生产样本确认
`negative_cache_hits=1`、`cache_hits=0` 且 R2 URL cache 继续生效。

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

HTTP 与 WS 首帧的 16MB forward cap 会在 failover 前阻止这些请求继续占用上行，但用户
仍会收到 413；`http_bridge` 后续 replay 暂不受该 cap 覆盖。

### P2：请求级预算尚未 enforce

`request_budget_enabled=true` 目前只观察，`request_budget_enforce=false`。候选附件上限
（32 个、12MiB 内联数据、14MiB candidate body）还不会主动拒绝；HTTP 与 WS 首帧另有
16MB forward cap，bridge 后续 replay 当前没有同等保护。应先积累目标用户的
`budget_would_reject` 数据，再决定是否只对当前 scope 开 enforcement，避免误伤有效请求。

### P3：缓存与多实例边界

磁盘缓存可跨进程复用，但 singleflight 只在单进程内生效；蓝绿切换或多个活跃实例可能
同时压缩同一 cold hash。当前流量和缓存规模很小，不是即时故障，但全量前应评估跨进程
锁或对象存储。缓存是图片内容衍生数据，仍需持续执行 TTL、容量、权限与磁盘告警。

## 7. 是否容易再达到 16MB

对当前故障类型——重复、可压缩的 Base64 PNG/JPEG/WebP——压缩后再次超过 16MB 的概率
已经显著下降：实测 17.49MB 可降到 3.99MB，单图和聚合小图通常下降 91%–94%。

但不能把它表述成“很难达到 16MB”这一绝对保证。已压缩媒体、未支持附件、透明复杂图、
大量不同图片、超过 10 张优化上限或超长文本仍可能达到。HTTP 与 WS 首帧的 16MB
forward cap 是最后保险，不是压缩成功率指标；bridge 后续 replay 缺口需单独修复。

## 8. 建议与下一步

当前建议：**保持当前 scoped R2 canary，不全量、不打开 request-budget enforce。**

1. 等 API Key 27 下一笔真实图片大请求，记录压缩率、cache hit、状态、retry 与 TTFT。
2. 生产继续使用 `http_bridge`；把已通过本地回归的 `ctx_pool` stale retry 修复做成蓝绿
   候选，先用隔离账号从授权桌面重测，再决定是否切统一默认。
3. 负结果缓存完成全量回归后发布到当前精确 scope，用原 1.26MB WebP 做 cold/warm
   canary；复杂透明图继续增加快速跳过/低收益判定。
4. 将 Responses 公网绝对入口从 256MB 收紧到 200MB，并在扩大 scope 前增加大 body
   并发、速率和 RSS 告警。
5. 累积 `budget_would_reject` 样本后，再评估仅对当前 scope 开启请求级 enforce。
6. R2 已验证私有 bucket + 短时签名 URL；继续保持当前 scope，并检查生命周期与长期
   HeadObject 命中，不扩大普通用户范围。
7. `ctx_pool` 仍是独立优化方向，但本轮 A/B 已给出 No-Go；在 stale connection 修复并
   通过隔离回归前，不作为生产默认。它也不能替代附件优化。

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
