# Attachment Gateway 请求级预算与 64MB 入站验证

日期：2026-07-20
状态：代码与本地门禁完成；尚未切换生产 Caddy。

## 结论

服务器瓶颈是 TX（Sub2 → OpenAI），不是 RX（Client → Sub2）。因此可以只为
Responses 放宽原始入站 body，让 Sub2 先压缩，再对实际上游 body 做硬限制。

安全数据流为：

```text
Responses 原始 body（Caddy 最多 64MB）
  -> 原始 body 重复请求保护
  -> scoped Attachment Gateway
  -> 聚合小图压缩与 hash 缓存
  -> 候选附件预算
  -> 全局 Responses 上游 body 最多 16,000,000 B
  -> OpenAI / failover
```

不能直接全站删除 Caddy 16MB 限制。非 Responses 路由仍保持 16MB；未进入优化器或
优化失败的 Responses 请求也必须受 Sub2 的 16,000,000 B 上游转发保险保护。

## 实现

- `request_budget_enabled=false`：默认不扫描聚合附件；
- `request_budget_enforce=false`：默认只记录 `budget_would_reject`；
- `aggregate_small_image_enabled=false`：默认不改变 512 KiB 单图阈值；
- 聚合压力默认条件：支持图片 decoded 总量达到 4 MiB，或支持图片达到 8 张；
- 聚合压力下的候选单图阈值：128 KiB；
- 候选限制：32 个内联附件、12 MiB 内联数据、14 MiB 候选上游 body；
- count 超限，或完全不可优化的 PDF/Office/audio/video 超限，会在编码前拒绝；
- 可压缩图片先压缩，预算使用压缩后的候选 body 判断；
- `gateway.openai_responses_max_forward_body_size=0` 默认关闭。生产放宽 Caddy 前设置为
  `16000000`，对 scoped/unscoped、HTTP/WS 都生效；
- 日志只有数量、字节、耗时和原因，不记录 Base64、文件内容、prompt 或 hash。

现有 `max_images_per_request` 和 `max_total_image_bytes_per_request` 仍只是 fail-open 的
CPU/内存护栏，没有被改成用户拒绝语义。

## 真实流量佐证

生产 API Key 27 在 17:59–18:09 的连续 HTTP Responses 中，图片从 1 张增长到 7 张：

| 图片数 | 原 body | decoded 图片总量 | 旧策略结果 |
|---:|---:|---:|---|
| 1 | 823,661 B | 178,274 B | `<512KiB`，跳过 |
| 5 | 1,530,808 B | 698,074 B | 5 张全部跳过 |
| 7 | 1,905,422 B | 923,975 B | 7 张全部跳过 |

这正是请求级预算要覆盖的“小图很多、单图都没到阈值”场景。第 8 张会触发默认 count
压力策略，其中达到 128 KiB 的 PNG/JPEG/WebP 会进入既有 WebP/hash 缓存链路。

## 本地结果

两张中小 PNG 聚合样本：

| 指标 | 结果 |
|---|---:|
| 原 body | 422,015 B |
| 候选 body | 48,349 B |
| 降幅 | 88.54% |
| 冷处理 | 68–106 ms |
| 热缓存 | 约 17 ms |
| 第二次 cache hits | 2/2 |

另外，既有 loopback 上游限制用例本轮为 `2,448,643 → 255,732 B`，下降 89.56%，
热请求命中缓存。PNG/JPEG/WebP、透明图、代码/UI OCR 与照片质量门禁继续通过。

已通过：

```text
go test -tags nodynamic ./... -count=1
go test -race ./internal/service/attachment_gateway (新增聚合/预算/缓存用例)
go vet ./internal/config ./internal/service/attachment_gateway ./internal/handler
```

HTTP handler 集成测试确认：预算拒绝返回 413，且 upstream 调用次数不增加；WS
passthrough/ctx_pool 首帧允许场景保持通过。

## Caddy 候选

已用生产同版本 `caddy:2.11-alpine` 离线验证候选配置：

- `/v1/responses`、`/responses`：64MB；
- 其他路径：16MB；
- 两组 `Content-Length` 提前拒绝规则分别为 64,000,000 与 16,000,000 B；
- chunked body 仍由对应 `request_body` 上限约束。

## 生产切换顺序

1. 先发布应用代码，Caddy 保持 16MB；
2. 设置全局 Responses 上游转发保险 `16000000`；
3. Key 27 与 admin user 1 开启 request-budget observe 和聚合小图 rewrite；
4. 健康检查与小于 16MB smoke 通过后，只把 Responses 入站提高到 64MB；
5. 用 admin 测试 16–64MB 可压缩 body、不可压缩文件、HTTP 热缓存和 WS 回归；
6. 观察 `budget_would_reject` 后，再只对当前 scope 开启 enforcement；
7. 普通用户的附件优化仍不全量开启。
