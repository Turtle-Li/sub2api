# Configurable pricing currency

## Confirmed scope

- R1: Existing wallet amounts are USD; the owner confirmed conversion to CNY at 6.75 on 2026-09-12. Do not apply the superseded no-conversion CREDIT_PARITY decision.
- R2: Model cards explicitly select USD or CNY. Existing cards/catalog entries default to USD. Group/user/image discount multipliers retain their meaning and values.
- R3: One settlement currency per installation (USD or CNY); a fixed USD→CNY rate. No live FX or separate provider-cost ledger.
- R4: Publishing additive code/schema alone preserves legacy USD behavior. Wallet and quota conversion is a separate coordinated cutover, never an incidental settings side effect.
- R5: Preserve balances/request purchasing power. Convert monetary limits and accumulated usage together, for wallet-funded API keys and platform limits. Subscription entitlements, their API-key quota windows, and upstream account quotas retain USD. Payment orders, actual CNY receipts, refunds and invoices are historical facts and must not be multiplied.
- R6: New wallet consumption is recorded in settlement units; subscription consumption remains USD. Every new usage row records its currency; legacy rows default to USD. Primary wallet/usage dashboards hide mixed-period monetary totals and show row-level currencies. Remaining rough legacy analytics are not acceptance evidence for wallet reconciliation; a full multi-currency analytics overhaul is outside the owner-requested scope.

## Task dependencies and ownership

| Task | Owner / isolated branch | Depends on | Acceptance / validation |
| --- | --- | --- | --- |
| T1 card storage | schema worker / codex/currency-schema | R1–R4 | USD/CNY validated; group JSON and channel SQL roundtrip; additive migration; legacy USD |
| T2 settings and UI | UI worker / codex/currency-ui-settings | R1–R4 | fixed FX settings; public denomination; card selector and serialization; wallet currency; focused tests/typecheck |
| T3 billing | root / codex/configurable-pricing-currency | T1, T2 interfaces | source-card conversion once; token/cache/priority/interval/media/batch behavior; unchanged discounts |
| T4 integration and review | root + independent reviewer | T1–T3 | unit/type/build checks; integration of current production baseline; focused independent review |
| T5 live cutover packet | root | T4 + fresh read-only inventory | exact row manifest, coordinated pause/cache handling, rehearsal, rollback; no blind SQL or automatic historical rewrites |

Workers have separate worktrees and commit owned changes for root integration. Root owns contracts, conflicts and final verification. No upstream vendor repricing, special lunar-model increase or unrelated frontend redesign is included.

## Design

Price cards retain their authored currency. The existing evaluator uses the catalog's USD basis as its arithmetic unit; CNY card fields are normalized before partial overrides so an omitted field cannot accidentally mix units. Completed breakdowns are converted once to the selected settlement currency. This does not introduce a second cost ledger. Ratios, token thresholds, timestamps and counts are not monetary values and are never converted.

## Release and rollback boundary

Default settlement is USD. Changing denomination requires the coordinated data cutover and all-instance cache refresh while debit/credit/background writers are fenced. A settings save does not convert data. Existing in-flight batch snapshots and payment/refund work must be drained or explicitly reconciled first. Runtime failures retain a last-known-good currency policy rather than reverting CNY billing to USD silently.

Rollback before reopening traffic restores the exact saved monetary/configuration values. After accepting new writes, do not restore an old database over new financial facts; reconcile new writes or use a forward correction. The feature remains implementation-only until validation and a concrete live cutover packet pass.

## Cutover operating constraints

The read-only production audit found only subscription traffic in the last hour, no frozen balances, no active batch jobs, and no unfinished balance orders. These are preflight observations, not a fence: registrations, first-bind gifts, redeem/promo codes, affiliate jobs, payment fulfillment and admin edits also write wallets. Recheck under a writer fence immediately before applying the transaction. Prefer a short coordinated admission pause with in-flight requests drained over adding a new permanent wallet-maintenance subsystem solely for this migration. Never mutate an old in-flight USD debit against a converted CNY wallet.

The canonical lifecycle lock is `/run/sub2api-maintenance/sub2api-maintenance.lock` (see `deploy/README.md`); the legacy `/run/lock` path is obsolete. Snapshot and restore-smoke verification use the existing `sub2api-db-backup` and `sub2api-db-restore-smoke` tools on `sub2api-db`. Backups and per-row financial manifests stay on the protected server, outside Git.

## Confirmed retail decision

The owner confirmed future recharges are **1 CNY paid → 1 CNY wallet credit** on 2026-09-12. Use `recharge_factor=1`; retain nominal preset bonuses and their descriptions. Existing wallet balances still convert ×6.75. Receipt amounts and purchase eligibility thresholds remain actual paid CNY. The owner also explicitly prioritizes uninterrupted API use and accepts temporary undercharging during transition; the earlier global admission-pause plan is superseded subject to verification of an online transaction, cache refresh and exclusion of obsolete writers.
