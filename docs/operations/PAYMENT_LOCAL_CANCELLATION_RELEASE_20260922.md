# 本地取消与迟到付款退款发布 — 2026-09-22

用户已明确授权：测试没问题即发布线上。两端源码 QA/Review 均通过，详细源快照和测试见 `PAYMENT_LOCAL_CANCELLATION_20260922.md`。

## 发布范围与协调

- Sub2：`Turtle-Li/sub2api` 的 main；唯一当前应用目标 `sub2api-candidate:/opt/sub2api`。使用既有 GitHub workflow concurrency `sub2api-production-deploy` 序列化构建，既有 root-owned 上传锁与 `/run/sub2api-maintenance/sub2api-maintenance.lock` 协调生产操作；不另起并行发布。
- 中央：Registry 的 totools-pay 专用 SSH 身份与 `/opt/totools-pay-live`；仅该 Compose 项目及其既有发布锁。沿既有受控制品、加密备份、迁移及恢复流程执行。
- 先中央 23+24，再 Sub2 256。保留金融记录和旧制品；旧金融写入者排空后再暴露新取消行为。不得强行终止用户长连接。
- 保留 Vault 内存代理；使用现有注入，不输出或复制秘密。无真实客户支付/退款或伪造生产回调。

## 当前状态

发布准备中。部署、备份、迁移和线上 Smoke 的实际证据将追加，不以本地测试冒充线上验收。

## 蓝绿兼容补强

当前线上为 c14d7865，canonical 蓝绿会保留已有 HTTP/WS 连接直到自然排空。新增迁移 257 从数据库层永久拒绝 CANCELLED 改回其他业务状态；新实现对迟到付款/退款仅补资金事实，不改变 CANCELLED，因此可与旧进程安全交接。旧普通及优惠券付款路径在状态写入失败后不会履约，回调可重试到新版本。不强断用户长连接，不用临时全站停机绕开兼容问题。

首轮 c4da1463b 的 GitHub 构建及 Security 通过，CI 的集成用例发现三个独立场景共用用户、遗留待付订单影响后续测试；已改为每场景独立用户，保留单待付约束与设置行锁断言。另修正测试资源清理 errcheck 和已有 Codex 测试的纯格式问题。新源需重新完成完整 CI 和制品构建。

本机 Docker API 无响应；不重启其他本地业务容器。中央制品改为固定工具链本地交叉编译，并从已验证旧制品构造等价的不可变二进制覆盖层，独立验证 archive/config/layer/ELF/嵌入迁移。新增 Sub2 PostgreSQL 用例由 GitHub 干净 Docker 环境运行，并在隔离备份恢复库中演练迁移。
