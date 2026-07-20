# Attachment Gateway R2 URL MVP

日期：2026-07-20

状态：本地实验实现与自动回归完成；默认关闭，未填写真实凭证、未连接用户 R2、未推送、
未部署或修改生产配置。

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

管理入口：系统设置 → 附件网关。管理 API：

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
- `go vet -tags nodynamic ./...` 通过；
- macOS arm64 本机构建和 `linux/amd64 + CGO_ENABLED=0 + nodynamic` 静态交叉构建通过；
- 前端 ESLint、`vue-tsc`、Vite 生产构建通过；
- 前端全量 Vitest：178 个测试文件、1229 项测试全部通过。

## 仍需真实 R2 canary

- 本机后台填写真实 R2 后执行读写探针；
- 日本服务器到 R2 的首次上传耗时及 p50/p95；
- OpenAI API Key 与 ChatGPT OAuth Codex 上游是否都能及时拉取签名 URL；
- 原图、压缩 data URL、R2 URL 三组 OCR、代码/UI、照片识别结果一致性；
- HTTP 相同 hash 的进程内 URL 命中，以及重启后 `HeadObject` 命中；
- 5 图、长上下文+图、复杂透明 PNG 与压不小图片的 body、CPU、TTFT；
- HTTP / WS passthrough / ctx_pool 的 timeout、retry、异常关闭与首 token；
- 生命周期删除后同 hash 能否正常重新上传。

真实 R2 与模型质量 canary 通过前，`url_rewrite_enabled` 必须保持 `false`，不得扩大到
普通用户。
