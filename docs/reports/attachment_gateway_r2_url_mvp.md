# Attachment Gateway R2 URL MVP

日期：2026-07-20

状态：本地实验实现，默认关闭；未填写 R2 凭证，未连接 R2，未部署或修改生产配置。

## 目标与边界

在现有图片压缩和 decoded-bytes SHA-256 缓存之后，增加可独立关闭的 URL 化阶段：

```text
Base64 图片 → WebP/无损 WebP → 本地 hash 缓存
             → 私有 R2 对象 → 短时签名 GET URL
             → Responses image_url 替换
```

本阶段只处理 Responses 已支持的 PNG、JPEG、WebP 图片 data URL，不处理 PDF、Office、
音频、视频或 `file_id`。URL 化只在显式 allowlist 且 rollout=`rewrite` 时执行；dry-run
不会写 R2。

## 已实现

- 复用项目现有 S3 兼容存储适配器，可用于 Cloudflare R2；
- URL 开关与 `image_storage.enabled` 独立，不会顺带开启异步生图接口；
- 以优化后图片 SHA-256 生成确定性对象 key；
- R2/S3 `HeadObject` 命中时只生成新签名 URL，不重复上传图片；
- 进程内 URL TTL 缓存和 singleflight，避免相同 hash 并发上传；
- 只接受对象存储返回的 HTTPS URL；
- 上传失败、超时、非法 URL 均 fail-open，继续转发压缩后的 data URL；
- 日志只记录计数、大小、耗时、上传/缓存结果，不记录 URL、hash、对象 key、图片或凭证；
- 请求预算在 URL 替换后重新计算。

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
```

对象存储复用 `image_storage` 的 endpoint、bucket、Access Key 与 Secret。私有桶应保持
`public_base_url: ""`，由服务端生成 presigned GET URL；`url_cache_ttl_seconds` 必须短于
`presign_expiry_hours`。

## Cloudflare 侧申请清单

1. 启用 R2 并创建私有 bucket；
2. 创建只对该 bucket 生效的 Object Read & Write API Token；
3. 保存 Account ID、R2 endpoint、Access Key ID、Secret Access Key 和 bucket 名；
4. 设置对象生命周期，实验期建议 1 天自动删除；
5. 不开启公开 `r2.dev` 访问，首版不需要自定义域名；
6. 凭证只写入服务器密钥配置，不发送到聊天、不写仓库。

## 当前验证

- URL 外置器关闭时字节级 no-op；
- 首次相同图片上传一次，后续命中 URL 缓存；
- 本地缓存缺失但对象已存在时走 `HeadObject`，不重复上传；
- R2 错误与非 HTTPS URL 均保留原 data URL；
- dry-run 不调用对象存储；rewrite 才替换 payload；
- URL 外置器 race 测试通过；
- `go test -tags nodynamic ./...` 全仓通过；
- `CGO_ENABLED=0 go build -tags nodynamic ./cmd/server` 通过。

## 仍需真实 R2 验证

- 日本服务器到 R2 的首次上传耗时及 p50/p95；
- OpenAI API Key 与 ChatGPT OAuth Codex 上游是否都能及时拉取签名 URL；
- 签名 URL 查询参数、Content-Type、重试和 failover；
- 重复 hash 在进程重启后的 `HeadObject` 命中；
- R2 生命周期删除后能否正常重新上传；
- 原图、压缩 data URL、R2 URL 三组模型识别结果一致性。

真实验证通过前，`url_rewrite_enabled` 必须保持 `false`，不得扩大到普通用户。
