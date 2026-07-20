# Attachment Gateway R2 URL MVP

日期：2026-07-20；生产 canary 更新：2026-07-21

状态：代码与后台配置已发布到生产 `7affb6a03`。真实 R2 读写探针、HTTP 图片矩阵和当前
WS `http_bridge` 图片/多 turn canary 已通过；运行态只允许 API Key 27 与 admin user 1，
`allow_unscoped=false`，普通用户未开启。`ctx_pool` 同条件对比复现两次 stale pooled
connection 1011，账号 2 已恢复 `http_bridge`。

## 生产 scoped canary 更新

- active：`sub2api-blue` / `sub2api:auto-20260721-0206-7affb6a0`，healthy；
- R2 后台保存并热加载成功，Put→presigned GET→Delete 通过；
- JPEG：5,020,854 → 789 B；首次上传后同图两次均 URL cache hit；
- WebP：2,064,342 → 789 B；复杂透明 PNG：5,911,495 → 779 B；
- 5 图：13,929,029 → 3,090 B，热请求 5/5 URL cache hit；
- 长上下文+图：5,086,850 → 66,785 B；
- HTTP 所有 OCR、代码/UI、照片、透明图 marker 与原图 baseline 一致；
- WS `http_bridge` 冷对象 1,261,586 → 861 B，`url_upload_count=1`；同图热请求
  `url_upload_count=0`、`url_cache_hits=1`；两次视觉答案完全一致；
- 同一 WS 连接连续 5 turn 全部完成；`read_upstream=0`、`write_upstream=0`、
  lease lost=0、panic=0、异常关闭=0。

本轮发现的扩量前缺口已有本地候选修复：重编码不足 5% 节省的图片会安全保留原图并由
R2 URL 化；生产版本重复请求虽命中 URL cache，仍会重新编码约 1.6–2.3 秒。本地新增
有界负结果缓存后，真实 libwebp 579,454 B 样本从 cold 463.8ms 降到 warm 16.3ms，
且 `negative_cache_hits` 与正缓存分开。该修复尚未发布，仍不得扩大普通用户 scope。

结构化数据：`docs/reports/data/attachment_gateway_r2_canary_20260721.json`。

## 目标与边界

在现有图片压缩和 decoded-bytes SHA-256 缓存之后，增加可独立关闭的 URL 化阶段：

```text
Base64 图片 → WebP/无损 WebP → 本地 hash 缓存
             → 私有 R2 hash 对象 → 短时签名 GET URL
             → Responses image_url 替换
```

本阶段只处理 Responses 已支持的 PNG、JPEG、WebP 图片 data URL，不处理 PDF、Office、
音频、视频或 `file_id`。URL 化只在显式 allowlist 且 rollout=`rewrite` 时执行；dry-run
不会查询、写入或测试 R2。

## 独立配置

Attachment Gateway 不再复用数据库备份或异步生图的 S3 凭证。独立设置 key 为：

```text
attachment_gateway_r2_config
```

管理入口：系统设置 → 存储与备份 → Attachment Gateway 专用 R2。该位置与上游新增的
异步/批量生图对象存储卡片保持一致，但凭证仍独立保存，不与数据库备份或生图复用。
管理 API：

```text
GET  /api/v1/admin/attachment-gateway/r2-config
PUT  /api/v1/admin/attachment-gateway/r2-config
POST /api/v1/admin/attachment-gateway/r2-config/test
```

可配置字段：启用状态、endpoint、region、bucket、Access Key ID、Secret Access Key、
对象基础前缀、path-style 与签名 URL 有效分钟数。

安全行为：

- Secret 使用现有 AES-256-GCM `SecretEncryptor` 加密保存；
- GET 永远不返回 Secret，只返回 `secret_configured`；
- 更新时 Secret 留空会保留旧值，并重新以密文写入，不会降级成明文；
- PUT 受管理员 step-up 2FA 保护；管理面读写进入审计日志，Secret 字段脱敏；
- endpoint 只接受不含凭证、query、fragment 的绝对 HTTPS URL；
- 连通测试不持久化表单，会写入、通过 presigned URL 读取并删除一个极小探针对象；
- 当前进程保存后立即热加载，其他实例最多 30 秒重新读取设置；
- 存储配置启用不等于附件实验全量开启，静态实验开关、allowlist、rollout=`rewrite`
  仍缺一不可。

## 已实现的数据路径

- 以优化后图片 SHA-256 生成确定性对象 key；
- 可选 R2 基础 prefix 与实验 `attachments/` 子路径组合；
- R2/S3 `HeadObject` 命中时只生成新签名 URL，进程重启后不重复上传；
- 进程内 URL TTL 缓存与 singleflight 避免相同 hash 重复上传；
- 配置变更会失效旧客户端和旧 bucket 的签名 URL 缓存；
- URL 缓存与 singleflight 都绑定存储配置版本；若 bucket/凭证在上传中途切换，该图片
  fail-open 回压缩后的 Base64，旧上传结果不会重新写回新一代缓存；
- URL 缓存会解析 SigV4 `X-Amz-Date` / `X-Amz-Expires`，提前一分钟停止复用，
  即使管理员把配置缓存时间设得更长也不会继续返回临近过期 URL；
- 只接受对象存储返回的 HTTPS URL；
- 上传失败、超时、未配置、非法 URL 均 fail-open，继续转发压缩后的 data URL；
- 日志只记录计数、大小、耗时、存储就绪、上传/缓存结果，不记录 URL、hash、对象 key、
  图片或凭证；
- 请求预算在 URL 替换后重新计算；dry-run 保证零外部写入。

## 默认安全配置

```yaml
gateway:
  attachment_gateway:
    attachment_optimizer_enabled: false
    attachment_optimizer_dry_run: true
    url_rewrite_enabled: false
    url_rewrite_min_body_bytes: 524288
    url_upload_timeout_ms: 15000
    url_object_prefix: "attachments/"
    url_cache_ttl_seconds: 900
    max_concurrent_url_uploads: 2
    allow_unscoped: false
```

R2 后台表单默认关闭，region=`auto`，presign expiry=60 分钟。即使保存并启用 R2，
上述三个实验门和精确白名单未满足时，请求仍保持当前行为。

## Cloudflare 侧要求

1. 使用私有 bucket，不开启公开 `r2.dev`；
2. 创建只对该 bucket 生效的 Object Read & Write API Token；不需要 Admin Read & Write；
3. 在后台填写 R2 S3 API endpoint、bucket、Access Key ID 与 Secret Access Key；
4. 设置对象生命周期，实验期建议 1–7 天自动删除；
5. Secret 只在后台填写，不发到聊天、不写配置仓库或测试报告。

## 当前自动验证

- 独立配置加密、脱敏、空 Secret 保留和损坏密文拒绝；
- 测试连接使用已保存 Secret，且不会持久化临时表单；
- 连通探针完成 Put → presigned GET → Delete；
- 保存后动态客户端热更新，对象 prefix 正确组合；
- 其他实例模拟设置变化后在刷新窗口重建客户端；
- 未配置/关闭时整请求跳过 URL 阶段，不逐图报错；
- URL 外置器关闭时字节级 no-op；
- 首次相同图片上传一次，后续命中 URL 缓存；
- 配置版本变化会清除旧签名 URL；签名安全期限会截断 URL 缓存；
- 本地缓存缺失但对象已存在时走 `HeadObject`，不重复上传；
- R2 错误与非 HTTPS URL 均保留原 data URL；
- dry-run 不调用对象存储，rewrite 才替换 payload；
- 管理后台专用表单、保存、空 Secret 和读写测试组件测试通过；
- 后端 `go test -tags nodynamic ./...` 全仓通过；先前与前端大测试并行时出现过一次
  WS `ctx_pool/dry_run` 测试超时，改为串行后连续 5 次及全仓回归均通过，未复现功能错误；
- R2 动态配置、URL 外置器及“配置切换发生在上传中途”的定向 race 测试通过；
- 负结果缓存的 decoded-bytes hash、并发 singleflight、进程重启、TTL、损坏 metadata、
  policy 指纹变化、条目容量清理及正/负指标隔离定向 race 测试通过；
- `go vet -tags nodynamic ./...` 通过；
- macOS arm64 本机构建和 `linux/amd64 + CGO_ENABLED=0 + nodynamic` 静态交叉构建通过；
- 前端 ESLint、`vue-tsc`、Vite 生产构建通过；
- 前端全量 Vitest：178 个测试文件、1229 项测试全部通过。

## 仍需完成的生产候选验证

- 积累日本服务器→R2 冷上传 p50/p95；当前只有少量冷样本，复杂透明图约 7.74 秒；
- 若生产还会调度 OpenAI API Key 上游，补该账号类型的签名 URL 拉取验证；当前真实模型
  canary 使用 ChatGPT OAuth Codex 上游；
- 修复 `ctx_pool` stale pooled connection 健康检查/重试换连，再用隔离账号重跑；当前
  两次已有 R2 图片对象请求均 1011，而新连接 cold/hot 与 5-turn 文本可成功；
- 进程重启后的 `HeadObject` 命中和生命周期删除后同 hash 重新上传；
- 将本地负结果缓存发布到当前精确 scope，并用生产原样本验证热命中与 R2 URL cache；
- 继续观察目标 API Key 27 的真实大图片请求，确认原始 413/retry 故障闭环。

`url_rewrite_enabled` 当前只允许既有精确 scope；完成上述验证前不得扩大到普通用户。
