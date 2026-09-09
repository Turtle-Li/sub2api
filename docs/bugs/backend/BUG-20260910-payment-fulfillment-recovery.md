# BUG-20260910-payment-fulfillment-recovery

Status: ANALYSIS_READY
Baseline: 7f8b274b7d5f4b67d86b701061a30f256ddffff0

现象：现有单笔真实余额发放已通过，但进程中断或发放失败后没有独立的产品侧自动补发扫描。已付款未完成订单依赖另一轮回调或人工重试，不能保证在不再收到回调后恢复。

复现条件：可信付款已将 payment_orders 置为 PAID；在 executeFulfillment 前终止进程，或发放中途失败使状态成为 FAILED/stale RECHARGING。重启后不再注入付款回调。

调用链：toPaid 先提交 PAID/paid_at，然后 executeFulfillment。重复回调的 alreadyProcessed 可恢复；管理员 RetryFulfillment 可恢复。wire.go 启动 PaymentOrderExpiryService，每60秒的 runOnce 只调用 ReconcilePendingPaymentOrders（仅PENDING）及 ExpireTimedOutOrders，未扫描PAID/FAILED/RECHARGING。

影响范围：余额、订阅及关联套餐权益的发放收敛。现有兑换码消费事务、订阅assignment/benefit事务、订单级CAS租约和唯一审计键提供幂等基础，必须复用，不另造一套发放逻辑。

根因：持久订单状态已存在，但未被接入自动恢复消费者。中央outbox重试是独立投递层，不能代替产品本地恢复。机制由实际源调用链直接证明，红测试须模拟回调停止后重启扫描。

推荐修改：以现有支付订单表作为持久待办，给既有60秒leader/background-gated sweep增加有界补发阶段；仅有paid_at、状态PAID/FAILED且至少60秒未处理，或5分钟RECHARGING租约已过期的记录，按updated_at/id公平领取，每轮最多100条、最多4并发。复用原有权益事务、租约CAS、稳定兑换码和订阅审计；一次订单失败不能阻塞其它订单。状态和时间保存在PostgreSQL，重启不丢；持续失败每轮有界重试并可由既有订单失败状态/错误和新增聚合日志观测，不静默丢弃资金任务。

安全排除：未付款、完成、退款相关、人工复核/无法关联退款证据均不能自动发放。使用已有unifiedRefundOrderNeedsReview边界；无法读取该证据必须fail-closed。未付款不能从普通FAILED状态推断为可发放。

附带已确认错误传播缺口：confirmPayment/alreadyProcessed 的数据库读取错误存在返回nil路径，可能让统一Webhook误ACK。只修复数据库错误被吞掉的路径并保留明确NotFound语义，补回归。

风险：新增自动消费者会与正常回调/管理员重试并发，必须以实际PostgreSQL测试验证一次发放；多订单同用户同组必须累加。沿用5分钟租约；请求取消造成的RECHARGING残留会在租约到期后重新扫描。无需新增MQ、schema迁移或改支付通道调用。

验证：现有service基线及PostgreSQL inbox/refund race基线PASS。新增测试覆盖无新回调自动恢复、任务重启、发放事务提交前后故障、同单多消费者/重复回调、不同订单同用户余额累加、同组订阅天数累加、未付款/退款/人工复核排除、取消与重试间隔、公平有界并发。数据均为本地一次性PostgreSQL/Redis与签名fixture，不触发真实资金。

2026-09-10 Developer implementation frozen: payment_fulfillment.go83ef29ce..., payment_order_expiry_service.go7dd2cbe4..., payment_fulfillment_recovery.go8449aa7b..., unit cdd004d3..., PostgreSQL integration509dd348.... Focused unit-race PASS12.680s; actual CI=true PostgreSQL-race recovery suite PASS9.163s. Covers restarted sweep without callback, same-order/different-order balance concurrency, balance after-commit recovery, same-group subscription term convergence, before/after subscription commit failures and100-fenced-row fairness. Existing transaction/redeem/audit idempotency reused; no schema change. Independent QA/review and deployment evidence tracked in operations report.

## Independent review refinement — refund fence and lease race

REVIEW_FAIL P1 on the prior QA-passed snapshot: a signed uncorrelated/contradictory refund writer can lock the order and commit a durable manual-review fence after recovery's nonlocking recheck but before its lease update. The later claim has no fence predicate, so recovery can start after the fence exists. This is a confirmed interleaving, not a failed test assertion or hypothetical load claim. No Sub2 source was published/deployed at this checkpoint.

Approved bounded repair: use an explicit recovery lease-acquisition policy; in a short Ent transaction lock the same payment_orders row used by refund writers, re-read paid/state and refund fence, acquire the existing timestamp-CAS lease and commit before entitlement execution. Preserve normal public balance/subscription callback methods and common doBalance/doSub idempotency. Do not hold the order transaction across network/entitlement work. The lease transaction is the ordering point: a committed review fence must prevent a subsequent automatic claim. No hidden context flag or copied service mutex. Add a deterministic real-PostgreSQL interleaving test, cancel/rollback checks, then new source hashes and fresh QA/review before publication.

Developer repair completed: explicit recovery lease acquirer uses the refund writer’s order lock in a short transaction, then commits before entitlements. Identical real-PG boundary regression fails with historical acquisition (expected recovered 0, got 1), passes with fixed acquisition; cancellation rolls back and a fresh recovery subsequently succeeds. Unit race PASS 10.353s, full four-group PG race PASS 9.059s, final boundary race PASS 7.052s. Final SHA manifest is in the operations record; fresh independent gates pending.

Final independent REVIEW_PASS: all five frozen hashes match, original P1 closed; no new actionable P0/P1/P2. The reviewer confirmed shared row-lock/fence/CAS transaction, commit before entitlement side effects, ordinary callback/admin policy compatibility and deterministic PostgreSQL boundary/rollback coverage. Source is cleared for the authorized release; deployment results are recorded separately after execution.

Deployed after independent QA_PASS/REVIEW_PASS: runtime c82e5df699575a7b4f2faa76253b7d96208ab2a7 on both production nodes; identical image, healthy, one active background owner. Final production checks retain two REFUNDED pilot orders with exactly one recharge/refund audit each and payment_enabled=false. No production fault injection; local PG matrix is the recovery/concurrency evidence.
