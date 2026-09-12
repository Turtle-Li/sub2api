# Configurable pricing currency

## Confirmed scope

- R1: Existing wallet amounts are USD; the owner confirmed conversion to CNY at 6.75 on 2026-09-12. Do not apply the superseded no-conversion CREDIT_PARITY decision.
- R2: Model cards explicitly select USD or CNY. Existing cards/catalog entries default to USD. Group/user/image discount multipliers retained their meaning and values for the initial cutover; the later standard OpenAI correction below supersedes this for that group type.
- R3: One settlement currency per installation (USD or CNY); a fixed USD→CNY rate. No live FX or separate provider-cost ledger.
- R4: Publishing additive code/schema alone preserves legacy USD behavior. Wallet and quota conversion is a separate coordinated cutover, never an incidental settings side effect.
- R5: Preserve balances/request purchasing power. Convert monetary limits and accumulated usage together, for wallet-funded API keys and platform limits. Subscription entitlements, their API-key quota windows, and upstream account quotas retain USD. Payment orders, actual CNY receipts, refunds and invoices are historical facts and must not be multiplied.
- R6: New wallet consumption is recorded in settlement units; subscription consumption remains USD. Every new usage row records its currency; legacy rows default to USD. The later owner display correction below restores raw reference totals and the historical dollar marker on consumption screens. Remaining rough legacy analytics are not acceptance evidence for wallet reconciliation; a full multi-currency analytics overhaul is outside the owner-requested scope.

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

## Owner correction: standard OpenAI wallet rate

On September 12 the owner explicitly confirmed that standard OpenAI/Codex groups
keep their original USD reference prices, and their existing multiplier directly
produces CNY wallet debits. There is no additional FX multiplication, new setting,
schema or price-card control. With rate 0.25, reference $1 consumes ¥0.25; ¥500
therefore buys $2000 of reference usage (before optional recharge bonuses).

The common usage finalization rebases the complete cost breakdown using its
captured settlement rate before logging and atomic/legacy billing. Wallet,
wallet-key and platform usage use CNY; account quota/statistics retain the USD
reference basis. HTTP, WebSocket and the generic gateway share the same rule.
Existing user-specific and independent media rates retain precedence. Free Fast
retains its existing standard-tier customer charge and priority-tier reference.
CNY-authored overrides are still normalized to the USD reference basis first.

This applies only to explicitly standard OpenAI groups with CNY settlement.
Subscription groups, including wallet fallback, and other provider platforms
retain their current currency rules. Catalog prices are not rewritten. Existing
wallets have already been converted and must never be multiplied again.

Production activation changes group 6 from 0.03 to 0.25 after deploying this
compatible code and naturally draining old binaries. Preserve its before-image,
refresh scoped auth caches and allow the existing group-rate cache to expire.
Do not start an older FX-applying binary against the 0.25 group: restore the old
group rate before such a rollback, or use the new compatible binary. Keep the
API accepting throughout; temporary undercharging is owner-authorized.

## Owner display correction: reference usage labels

The September 12 screenshot clarification explicitly prioritizes existing numeric
usage values over settlement-unit labels. Consumption tables, their token-price
and cost tooltips, and recent-use cards use the historical `$` marker without
converting any number. For the confirmed Codex example, input/output unit prices
are $5/$30 per million, original cost is $0.102952 and rate .25 yields displayed
$0.025738. The wallet debit remains 0.025738 CNY. Usage dashboards and their top
summaries again show existing raw aggregates as a rough API usage reference;
these historical mixed-period figures are not a unified USD cost ledger.

This is presentation only: persisted row currency, exports, pricing cards,
settlement, group multipliers and CNY balances are unchanged. No data migration,
new setting or extra FX calculation is introduced. This supersedes the initial
mixed-currency hiding decision in R6.
