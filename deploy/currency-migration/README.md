# USD wallet → CNY cutover (2026-09-12)

This is an explicit operator migration, not an application startup migration.
Approved rate: **6.75**. Default code rollout remains USD until the transaction.

**Current owner-approved execution:** future recharge is **1 CNY paid → 1 CNY credited**, so use `recharge_factor=1`. On September 12 the owner explicitly prioritized uninterrupted API use and accepted temporary undercharging. Follow [the online execution packet](ONLINE_CUTOVER_20260912.md) for this run; the stopped-writer steps below are the earlier alternative, not the current execution authorization. Never activate the HTTP admission fence for this online run.

## Required gates

1. Review the exact application commit with Claude, run backend unit tests and frontend checks, and deploy the same reviewed image to both production application nodes with existing role-preserving release receivers. Keep the default USD policy during rollout.
2. Complete the existing database backup and isolated restore smoke. The first successful rehearsal backup is `sub2api-db-backup-20260912-033913.tar.gz`: outer archive, PostgreSQL dump and Redis checksums passed; isolated PostgreSQL schema verification and Redis loading passed. Take a fresh backup under the final cutover fence too.
3. Acquire the documented maintenance lock on **both** application nodes. Set the existing operator-owned traffic-state file to `draining` on each node (preserve file inode because it is bind-mounted); the admission middleware returns HTTP 503 with Retry-After. Poll authenticated `/internal/livez` until `in_flight_requests=0`; let in-flight requests finish, and drain billing/cache/background jobs. Stop all application writers under their lifecycle lock before changing monetary state. Subscription entitlements are unchanged, but a brief admission pause is simpler and safer than an incomplete wallet-only fence.
4. Recheck no frozen wallets, unfinished image batches, unfinished payment/refund fulfillment, refundable legacy balance orders, or frozen affiliate entries. Abort for any unexpected state. Do not attempt to repair such state with generic multiplication.
5. Rehearse the SQL with rollback on an isolated restored database. Compare each converted field to `round(old_value * 6.75, 8)`, verify all discount multipliers and subscription quotas unchanged. Keep the per-row before/after manifest in the protected database and backup, never in Git.
6. Apply a single transaction that snapshots only selected monetary fields, converts wallet data, sets the CNY policy and records a unique migration marker. A repeat must fail. Purge only the documented wallet/auth/key/platform-quota cache namespaces while writers are stopped; do not `FLUSHALL` shared Redis.
7. Start the reviewed application generation with fresh CNY policy caches while traffic-state remains draining. Restore the recorded background-owner role; run canaries over the actual loopback socket inside the application container with the existing monitor token plus normal API credentials. This permits testing while public traffic remains fenced; forwarded headers never qualify. Restore accepting state only after canaries pass. Run an isolated wallet debit and subscription debit canary, confirm corresponding usage row currency and before/after balance/quota. Remove only the dedicated test fixtures after preserving evidence. Reopen admissions after both pass.

## Conversion scope

- Users: `balance`, `frozen_balance`, `total_recharged`; fixed balance notification thresholds only.
- Wallet API keys: quota limits, accumulated quota, 5h/1d/7d rate limits and usage. Subscription-bound keys remain USD.
- `user_platform_quotas`: wallet-only monetary limits and usage, together. Field suffix `_usd` is retained for compatibility.
- Unused balance redeem codes and promo bonus configuration; exclude historical used records and non-money code types.
- Affiliate current available/frozen/history totals; require no outstanding frozen ledger entries. Historical ledger amounts retain their original unit.
- Existing registration/first-bind/default balance settings and fixed notification defaults. Group/model discount multipliers are not converted. `BALANCE_RECHARGE_MULTIPLIER` is wallet credits per paid CNY, not a discount: its conversion and preset bonuses depend on the pending owner decision for new recharges (`recharge_factor=6.75` preserves old retail purchasing power; `1` retains CNY nominal retail amounts). The script refuses an unspecified factor. Update any preset descriptions that embed old bonus numbers after reviewing the exact catalog. Rehearsals use 6.75; this is not approval for production retail policy.

Do not change payment order amounts, actual receipts/refunds/invoices, subscription plan prices, subscription entitlement limits/usage, upstream account quotas, historical usage rows, completed batch snapshots, or group/user/model multipliers.

## Recovery

Before accepting any new writes, restore exact snapshotted monetary/settings values and matching cache state while still fenced. After accepting CNY writes, never restore an old full database over new requests/payments; use a forward correction or reconcile the new writes first. Keep the tested backup, image digest, transaction identifier and smoke evidence together in protected operational storage.

The admission fence reuses `SUB2API_TRAFFIC_STATE_FILE`; no new wallet-maintenance setting or Caddy transaction is needed. It does not stop background work or admitted WebSocket sessions. A nonzero in-flight count is a stop condition: wait for completion, or explicitly plan any required session interruption before proceeding. Both nodes must retain their maintenance lock until the approved generation and traffic/background state are restored.

Before stopping the final background owner, drain the platform-quota flusher and assert Redis `SCARD billing:upq:dirty = 0`. After all writers stop, assert it again. Never discard nonempty dirty snapshots: flush/reconcile them first. Under the fence, clear `billing:upq:dirty` together with `billing:user_platform_quota:*` after SQL, so stale USD snapshots cannot overwrite CNY usage. Test a platform-quota debit and DB/cache parity before reopening.

## Execution details to complete before cutover

- Hold each node's local maintenance lock for the entire monetary cutover; two successful local locks are required. Deploy through the lock-owning release receiver **before** entering this lock-held phase to avoid nested lock contention. Record the current active container and background role on each node rather than assuming a color.
- The allowed Redis cleanup namespaces, with all application writers stopped, are `billing:balance:*`, `apikey:auth:*`, `apikey:rate:*`, `billing:user_platform_quota:*`, and the already-empty `billing:upq:dirty`. Use bounded `SCAN` and `UNLINK`, without printing key contents. Preserve `billing:sub:*`, `apikey:ratelimit:*`, and request-count caches. Auth snapshots must be invalidated because they contain wallet/key state.
- The existing real-request release probe targets the container bridge IP and does not send the monitor header. It cannot serve as the fenced canary unchanged. Prepare a wrapper executing inside the selected container against actual `127.0.0.1`, with both normal API credentials and the existing monitor token injected from protected files. Do not print secrets, use shell tracing, or put them in evidence.
- A zero dirty set before stopping does not replace checking again after stop. There is no verified standalone synchronous flusher command in this packet. If dirty snapshots remain, halt and reconcile/flush them under the USD policy; never delete them to pass the gate.
- Provision dedicated wallet and subscription fixtures before final execution. A wallet fixture with its own platform quotas must produce a nonzero CNY usage row whose `actual_cost` matches the wallet decrease and applicable key/platform usage increase. A subscription fixture must produce a nonzero USD usage row, increase subscription/key usage, and leave its wallet unchanged. Verify DB/cache parity after asynchronous flushing. Do not use real payment orders or modify an ordinary subscriber to manufacture a test fixture.
- Fixture deletion must not erase usage/audit evidence. Disable/delete only dedicated access objects through supported APIs after recording their IDs and results; retain financial and usage records. Canary writes must be reconciled before using the pre-reopen rollback script, and an old full backup must never overwrite them blindly.
