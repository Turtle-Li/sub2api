# 支付回调与履约 QA 报告

日期：2026-08-06
QA 级别：Level 3（支付资金/权益一致性、数据库事务、回调并发和超时状态机）
测试方式：本地 SQLite Ent 测试数据库 + 本地测试用户 + 伪造但可验证的支付宝/微信回调；没有连接真实商户、没有产生外部支付交易。

## 验收结论

支付履约核心 P0 场景通过：同一订单重复或并发回调只会完成一次；超时未付款订单会关闭上游交易并变为 `EXPIRED`；宽限期内确实已付款的晚到回调会恢复并履约；宽限期外回调不会复活或入账；订阅套餐快照中的赠送余额和重置次数在恢复执行时只发放一次。

当前不能给出“可直接接入真实支付宝/微信生产商户”的最终放行结论：完整 `unit` 包仍被两处与支付无关的工作区测试漂移阻断，并且 SQLite 不能替代上线前 PostgreSQL 并发/迁移门禁。详见“阻断与后续门禁”。

## P0 用例与证据

| 用例 | 结果 | 测试证据 |
| --- | --- | --- |
| 支付宝 RSA2 回调验签、金额/订单号解析 | PASS | `TestVerifyNotificationAcceptsLocallySignedCallbackAndRejectsTampering` |
| 微信 API v3 RSA header 验签 + AES-256-GCM 解密 | PASS | `TestVerifyNotificationAcceptsLocallySignedAndEncryptedCallback` |
| HTTP 回调入口与支付宝/微信 ACK 格式 | PASS | `TestSimulatedAlipayAndWxpayCallbacksReachFulfillmentService` |
| 6 个并发重复支付宝回调，余额恰好入账一次 | PASS | `TestConcurrentDuplicateAlipayNotificationsCreditLocalUserExactlyOnce`；另有 `-race` PASS |
| 付款超时、上游未支付、关闭订单 | PASS | `TestExpiryAndLatePaymentCallbacksHaveNoLossOrDoubleCredit` |
| 超时宽限期内晚到支付回调恢复并履约 | PASS | 同上，校验 `ORDER_RECOVERED` 且兑换码只使用一次 |
| 超过宽限期的支付回调不复活、不入账 | PASS | 同上 |
| 套餐赠送余额 + 重置次数原子发放与恢复幂等 | PASS | `TestSubscriptionSnapshotBenefitsAreGrantedExactlyOnceAfterRecovery` |
| 金额不匹配不改变订单状态、不触发履约 | PASS | 既有 `TestPaymentNotificationRejectsAmountMismatchBeforeFulfillment` |

本地测试账号由测试数据库按用例生成，订单号均为 `sub2_local_*`，不会写入真实业务数据库。

## 执行命令

已通过：

```text
go test ./...
go test -tags=unit ./internal/payment/provider -count=1
go test -tags=unit ./internal/payment/provider -run TestVerifyNotificationAcceptsLocallySignedAndEncryptedCallback -count=1
service_src=$(rg --files | rg '^internal/service/[^/]+\.go$' | rg -v '_test\.go$')
service_test_base='internal/service/payment_fulfillment_test.go internal/service/payment_config_service_test.go internal/service/user_service_test.go internal/service/subscription_assign_idempotency_test.go internal/service/subscription_calculate_progress_test.go internal/service/payment_order_lifecycle_test.go internal/service/idempotency_test.go'
go test -race -tags=unit $service_src $service_test_base -run TestConcurrentDuplicateAlipayNotificationsCreditLocalUserExactlyOnce -count=1
go test -tags=unit $service_src $service_test_base -run 'Test(ConcurrentDuplicateAlipayNotificationsCreditLocalUserExactlyOnce|ExpiryAndLatePaymentCallbacksHaveNoLossOrDoubleCredit|SubscriptionSnapshotBenefitsAreGrantedExactlyOnceAfterRecovery|ExecuteSubscriptionFulfillmentRecoversCommittedAssignmentWithoutExtendingAgain)$' -count=1
handler_src=$(rg --files | rg '^internal/handler/[^/]+\.go$' | rg -v '_test\.go$')
go test -tags=unit $handler_src internal/handler/payment_webhook_handler_test.go -count=1
```

最后一次支付 service 定向执行结果：`ok`；回调 provider 全量 unit 结果：`ok`；回调 handler 定向结果：`ok`；不带 `unit` 标签的后端全量结果：`ok`。

## 阻断与后续门禁

`go test -tags=unit ./...` 当前不能启动，编译在支付测试之前被工作区已有的非支付测试阻断：

1. `internal/service/desktop_app_download_sources_test.go` 引用了当前不存在的 `SettingKeyDesktopAppDownloadSources` 和 `PublicSettings.DesktopAppDownloadSources`。
2. `internal/handler/openai_responses_failover_cancel_test.go` 使用了旧版 `NewOpenAIGatewayService` 和 `NewOpenAIGatewayHandler` 参数列表。

这两处没有为支付测试绕过或隐藏；修复后必须重新跑完整 `go test -tags=unit ./...`。

上线前仍需增加/执行：

- PostgreSQL 真迁移环境下的两 worker 同订单并发履约、唯一审计索引和 `subscription_reset_grants` 外键/约束测试。
- 支付宝/微信官方沙箱或商户测试号的真实 HTTP 回调回放（含重试、乱序、重复通知和签名密钥轮换）。
- 过期扫描任务与回调同时发生的压力测试，以及 worker 在“兑换成功/权益事务提交前后”退出的恢复测试。
- 实际邮件发送队列的去重和失败重试验收。

## 可复现源快照

基线提交：`1720aaecce42c86070cbb622893d285b243aeb64`（工作区含未提交支付改动）。关键文件 SHA-256：

```text
backend/internal/service/payment_fulfillment.go       db252b948690929542cd3a38cc158942057cc5c74ff2b7611552e2e1c414569a
backend/internal/service/payment_fulfillment_test.go  e8aa0f6d599f75b19b56548768cd7da13d961b2e5f9d2a1a20d793793bb59478
backend/internal/handler/payment_webhook_handler.go   416cfae490de2e72e52d2e1ac1a84aa4712181d32d83b5693267c0b7a4ffa9c1
backend/internal/handler/payment_webhook_handler_test.go f3b0b624650124065273fd28b46b0e1e197d750c383b86ff6ebabe8e418e2d5f
backend/internal/payment/provider/alipay_test.go      d5b7a5d97745e135394a6eef036efeac1be64d4a9abb8eaac07a0a70356753a0
backend/internal/payment/provider/wxpay_test.go      c73079afc136cb6d70c81d64ff5f9540ebe1407ec068a7dc5e312ee1bdf3fcce
backend/migrations/194_payment_product_entitlements.sql 944ad10c1db002d3a5a57bd05b5b3515fa8d4cb4fe826d2f51ed801ca9e6ccbc
```
