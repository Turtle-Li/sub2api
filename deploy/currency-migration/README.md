# USD wallet → CNY cutover (2026-09-12)

This is an explicit operator migration, not an application startup migration.
Approved rate: **6.75**. Default code rollout remains USD until the transaction.

## Required gates

1. Review the exact application commit with Claude, run backend unit tests and frontend checks, and deploy the same reviewed image to both production application nodes with existing role-preserving release receivers. Keep the default USD policy during rollout.
2. Complete the existing database backup and isolated restore smoke. The first successful rehearsal backup is `sub2api-db-backup-20260912-033913.tar.gz`: outer archive, PostgreSQL dump and Redis checksums passed; isolated PostgreSQL schema verification and Redis loading passed. Take a fresh backup under the final cutover fence too.
3. Acquire the documented maintenance lock on **both** application nodes. Pause admissions through the documented Caddy transaction mechanism; let in-flight requests finish, and drain billing/cache/background jobs. Stop all application writers under their lifecycle lock before changing monetary state. Subscription entitlements are unchanged, but a brief admission pause is simpler and safer than an incomplete wallet-only fence.
4. Recheck no frozen wallets, unfinished image batches, unfinished payment/refund fulfillment, refundable legacy balance orders, or frozen affiliate entries. Abort for any unexpected state. Do not attempt to repair such state with generic multiplication.
5. Rehearse the SQL with rollback on an isolated restored database. Compare each converted field to `round(old_value * 6.75, 8)`, verify all discount multipliers and subscription quotas unchanged. Keep the per-row before/after manifest in the protected database and backup, never in Git.
6. Apply a single transaction that snapshots only selected monetary fields, converts wallet data, sets the CNY policy and records a unique migration marker. A repeat must fail. Purge only the documented wallet/auth/key/platform-quota cache namespaces while writers are stopped; do not `FLUSHALL` shared Redis.
7. Start the reviewed application generation with fresh CNY policy caches. Run an isolated wallet debit and subscription debit canary, confirm corresponding usage row currency and before/after balance/quota. Remove only the dedicated test fixtures after preserving evidence. Reopen admissions after both pass.

## Conversion scope

- Users: `balance`, `frozen_balance`, `total_recharged`; fixed balance notification thresholds only.
- Wallet API keys: quota limits, accumulated quota, 5h/1d/7d rate limits and usage. Subscription-bound keys remain USD.
- `user_platform_quotas`: wallet-only monetary limits and usage, together. Field suffix `_usd` is retained for compatibility.
- Unused balance redeem codes and promo bonus configuration; exclude historical used records and non-money code types.
- Affiliate current available/frozen/history totals; require no outstanding frozen ledger entries. Historical ledger amounts retain their original unit.
- Existing registration/first-bind/default balance settings and fixed notification defaults. Dimensionless recharge/discount multipliers are not converted.

Do not change payment order amounts, actual receipts/refunds/invoices, subscription plan prices, subscription entitlement limits/usage, upstream account quotas, historical usage rows, completed batch snapshots, or group/user/model multipliers.

## Recovery

Before accepting any new writes, restore exact snapshotted monetary/settings values and matching cache state while still fenced. After accepting CNY writes, never restore an old full database over new requests/payments; use a forward correction or reconcile the new writes first. Keep the tested backup, image digest, transaction identifier and smoke evidence together in protected operational storage.
