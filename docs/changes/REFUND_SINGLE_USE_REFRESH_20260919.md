# One successful refund and targeted status refresh

Baseline: fork/main `8d03649bce62868ab7d3ac1a9cf3fa19496da1a5`.

## Bug analysis — ANALYSIS_READY

Bug: refund result stays stale; partial refunds retain a new-refund action.
现象: owner observed Alipay success only after manually refreshing; partial refund still offers refund.
复现条件: admin refund returns pending before asynchronous reconciliation; order becomes PARTIALLY_REFUNDED.
调用链: AdminOrdersView.handleRefund → refundOrder → one loadOrders; canOpenRefundReview and user canRequestRefund both admit PARTIALLY_REFUNDED.
影响范围: admin row/detail actions, user request entry, server new-refund admission.
疑似根因: directly evidenced missing follow-up reads and explicit repeat-partial-refund policy. No live-provider failure is inferred.
推荐修改方案: bounded, read-only refresh of operated orders; reject new refunds after any settled success while keeping same-attempt recovery/idempotency.
风险: stale reads after navigation, overlapping polls, confusing legacy requested amount with settled amount, interrupting an existing financial attempt.

The pinned upstream `bdb42e22f81fcb633ff0a060961211dd2bcb515b` admin orders view explicitly permits PARTIALLY_REFUNDED and uses one list reload; this is an intentional local product-policy divergence. Its refund spec path does not exist. Preserve LGPL-3.0 obligations. Existing gateway protocol, signatures and settlement evidence remain unchanged.

## Implementation plan — PLAN_READY

- AC1: each order permits at most one successful refund, including partial amounts; failed attempts may be retried, pending attempts recovered without a new cash refund.
- AC2: only records explicitly operated on are refreshed, through local order reads; never periodically list orders or query the provider.
- AC3: stop on terminal/manual/paused state, navigation/unmount, or five-minute deadline; pause while hidden, back off and never overlap requests for a row.
- T1 (root): frontend action predicates and scoped refresh, fake-timer/API-count tests. No dependency on T2.
- T2 (root, after backend analysis): authoritative service/store admission guards and regression tests. Preserve settlement and history.
- T3 (root): contract/docs synchronization and focused frontend/backend validation; depends on T1/T2.
- T4 (independent QA/review): frozen diff inspection and acceptance tests, depends on T3. Production release is outside this change.

Rollback: revert application changes; no migrations or financial data rewrites. Risk: application rollback restores the earlier repeat-partial-refund policy.
