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

## Frontend verification

- Red reproduction: targeted-refresh assertion and four settled-order action assertions failed on the original view; original tests otherwise remained green.
- Final admin suite: 35 passed. Affected user/detail/component/API/dialog tests: 24 passed. Locale completeness: 3 passed. Typecheck and scoped ESLint passed.
- Real Chrome, actual Vue admin view with isolated synthetic local API: operated order #42 changed from pending/reclaiming to partially refunded/reclaimed automatically; #43 stayed completed/refundable; pre-existing partial #44 and updated #42 offered only View. Detail #42 had no new refund action. No live account or provider action was performed. Temporary fixture and server were removed afterwards.
- Independent frontend QA on `8d03649bc..626cb727f`: QA PASS, no remaining P1/P2. One discovered stale-quote 409 issue was corrected with a targeted refresh and regression test.

Targeted updates keep the operated row in its current position and retain the
current list/pagination snapshot, even if the new status no longer matches the
selected filter. An explicit list reload reapplies filters and counts. This lets
the operator see the outcome without fetching/reordering other orders. If order
operations later require continuously accurate filtered counts, the order-page
owner should add a scoped server projection rather than global polling.

## Backend verification

- Red regressions established that a second reviewed/legacy partial refund was previously admitted and a stale legacy plan did not report the single-success refusal.
- New policy cases passed: user request rejection, legacy in-flight amount compatibility, unified second-attempt denial, legacy second-refund denial, stale-plan rejection and selected-subscription success/failure lifecycle. Existing query, lost-response recovery, accounting and backfill checks also passed.
- Integrated race run: `go test -race -tags=unit ./internal/service -run 'Test.*(Refund|SelectedSubscription)' -count=1` passed in 84.717 seconds. This is service/SQLite test evidence, not a new live-provider or PostgreSQL deployment acceptance.
- Imported backend source is byte-identical to worker `55c1e9803`, integrated as `d9c3a0a15`. No settlement callbacks, financial migrations or production state were changed.
- Independent backend QA/review on `8d03649bc..d9c3a0a15`: PASS, no actionable P1/P2 in the change. The reviewer separately identified a pre-existing pre-240 legacy-field finalization limitation (old requested amount can be counted twice after PENDING→REFUNDING). The normalization migration and those finalization functions are unchanged; this patch preserves recovery reachability and does not claim to repair that baseline path. Migration 240 already normalizes that old row encoding; no live legacy-row prevalence was inspected in this task.

## Outcome

`IMPLEMENTATION_READY`: frontend and backend scoped independent checks passed;
local source is ready for owner review. Not pushed, merged or deployed.

knowledge_candidate: no — project-specific refund policy; no human-accepted
cross-project pattern is being promoted from this implementation task.
