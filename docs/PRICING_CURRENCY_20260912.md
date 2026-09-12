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
| T5 live cutover packet | root | T4 + fresh read-only inventory | exact row manifest, online cache bypass, rehearsal and forward recovery; no automatic historical rewrites |

Workers have separate worktrees and commit owned changes for root integration. Root owns contracts, conflicts and final verification. No upstream vendor repricing, special lunar-model increase or unrelated frontend redesign is included.

## Design

Price cards retain their authored currency. The existing evaluator uses the catalog's USD basis as its arithmetic unit; CNY card fields are normalized before partial overrides so an omitted field cannot accidentally mix units. Completed breakdowns are converted once to the selected settlement currency. This does not introduce a second cost ledger. Ratios, token thresholds, timestamps and counts are not monetary values and are never converted.

## Release and rollback boundary

Default settlement is USD. Changing denomination requires the explicit data transaction and all-instance monetary/auth cache refresh. A settings save does not convert data. Runtime failures retain a last-known-good currency policy rather than reverting CNY billing to USD silently.

The owner approved the online procedure in `deploy/currency-migration/ONLINE_CUTOVER_20260912.md`: keep request admission accepting, deploy with monetary/auth cache bypass enabled, exclude the expired database peer, and convert under the canonical maintenance lock. The SQL briefly serializes monetary writes. A previously admitted USD request may deduct its old numeric amount after conversion; the owner accepts this temporary undercharge and no compensating extra debit is made.

After the online transaction, new financial facts continue immediately. Do not restore an old database or use the pre-reopen rollback script. Keep compatible CNY code, preserve the monetary manifest, and correct forward if necessary. Existing unfinished payment/refund work and batch reservations still fail the transaction's preconditions; public batch admission is already disabled and remains so.

## Cutover operating constraints

Subscription-only foreground traffic does not exclude registrations, gifts, redemptions, payments, affiliate jobs or admin wallet edits. Verify all SQL preconditions at the transaction boundary. Keep cache bypass enabled until pre-cutover processes and queued writers naturally drain. The disabled platform flusher and empty dirty set are mandatory; never discard unflushed authoritative quota data.

The canonical lifecycle lock is `/run/sub2api-maintenance/sub2api-maintenance.lock` (see `deploy/README.md`); the legacy `/run/lock` path is obsolete. Snapshot and restore-smoke verification use the existing `sub2api-db-backup` and `sub2api-db-restore-smoke` tools on `sub2api-db`. Backups and per-row financial manifests stay on the protected server, outside Git.

## Confirmed retail decision

The owner confirmed future recharges are **1 CNY paid → 1 CNY wallet credit** on 2026-09-12. Use `recharge_factor=1`; retain nominal preset bonuses and their descriptions. Existing wallet balances still convert ×6.75. Receipt amounts and purchase eligibility thresholds remain actual paid CNY. The owner also explicitly prioritizes uninterrupted API use and accepts temporary undercharging during transition; the earlier global admission-pause plan is superseded subject to verification of an online transaction, cache refresh and exclusion of obsolete writers.
