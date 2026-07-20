# Sub2 HTTP 入站、透明 PNG、R2 与视频边界补充验证

日期：2026-07-20  
范围：本地代码核验、透明 PNG 编码基准、官方文档核验；未连接生产、未修改生产配置。

## 1. 结论

1. Caddy 可以一边接收一边把请求流交给 Sub2，但 Responses handler 会先把完整 JSON
   body 读入内存，之后才解析 Base64、压缩图片并创建新的上游 body。因此当前不是
   “边上传边压缩”；`stream=true` 只控制模型响应 SSE。
2. 当前优化只能显著减少 **Sub2 -> OpenAI** 的上行、failover 重放和上游 413，不能减少
   用户第一次把原始 Base64 body 上传到 Sub2 的时间。
3. 普通 PNG/UI/截图的压缩收益已经很高；生产 canary 的 17.49MB/12 图请求降到 3.99MB。
   已压缩 JPEG/WebP、随机噪声和复杂半透明 PNG 不能保证有同等收益。
4. 复杂透明 PNG 需要分类处理：完全透明像素中的隐藏 RGB 可以安全丢弃；高熵、部分透明
   像素很多的图片不适合默认做有损强压，原图 R2 URL 化更稳妥。
5. Responses API 官方支持 `input_image.image_url` 和 `input_file.file_url`，所以 R2 短时
   签名 GET URL 在 API 形态上可行，但仍需要真实 OpenAI fetch canary。
6. Codex/Responses 当前没有原生 `input_video`。视频不能当普通 `input_file` 直接交给模型
   理解；大视频应直传 R2，再异步抽帧和转写，最终发送图片帧与字幕/文本。

## 2. HTTP 请求实际时序

代码证据：

- `deploy/Caddyfile` 的 `request_body` 只限制大小，不要求先完整缓冲；
- `backend/internal/handler/openai_gateway_handler.go` 先调用
  `readLenientJSONRequestBodyWithPrealloc`；
- `backend/internal/pkg/httputil/body.go` 使用 `io.Copy` 把 `req.Body` 完整读入 `bytes.Buffer`；
- body 完整读取、校验、重试保护、审计和计费检查之后，才调用 Attachment Gateway；
- 优化后的 `forwardBody` 在 account/failover 循环之前只生成一次并重复复用。

```text
用户 -> Caddy（网络层可流式转发）
     -> Sub2 完整读取 JSON body
     -> Base64 decode / hash / WebP / cache
     -> 重建完整 JSON
     -> OpenAI
     -> SSE 流式返回（仅当 stream=true）
```

因此端到端首输出时间至少包含：完整入站上传 + 优化 + OpenAI 上传 + 模型首输出。

## 3. 请求体与图片压缩效率

### 3.1 已验证图片收益

| 场景 | 原始 -> 优化后 | 结论 |
|---|---:|---|
| 12 张真实 HTTP 图片 | 17.49MB -> 3.99MB | 77.2% 下降，HTTP 200，10 个缓存命中 |
| 单张 PNG | 1.969MB -> 113KB | 约 94.3% 下降 |
| 8 张小 UI PNG | 3.059MB -> 273KB | 91.1% 下降，OCR 正确 |
| 已被客户端压过的 JPEG | 531KB -> 原样 | 再编码不足 5% 收益，安全不改写 |

对截图、UI、可压缩 PNG，这个方向收益很高；对已压缩照片、噪声、动画、未支持文件，
不能按 90% 下降估算。

### 3.2 整体 HTTP Content-Encoding 的上限

Sub2 已能解 gzip/zstd/deflate。如果将来在 CCSwitch/本地代理给整个 JSON 加
`Content-Encoding`，可以基本消除 Base64 的约 33% 膨胀，但不能重新压缩已经压缩过的
PNG/JPEG/WebP 内容：

| 样本 | 二进制 | Base64 | Base64+gzip | Base64+zstd |
|---|---:|---:|---:|---:|
| PNG 1 | 1,013,568B | 1,351,424B | 1,023,318B | 1,013,670B |
| PNG 2 | 761,902B | 1,015,872B | 756,888B | 750,081B |
| JPEG | 25,231B | 33,644B | 22,253B | 22,339B |

这通常只带来约 25% 的 WAN body 下降，明显小于客户端先压图片或直接 URL 化的收益。
当前 Codex 客户端没有因此被改动。

## 4. 复杂透明 PNG

结构化数据见 `docs/reports/data/attachment_gateway_transparent_benchmark_20260720.json`。
以下为 Apple M1 Pro 的单次冷编码比较，只用于策略选择，不能当生产 Xeon 延迟：

| 样本/策略 | PNG -> WebP | 降幅 | 编码 | Alpha MAE | 黑/白背景 RGB RMSE |
|---|---:|---:|---:|---:|---:|
| 隐藏 RGB / lossless Exact=true | 8.41MB -> 6.67MB | 20.75% | 491ms | 0 | 0 / 0 |
| 隐藏 RGB / lossless Exact=false | 8.41MB -> 2.17MB | 74.26% | 197ms | 0 | 0 / 0 |
| 高熵半透明 / lossless Exact=true | 9.40MB -> 9.53MB | -1.39% | 425ms | 0 | 0.51 / 0.51 |
| 高熵半透明 / lossless Exact=false | 9.40MB -> 9.50MB | -1.01% | 428ms | 0 | 0.51 / 0.51 |
| 高熵半透明 / q90 | 9.40MB -> 3.80MB | 59.58% | 622ms | 0 | 44.94 / 44.94 |
| 高熵半透明 / q95 | 9.40MB -> 4.06MB | 56.82% | 651ms | 0 | 44.89 / 44.89 |

Alpha 在所有 WebP 策略下都保持一致；问题是高熵 RGB 的有损误差。q95 在这个极端样本上
几乎没有改善可见误差，所以不能把“透明图统一 q95”作为安全生产策略。

本地候选已将 libwebp `Exact` 改为 `false`，只允许编码器忽略完全透明像素中不可见的
RGB；同时修改 encoder ID，使旧缓存不会被误当成同一策略。新增测试在黑、白背景合成后
验证可见结果。`go test -count=1 ./...` 与 `go test -tags nodynamic -count=1 ./...`
全量通过，包含 handler、service、Attachment Gateway 和 WS v2；该候选尚未发布生产。

剩余高熵半透明图建议：

1. 增加透明度分布/采样熵快速判断，避免在请求路径反复做低收益 lossless；
2. 缓存“不值得压缩”的负结果，避免同一 hash 每次重算；
3. 默认保持原图；超过内联上限时用 R2 原图 URL，而不是强制 q90/q95。

## 5. R2 方案

### 5.1 API 兼容性

OpenAI 官方文档确认：

- `input_image.image_url` 可用公网 HTTPS URL、Base64 data URL 或 Files API `file_id`；
- Responses 的 `input_file` 可用 Base64、`file_id` 或外部 `file_url`；
- 图片支持 PNG、JPEG、WebP 和非动画 GIF。

因此私有 R2 对象 + 短时签名 GET URL 可以作为图片 URL。建议签名有效期 2-6 小时，
覆盖排队、OpenAI 抓取和 failover；敏感场景可更短，但不能短到模型尚未抓取就过期。

### 5.2 两阶段收益

```text
阶段 A（服务端 URL 化）
Codex Base64 -> Sub2 完整接收 -> hash/压缩或保留原图 -> R2 -> 签名 URL -> OpenAI

阶段 B（客户端直传）
Codex/CCSwitch -> 申请签名 PUT -> 直接上传 R2 -> 只把 URL/asset_id 发给 Sub2
```

- 阶段 A 可把 Sub2 -> OpenAI 上行、retry 和请求体降到很小，但不能减少第一次入站。
- 阶段 B 才能绕过本机约 10Mbps 的 Sub2 RX，并让重复 hash 不再重复上传；它需要
  CCSwitch/Codex 侧代理能力，不能只改服务器 handler。

建议使用私有 Standard bucket、SHA-256 对象键、生命周期删除、短时签名 URL，并确保日志
不记录签名 query。先做图片 canary，不在第一版处理视频。

### 5.3 免费额度与限制

Cloudflare R2 当前 Standard 免费层为每月 10GB-month、100 万 Class A、1000 万 Class B，
公网 egress 免费。对 7 天 TTL、按 hash 去重的图片缓存大概率足够；视频是否免费取决于
总时长和保留时间，不能直接假定够用。

单对象单次上传最高约 5GiB，多段上传最高约 4.995TiB。预签名 URL 支持 GET/HEAD/PUT/
DELETE，有效期 1 秒到 7 天；不支持 HTML form POST。生产不应使用有可变限流的 `r2.dev`
测试域名。

## 6. Codex 与大视频

当前公开 Codex CLI 只记录 `--image/-i` 图片附件；Responses `/v1/responses` OpenAPI 中
有 `input_image`、`input_file`，没有 `input_video`，官方 `input_file` 接受列表也没有
MP4/MOV/WebM。Sora `/v1/videos` 是视频生成/编辑 API，不是 Codex 视频理解入口。

所以大视频不能作为 Base64 塞进现有 Responses 请求，也不能仅靠把 MP4 URL 放进 R2 就
让 Codex 原生观看。推荐独立异步链路：

```text
客户端分段直传 R2
 -> ffprobe/安全校验
 -> 场景切分 + 限量抽帧
 -> 提取音轨 + ASR，生成带时间戳字幕
 -> R2 保存原视频、帧和字幕（短 TTL）
 -> Responses 发送 input_image URL + input_file 字幕 URL
```

长视频应两遍处理：先每 5-10 秒/按场景抽取少量帧做粗定位，再只对相关时间窗加密抽帧；
设置总帧数、总像素和字幕大小上限。这样请求体可控，也比把几 GB 视频穿过 Sub2 稳定。

## 7. 下一步与停止线

1. 保持生产不变；本地 `Exact=false` 候选已完成完整普通/`nodynamic` 测试，下一步先做
   代码评审，再决定是否放入现有 admin/API Key scope canary。
2. 新建 R2 私有测试 bucket 后，只做一张图片的签名 URL -> Responses 真实 canary，验证
   OpenAI 抓取、TTL、重试、内容一致性和日志脱敏。
3. canary 通过后做阶段 A：只对现有 scope URL 化“压不动/超时”的图片；普通图片仍可
   使用本地 WebP hash cache。
4. 若目标是解决用户 -> Sub2 的 10Mbps 入站，再单独做阶段 B 的 CCSwitch 直传协议。
5. 视频作为独立 Phase 3，不接进同步 Attachment Gateway handler。

## 8. 官方资料

- OpenAI 图片输入：<https://developers.openai.com/api/docs/guides/images-vision>
- OpenAI 文件输入与外部 URL：<https://developers.openai.com/api/docs/guides/file-inputs>
- OpenAI Responses API：<https://developers.openai.com/api/reference/resources/responses/methods/create>
- Cloudflare R2 价格：<https://developers.cloudflare.com/r2/pricing/>
- Cloudflare R2 预签名 URL：<https://developers.cloudflare.com/r2/api/s3/presigned-urls/>
- Cloudflare R2 限制：<https://developers.cloudflare.com/r2/platform/limits/>
