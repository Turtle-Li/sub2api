# Attachment Gateway 生产内部灰度运行手册

日期：2026-07-20
执行状态：**HTTP scoped canary 已执行**。method 6 触发停止线后已回退；method 0 修正
完成发布、dry-run、rewrite、真实图片 A/B 与缓存验证。Key 27 + admin user 1 保持精确
范围，普通用户未开启。真实 WS 流量尚未出现。

## 0. 执行摘要

- 活跃版本：`17b7be8d11e88437302bb4cf05ed9a29e9348311`；蓝绿发布与健康审计通过；
- 当前范围：`allowed_api_key_ids=[27]`、`allowed_user_ids=[1]`、
  `allow_unscoped=false`；
- 当前模式：`rewrite`；控制文件为 0600，属主与应用配置一致，应用 UID 可读；
- 近 14 MB 重复 HTTP：13,749,545 → 2,945,577 B，8 个 cache hit，984.4 ms；
- 原图/rewrite：PNG、JPEG、WebP、透明 PNG、5 图和 8 重复图的固定答案全部一致；
- WS：桌面客户端相关 feature 已移除，生产最近 2 小时无 WS usage，不能给出真实 WS
  稳定性结论；自动化 passthrough/ctx_pool 矩阵通过。

首次切 `dry_run` 时发现控制文件由 root 原子创建为 `root:root 0600`，应用 UID 1000
无法读取，因而正确 fail-closed 为 `off`。修正为继承 `config.yaml` 属主后生效。以后所有
原子切换都必须同时保留原属主、0600 权限并用应用 UID 回读验证。

第一轮 method 6 中 JPEG 和透明 PNG 达到 5 秒预算，因此按第 8 节立即关闭。method 0
保持 q85/q90/lossless，仅改变 libwebp 速度/搜索强度，随后四格式均在预算内。完整指标见
`docs/reports/data/attachment_gateway_production_canary_20260720.json`。

## 1. 灰度目标与边界

本次只回答四个问题：

1. 实际 Sub2 → upstream body 和重放 bytes 是否显著下降；
2. 原图与 WebP 对 OCR、代码、UI、普通照片理解是否有业务可见差异；
3. HTTP、WS passthrough、WS ctx_pool 的 timeout、TTFT、`read_upstream` 是否回归；
4. 同一 decoded hash 的冷/热缓存是否生效。

禁止事项：

- 不对普通用户或全分组开启；
- `allow_unscoped` 不得设为 `true`；
- 不修改 Caddy body limit；
- 不接 R2/S3、不替换 URL；
- 不删除现有缓存、旧容器、数据库或 release 日志；
- 不在日志、报告或命令行保存 API key、base64、图片内容或 prompt。

## 2. 用户醒来后的只读预检

连接方式只使用项目 `AGENTS.md` 中的 `ssh sub2api-new`。先确认：

- 本地待发布 commit、工作树和完整测试结果；
- `/etc/sub2api-autodeploy.env` 中的 production branch、`SUB2API_APP_DIR` 和日志目录；
- `sub2api-autodeploy.service` 状态；
- Caddy 当前只指向一个 healthy active container；
- active container 的 image、mount、CPU/RSS、磁盘余量；
- candidate 镜像使用根 Dockerfile 的 `embed nodynamic` 构建，amd64 二进制可在最终
  Alpine 内启动且没有 glibc interpreter 依赖；
- 实际配置文件或 named volume 位置；
- 最近 30 分钟 413、timeout、`read_upstream`、WS close baseline。

不要打印完整环境文件或配置；只读取必要的非敏感键。若服务器真实布局与
`deploy/README.md` 不一致，停止并先更新项目部署记录，不能猜路径。

## 3. 发布代码，运行态保持关闭

按 `deploy/README.md` 记录的 GitHub Actions → restricted SSH → blue-green 流程发布，
不得手工绕过健康检查。应用默认值始终保持 `attachment_optimizer_enabled=false`；本次
已授权 canary 可在切流前预置第 4 节的精确白名单配置，但控制文件初始必须为：

```text
off
```

因此新容器虽已构造优化器，所有生产请求的运行态仍逐字节 passthrough，且不会压缩或
写缓存。这样可以在不再次回收长连接容器的情况下完成关闭态 smoke，再实时进入 dry-run。

发布后先做关闭态 smoke：

- `/health` 和现有 Responses HTTP 正常；
- WS passthrough、ctx_pool 各完成一轮；
- 没有 `openai.attachment_gateway_experiment` 事件；
- 请求 body、账号选择、计费、retry、TTFT 与发布前基线一致；
- Caddy 配置与限制不变。

关闭态失败时直接走现有 blue-green rollback，不进入 canary。

## 4. Stage A：目标 key + admin 用户 dry-run

备份实际配置后，仅填入用户批准的目标 key ID 与 admin user ID；不要在报告里写出 key
内容：

```yaml
gateway:
  attachment_gateway:
    attachment_optimizer_enabled: true
    attachment_optimizer_dry_run: true
    rollout_control_file: "data/attachment_gateway.mode"
    allow_unscoped: false
    allowed_api_key_ids: [<APPROVED_TARGET_API_KEY_ID>]
    allowed_user_ids: [<APPROVED_ADMIN_USER_ID>]
    allowed_group_ids: []
    optimize_timeout_ms: 5000
    threshold_bytes: 524288
    max_image_bytes: 67108864
    max_total_image_bytes_per_request: 33554432
    max_pixels: 50000000
    quality: 85
    special_quality: 90
    min_savings_ratio: 0.05
    cache_dir: "data/attachment_cache"
    cache_ttl_seconds: 86400
    cache_max_bytes: 536870912
    cache_cleanup_interval_seconds: 600
    max_images_per_request: 10
    max_concurrent_encodes: 1
```

`<...>` 必须替换成十进制 ID，不能把占位符原样写进 YAML。目标用户按单 API Key
限制；管理员可按 user ID 允许其轮换的本地测试 Key。通过已确认的生产配置
装载/蓝绿发布流程生效，不要临时改 Caddy 或容器命令行。切流前必须以 0600 权限原子
写入控制文件，初始内容为 `off`；关闭态 smoke 通过后再原子改为 `dry_run`。文件缺失、
超过 64 字节或内容非法都会 fail-closed 为 `off`。

发送 6–10 个授权请求：单张截图、5 图、长 context+图、代码图、UI 图、照片。原 body
必须小于当前 Caddy 前置门，否则只能验证 Caddy 413，无法验证 Attachment Gateway。

Stage A 验证：

- upstream 实际仍收到原 PNG/JPEG data URL；
- 日志 `dry_run=true`、`payload_rewritten=false`；
- `optimized_body_bytes` 比 `original_body_bytes` 至少低 70%；
- `forward_body_bytes == original_body_bytes`；
- 不出现图片内容、base64、hash 或 cache path；
- 单 key 以外的请求没有实验日志、没有缓存工作、没有延迟变化。

注意：dry-run 是同步测量，会增加该内部 key 的延迟；它不是零成本 shadow job。

## 5. Stage B：单内部 key rewrite

Stage A 无停止线后，只修改：

```text
rewrite
```

即原子更新 `rollout_control_file` 的内容，无需重启容器；其他范围和资源限制完全不动。
依次执行：

| Case | HTTP | WS passthrough | WS ctx_pool | 观察 |
|---|---|---|---|---|
| 单张大 PNG | 是 | 是 | 是 | body、TTFT、质量 |
| 5 张图 | 是 | 可选 | 是 | 总 bytes、CPU/RSS |
| 长 context + 图 | 是 | 否 | 是 | ctx_pool 稳定性 |
| 同图第二次发送 | 是 | 否 | 是 | cache hit、热耗时 |

日志应满足：

- `payload_rewritten=true`；
- `forward_body_bytes == optimized_body_bytes`；
- 第二次相同图片 `cache_hit=true` / `cache_hits>0`；
- `timed_out=false`、`errors=0`；
- upstream/账号 failover 中复用同一优化 body，不在每次尝试重复压缩。

## 6. 图片内容 A/B

使用固定模型、固定结构化 prompt 和已知答案 fixture。主矩阵固定 `detail=high`；若当前
Codex 模型支持 `detail=original`，代码/UI 各补一组 original A/B。每一对原图/WebP
必须使用相同 detail，不能把 detail 差异误判为压缩差异。每类至少 3 张：

- 代码：逐行抄录、标识符、错误行号、符号；
- UI：按钮/标签文字、相对位置、数值；
- OCR：小字、数字、大小写和标点；
- 照片：对象、颜色、数量、空间关系。

Stage A 的原图响应作为 baseline，Stage B 的实际 WebP 响应作为 candidate。评分保存结构化
结果和人工结论，不保存图片/base64 到日志。模型输出有随机性，至少重复 3 次；任何关键
字符、代码 token、UI 坐标或对象关系稳定退化都视为停止线。

## 7. WS / ctx_pool 观察

分别记录关闭态与 rewrite 态：

- 首 frame bytes；
- TTFT；
- `read_upstream` retry；
- `write_upstream` retry；
- timeout；
- abnormal close code/reason；
- connection reuse；
- 首轮完成后第二轮是否正常。
- 超时后 CPU worker 是否在配置并发内收敛，没有不同 hash 的后台编码堆积。

Attachment Gateway 当前只改写首轮。第二轮图片仍是原 payload 属于已知边界，不是缓存
失效；若实际用户经常在后续 turn 发图，再单独设计可返回替换 payload 的 transform hook。

## 8. 停止线

任一条件成立立即关闭 feature，不继续扩量：

- 图片语义/OCR/代码关键 token 有可重复退化；
- `timed_out` 或 `errors` > 1%，或连续出现 2 次；
- 新增 WS timeout、异常 close、`read_upstream`/`write_upstream` retry；
- 冷处理超过 5 秒并 fail-open，或热缓存没有明显改善；
- 超时请求结束后编码 CPU 未收敛，或实际同时编码数疑似超过
  `max_concurrent_encodes`；
- 进程 CPU 持续 >70%、RSS 接近容器预算、GC/TTFT 明显恶化；
- cache 目录权限、容量清理、磁盘余量异常；
- 非 allowlist key 出现实验日志或 payload 改写；
- 健康检查、计费、账号选择、审计或 failover 出现回归。

## 9. 关闭与回滚

首选秒级逻辑关闭：

```text
off
```

即原子更新 `rollout_control_file`，并验证实验事件停止、请求恢复原 payload；无需为了
关闭实验而重启活跃容器。缓存先保留
为 0700/0600 的 TTL 数据，不在事故处理中顺手删除；如需清理，另行确认精确 cache 目录
与保留要求。

只有关闭 feature 后仍有回归，才使用现有 blue-green release rollback。不得用
`git reset --hard`、删除 volume 或修改 Caddy 作为附件实验回滚手段。

## 10. 扩量结论

完成 Stage B 后输出：样本数、压缩分布、冷/热 p50/p95、cache hit、CPU/RSS、HTTP/WS
错误、质量 A/B。建议仅在全部停止线为 0 时扩到第二个内部 key；普通用户灰度需要另一次
明确审批，默认开关继续保持关闭。
