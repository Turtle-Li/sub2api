# Sub2 Attachment Optimizer 本地测试报告

日期：2026-07-20
性质：离线 POC 与 loopback mock；不是生产网络压测。
结论：本文保留 Phase 1 离线 POC 基线；最新生产 method 0 canary、16,000,000 B Caddy
门限和真实质量结果见 `attachment_gateway_phase1_report.md`。

## 1. 安全边界

本次测试：

- 生产 Sub2 请求：0；
- OpenAI 请求：0；
- 远程服务器连接：0；
- 唯一网络目标：`127.0.0.1` loopback HTTP sink；
- 未修改 Caddy、数据库、Redis、生产配置或现有 handler/forward；
- POC 位于 `experiments/attachment_optimizer/`，没有被 Sub2 runtime 引用；
- 开关默认 `attachment_optimizer_enabled=false`。

因此“AI 识别效果”采用 SSIM、Tesseract OCR 和人工视觉对照作为离线代理指标，不声称等价于真实 GPT 视觉模型评测。

## 2. 环境

```text
OS: macOS 26.5 arm64
Python: 3.11.4
Pillow: 12.2.0
NumPy: 2.4.6
pngquant: 3.0.3
oxipng: 10.1.1
zopflipng: zopfli 1.0.3_1
cwebp: 1.6.0
tesseract: 5.5.2
```

主机为 Apple Silicon；结果不能直接外推到生产 2 vCPU Xeon。

## 3. 样本与方法

代表性样本：

| 类别 | 尺寸 | 原始 PNG |
|---|---:|---:|
| 插画/照片类 | 1254×1254 | 1,476,604 B |
| UI 截图 | 1365×900 | 286,665 B |
| 代码截图 | 1800×1100 | 87,427 B |

大图 payload 样本由本地插画确定性放大并加入轻微噪声生成：2500×2100，10,579,969 B。它的视觉内容可辨认，同时接近现实中未经优化的复杂 PNG；base64 后恰好形成约 14.1 MB JSON。

评价指标：

- `saved_percent = 1 - optimized/original`；
- SSIM：1.0 为像素结构完全一致；
- 代码 OCR：与原图 Tesseract 输出做字符序列相似度；
- elapsed：单次本机 wall time；
- loopback：5 次 POST 的中位数，只比较相对开销；
- 5 Mbps 理论时间：`body_bytes * 8 / 5,000,000`，用于估算 Sub2 → 上游 TX 下限，不包含 TLS、TCP、代理与响应时间。

## 4. PNG 优化结果

### 4.1 明确术语

`oxipng` 和 `zopflipng` 是无损 PNG 重编码。`pngquant` 会把真彩色量化为调色板 PNG，属于**有损/视觉近似无损**，不能放在“严格无损”承诺下。

### 4.2 数据

| 类别 | 工具 | 原始 | 优化后 | 节省 | 耗时 | SSIM |
|---|---|---:|---:|---:|---:|---:|
| 插画/照片 | oxipng `-o4` | 1,476,604 | 1,286,132 | 12.9% | 1.992 s | 1.000000 |
| 插画/照片 | zopflipng | 1,476,604 | 1,296,234 | 12.2% | 67.237 s | 1.000000 |
| 插画/照片 | pngquant q80–95 | 1,476,604 | 573,228 | 61.2% | 0.517 s | 0.981805 |
| UI 截图 | oxipng `-o4` | 286,665 | 192,843 | 32.7% | 1.762 s | 1.000000 |
| UI 截图 | zopflipng | 286,665 | 177,632 | 38.0% | 24.298 s | 1.000000 |
| UI 截图 | pngquant q80–95 | 286,665 | 77,387 | 73.0% | 0.162 s | 0.993760 |
| 代码截图 | oxipng `-o4` | 87,427 | 64,414 | 26.3% | 1.251 s | 1.000000 |
| 代码截图 | zopflipng | 87,427 | 61,945 | 29.1% | 28.587 s | 1.000000 |
| 代码截图 | pngquant q80–95 | 87,427 | 26,337 | 69.9% | 0.179 s | 0.999910 |

判断：

- 严格无损只能省 12%–38%，无法稳定完成 14 MB → 2 MB；
- zopflipng 的 24–67 秒使其不适合在线；
- oxipng 可作为异步离线优化，但 1–2 秒仍不适合每请求同步；
- pngquant 对截图很好，但需要接受颜色量化语义和回归测试。

## 5. JPEG 与 WebP 视觉压缩

### 5.1 插画/照片类

| 格式 | 优化后 | 节省 | 耗时 | SSIM |
|---|---:|---:|---:|---:|
| JPEG q80 | 92,638 B | 93.7% | 41 ms | 0.968137 |
| JPEG q90 | 148,365 B | 90.0% | 40 ms | 0.977161 |
| WebP q80 | 43,596 B | 97.0% | 132 ms | 0.974270 |
| WebP q85 | 51,922 B | 96.5% | 135 ms | 0.975666 |
| WebP q90 | 67,702 B | 95.4% | 160 ms | 0.979082 |

### 5.2 UI 截图

| 格式 | 优化后 | 节省 | 耗时 | SSIM |
|---|---:|---:|---:|---:|
| JPEG q80 | 43,234 B | 84.9% | 28 ms | 0.987413 |
| JPEG q90 | 62,760 B | 78.1% | 24 ms | 0.990670 |
| WebP q80 | 16,246 B | 94.3% | 90 ms | 0.985041 |
| WebP q85 | 19,604 B | 93.2% | 92 ms | 0.986195 |
| WebP q90 | 23,468 B | 91.8% | 95 ms | 0.988727 |

### 5.3 代码截图

| 格式 | 优化后 | 节省 | 耗时 | SSIM | OCR 相似度 |
|---|---:|---:|---:|---:|---:|
| JPEG q80 | 89,044 B | **-1.9%** | 25 ms | 0.990745 | 0.907801 |
| JPEG q90 | 114,793 B | **-31.3%** | 31 ms | 0.995410 | 0.903319 |
| WebP q80 | 41,976 B | 52.0% | 123 ms | 0.996872 | 0.914773 |
| WebP q85 | 46,542 B | 46.8% | 126 ms | 0.997749 | 0.905444 |
| WebP q90 | 53,698 B | 38.6% | 124 ms | 0.998535 | 0.896552 |

JPEG 对代码截图反而增大；WebP 在保持较高 SSIM 的同时仍能减小 39%–52%。OCR 相似度不是严格单调，说明不能只用 quality 数字推断识别质量，需要固定的视觉回归集。

人工并排 QA 中，WebP q85 的插画、登录 UI 与代码行均保持可读，未观察到足以改变语义的伪影；但小字号、终端细线和点击坐标类图片仍应采用更保守策略。

## 6. Payload 端到端最小验证

POC 参数：WebP q85、128 KiB 阈值、至少节省 5% 才替换、保持原尺寸和 `detail`。

| 场景 | 原 body | data URL 优化后 | 降幅 | URL 化后 | 冷处理 | 缓存命中 |
|---|---:|---:|---:|---:|---:|---:|
| 1 张大图 | 14,106,892 B | 1,029,505 B | 92.7% | 351 B | 1.352 s | 64 ms |
| 5 张图片 | 3,990,160 B | 353,311 B | 91.1% | 207,169 B | 376 ms | 19 ms |
| 大图 + 1 MiB 长上下文 | 15,169,449 B | 2,092,062 B | 86.2% | 1,062,908 B | 1.274 s | 71 ms |

五图场景中有两张低于 128 KiB，按策略保持原样；其余三张被压缩并在第二次请求中全部命中缓存。

### 6.1 14 MB → 2 MB 目标

目标成立，而且有余量：

```text
原 PNG bytes:             10,579,969
base64 JSON request:      14,106,892
WebP q85 bytes:              771,930
优化后 data URL request:   1,029,505
```

原图到 JSON 的约 33% 放大来自 base64。仅转换编码格式，不改变尺寸，就把 body 降低 92.7%。

## 7. 转发与理论带宽影响

| 场景 | loopback 原 body | loopback 优化后 | 5 Mbps 原 body | 5 Mbps 优化后 |
|---|---:|---:|---:|---:|
| 1 张大图 | 5.76 ms | 0.97 ms | 22.57 s | 1.65 s |
| 5 张图片 | 2.73 ms | 0.70 ms | 6.38 s | 0.57 s |
| 大图 + 长上下文 | 5.21 ms | 1.31 ms | 24.27 s | 3.35 s |

loopback 数字只证明更小 body 的本机 HTTP copy 更快。5 Mbps 数字是理想下限，不包含 TLS、拥塞、上游处理、SSE/WS、代理重试和响应时间。

冷处理 1.35 s 远小于可节省的 20 秒级 5 Mbps 上游发送时间，但它会占 CPU，并可能在并发时扩大 TTFT。生产不能无限并行压缩。

## 8. 缓存验证

单元测试与 payload 场景共同验证：

- 同一 decoded image 只生成一份 `<source-sha256>.webp`；
- 同一 source 对应一份 `<source-sha256>.metadata.json`；
- 第二次请求校验 metadata 和 optimized hash 后复用；
- quality 或编码器版本改变时不复用旧结果；
- 缓存文件原子写入、owner-only；
- 坏 base64、动画或解码失败时原 URL 放行；
- remote URL 与 `file_id` 保持不变。

性能：

| 场景 | 冷处理 | 热处理 | 加速 |
|---|---:|---:|---:|
| 1 张 10.58 MB PNG | 1.352 s | 64 ms | 约 21× |
| 5 张混合图片 | 376 ms | 19 ms | 约 20× |
| 大图 + 长上下文 | 1.274 s | 71 ms | 约 18× |

热处理仍包含 base64 解码和全图 hash，不能视为零成本。

## 9. 413 模拟结论

以下为当时项目 Caddy 快照的十进制 `10,000,000 B` 历史模拟；当前生产门已复核为
`16,000,000 B`：

| 场景 | 原请求状态 | 如果在 Caddy 前已优化 |
|---|---:|---:|
| 1 张大图（14.11 MB） | 413 | 200 |
| 5 张图片（3.99 MB） | 200 | 200 |
| 大图 + 长上下文（15.17 MB） | 413 | 200 |

但当前 POC 设计在 Sub2 内部，顺序是：

```text
Client → Caddy body gate → Sub2 optimizer → OpenAI
```

所以实际生产中，前两条大请求在 optimizer 运行前就会 413。表格里的“200”只代表**优化器位于 Caddy 前**或 Caddy 临时允许原 body 进入时的大小判定，不代表本次 POC 已消除生产 413。

## 10. 测试与复现

单元测试：

```text
python3 -m unittest -v
Ran 6 tests in 0.208s
OK
```

覆盖：

- 默认关闭逐字节 no-op；
- Responses `input_image` 压缩并保留 `detail`；
- cache miss/hit；
- URL 替换；
- `file_id` 与 remote URL 不改写；
- 坏 base64 fail-open。

复现：

```bash
cd experiments/attachment_optimizer
python3 -m unittest -v
python3 run_benchmarks.py \
  --asset-root '/Users/lijiayu/auto_work/sub2 api' \
  --output ../../docs/reports/data/attachment_optimizer_benchmark.json
```

原始结构化结果：[attachment_optimizer_benchmark.json](./data/attachment_optimizer_benchmark.json)。

## 11. 未覆盖项

- 未使用真实 OpenAI vision 请求比较回答一致性；
- 未连接真实 Responses WebSocket/ctx_pool；
- 未测真实 `read_upstream`、TTFT、上游 413 或 failover；
- 未在生产同规格 2 vCPU Xeon 上测压缩 CPU/内存；
- 未测透明 PNG、ICC、EXIF、极端长宽比、动画 GIF、解压炸弹与并发 cache stampede 的完整矩阵；
- 未实现或验证签名 URL、Caddy 静态路由、R2/S3；
- 未测 Anthropic/自定义兼容上游对 WebP data URL 的完整兼容性；
- 未测图片在 OpenAI 内部异步抓取所需的 URL TTL。

## 12. 测试结论

- **压缩比例：通过。** 14.1 MB → 1.03 MB，超过 14 MB → 2 MB 目标。
- **缓存：通过。** 重复图冷/热处理约有 18–21× 差异。
- **视觉质量：初步通过。** WebP q85 在三类视觉对照中保持较高 SSIM并可读，但需真实模型回归集。
- **性能：有条件通过。** 单图冷压缩约 1.35 s，必须限并发、设超时和 fail-open；zopflipng 不可在线。
- **入口 413：边界不变。** Sub2 内优化位于 Caddy 后，无法绕过当前 16,000,000 B gate；
  但 13,749,545 B 的生产 HTTP canary 已进入 Sub2 并降到 2,945,577 B。
- **R2：暂不需要。** 先验证 data URL 路线的上游效果。
- **ctx_pool：继续独立推进。** 本次只证明 payload 变小，未证明 WS `read_upstream` 或 TTFT 改善。
