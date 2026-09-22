# 本地取消与迟到付款退款发布 — 2026-09-22

用户已明确授权：测试没问题即发布线上。两端源码 QA/Review 均通过，详细源快照和测试见 `PAYMENT_LOCAL_CANCELLATION_20260922.md`。

## 发布范围与协调

- Sub2：`Turtle-Li/sub2api` 的 main；唯一当前应用目标 `sub2api-candidate:/opt/sub2api`。使用既有 GitHub workflow concurrency `sub2api-production-deploy` 序列化构建，既有 root-owned 上传锁与 `/run/sub2api-maintenance/sub2api-maintenance.lock` 协调生产操作；不另起并行发布。
- 中央：Registry 的 totools-pay 专用 SSH 身份与 `/opt/totools-pay-live`；仅该 Compose 项目及其既有发布锁。沿既有受控制品、加密备份、迁移及恢复流程执行。
- 先中央 23+24，再 Sub2 256。保留金融记录和旧制品；旧金融写入者排空后再暴露新取消行为。不得强行终止用户长连接。
- 保留 Vault 内存代理；使用现有注入，不输出或复制秘密。无真实客户支付/退款或伪造生产回调。

## 当前状态

发布准备中。部署、备份、迁移和线上 Smoke 的实际证据将追加，不以本地测试冒充线上验收。
