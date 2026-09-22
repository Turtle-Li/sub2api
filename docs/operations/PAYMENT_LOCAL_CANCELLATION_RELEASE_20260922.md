# 本地取消与迟到付款退款发布 — 2026-09-22

用户已明确授权：测试没问题即发布线上。两端源码 QA/Review 均通过，详细源快照和测试见 `PAYMENT_LOCAL_CANCELLATION_20260922.md`。

## 发布范围与协调

- Sub2：`Turtle-Li/sub2api` 的 main；唯一当前应用目标 `sub2api-candidate:/opt/sub2api`。使用既有 GitHub workflow concurrency `sub2api-production-deploy` 序列化构建，既有 root-owned 上传锁与 `/run/sub2api-maintenance/sub2api-maintenance.lock` 协调生产操作；不另起并行发布。
- 中央：Registry 的 totools-pay 专用 SSH 身份与 `/opt/totools-pay-live`；仅该 Compose 项目及其既有发布锁。沿既有受控制品、加密备份、迁移及恢复流程执行。
- 先中央 23+24，再 Sub2 256。保留金融记录和旧制品；旧金融写入者排空后再暴露新取消行为。不得强行终止用户长连接。
- 保留 Vault 内存代理；使用现有注入，不输出或复制秘密。无真实客户支付/退款或伪造生产回调。

## 发布结果

2026-09-22 已完成线上发布。中央支付服务先完成迁移 23/24，随后 Sub2 完成蓝绿切换；没有创建真实支付单、退款单或伪造生产回调。

- 中央支付服务：`/opt/totools-pay-live` 已运行 `schema_migrations=24`。API、Worker、PostgreSQL 和四个 Vault Agent 均 healthy。新镜像为 `totools-pay-live:20260922-local-cancel24` 与 `totools-pay-postgres:20260922-local-cancel24`。
- 中央制品：归档 SHA-256 为 `ed11c160cd52b7523c14215ad5d6036ac7a9bea4354ac72fdacb94c376b6aab4`；迁移 23/24 checksum 与源文件一致。发布前后加密备份均通过 `verify` 和 `restore-check`。发布前备份为 `/backups/totools-pay-live-20260922T114733Z.pgdump.gpg`，发布后备份为 `/backups/totools-pay-live-20260922T114751Z.pgdump.gpg`。
- Sub2 源码：`c845f51aebbba49e1225ed2cad54148302344b07`，版本 `0.2.7`。GitHub CI、安全扫描和生产构建均通过；生产制品归档 digest 为 `sha256:739c4f5c2a92f696f98681348ff1c3becf4e374918d1aa00ca63e336f0574f32`。
- Sub2 线上：`sub2api-blue` 已切换到 `sub2api:auto-20260922-213152-c845f51a`，revision 与源码一致，healthy，重启数为 0。旧 `sub2api-green` 由既有 drain monitor 管理，没有强制关闭长连接。发布审计中的 `app_5xx=0`、`app_fatal=0`、`caddy_5xx=0`。
- 线上 Smoke：`https://api.turtleligpt.com/health` 返回 200，`/readyz` 返回 200，未认证 `/v1/models` 返回 401；`https://www.turtleligpt.com/` 返回 200。

## 蓝绿兼容补强

当前线上为 c14d7865，canonical 蓝绿会保留已有 HTTP/WS 连接直到自然排空。新增迁移 257 从数据库层永久拒绝 CANCELLED 改回其他业务状态；新实现对迟到付款/退款仅补资金事实，不改变 CANCELLED，因此可与旧进程安全交接。旧普通及优惠券付款路径在状态写入失败后不会履约，回调可重试到新版本。不强断用户长连接，不用临时全站停机绕开兼容问题。

首轮 c4da1463b 的 GitHub 构建及 Security 通过，CI 的集成用例发现三个独立场景共用用户、遗留待付订单影响后续测试；已改为每场景独立用户，保留单待付约束与设置行锁断言。另修正测试资源清理 errcheck 和已有 Codex 测试的纯格式问题。新源需重新完成完整 CI 和制品构建。

本机 Docker API 无响应；不重启其他本地业务容器。中央制品改为固定工具链本地交叉编译，并从已验证旧制品构造等价的不可变二进制覆盖层，独立验证 archive/config/layer/ELF/嵌入迁移。新增 Sub2 PostgreSQL 用例由 GitHub 干净 Docker 环境运行，并在隔离备份恢复库中演练迁移。
