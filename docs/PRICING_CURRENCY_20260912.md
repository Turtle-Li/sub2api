# Pricing basis and internal credit semantics

## Current semantic contract (owner clarification 2026-09-13)

- Wallet balances, quotas, subscription entitlements and usage charges are
  generic internal credit units. They are not customer-held USD or CNY.
- The historical `$` on usage and token-price screens is the uniform reference
  marker. It does not convert the displayed number or define the denomination
  of the wallet that funds the request.
- The numeric basis is `$1` of reference price → 1 internal base unit. An actual
  `¥1` recharge → 1 wallet unit before configured bonuses. Group, user and media
  multipliers then adjust the debit and purchasing power; for example, rate
  0.25 debits 0.25 units for 1 reference unit.
- Billing-side `USD`/`CNY` values in `settlement_currency`, model price-card
  currency and usage currency fields remain implementation markers for
  source-price normalization, the September 12 migration, compatibility checks
  and rollback. They are not separate wallet or subscription currencies and
  must not be used to infer one.
- Actual fiat currency remains at external boundaries. Order receipts,
  payment-provider transactions, refunds and invoices keep their real currency
  and historical amount; authored model prices and upstream provider-cost
  records keep their declared source currency until normalization.
- This clarification changes terminology only. It requires no runtime, schema,
  data, rate or UI migration, and existing balances must not be converted again.

## Confirmed scope

- R1: Existing wallet numbers were mechanically rescaled by 6.75 on 2026-09-12
  as a purchasing-power migration, with matching debit conversion. This was not
  a change in the customer's wallet currency. Do not apply the superseded
  no-conversion CREDIT_PARITY decision or rerun the migration.
- R2: Model cards explicitly record a USD or CNY source-price basis. Existing
  cards/catalog entries default to USD. Group/user/image discount multipliers
  retained their meaning and values for the initial cutover; the later standard
  OpenAI correction below supersedes this for that group type.
- R3: One compatibility settlement basis per installation (`USD` or `CNY`) and
  a fixed USD→CNY normalization rate. These are arithmetic and migration
  controls, not product-domain currencies. No live FX or separate provider-cost
  ledger.
- R4: Publishing additive code/schema alone preserves legacy `USD`-basis
  behavior. Wallet and quota conversion is a separate coordinated cutover,
  never an incidental settings side effect.
- R5: Preserve balances and request purchasing power. Convert internal monetary
  limits and accumulated usage together for wallet-funded API keys and platform
  limits. Subscription entitlements, their API-key quota windows and upstream
  account quotas retain their existing numeric reference basis. Payment orders,
  actual CNY receipts, refunds and invoices are historical fiat facts and must
  not be multiplied.
- R6: New wallet and subscription usage rows retain their configured
  compatibility/source-basis markers. Legacy rows default to `USD`. The later
  owner display correction below restores raw reference totals and the
  historical dollar marker on consumption screens. Remaining rough legacy
  analytics are not acceptance evidence for wallet reconciliation; a
  multi-marker analytics rewrite is outside the owner-requested scope.

## Task dependencies and ownership

| Task | Owner / isolated branch | Depends on | Acceptance / validation |
| --- | --- | --- | --- |
| T1 card storage | schema worker / codex/currency-schema | R1–R4 | source/compatibility markers validated; group JSON and channel SQL roundtrip; additive migration; legacy `USD` basis |
| T2 settings and UI | UI worker / codex/currency-ui-settings | R1–R4 | fixed normalization settings; public basis marker; card selector and serialization; balance formatting; focused tests/typecheck |
| T3 billing | root / codex/configurable-pricing-currency | T1, T2 interfaces | source-card conversion once; token/cache/priority/interval/media/batch behavior; unchanged discounts |
| T4 integration and review | root + independent reviewer | T1–T3 | unit/type/build checks; integration of current production baseline; focused independent review |
| T5 live cutover packet | root | T4 + fresh read-only inventory | exact row manifest, online cache bypass, rehearsal and forward recovery; no automatic historical rewrites |

Workers have separate worktrees and commit owned changes for root integration. Root owns contracts, conflicts and final verification. No upstream vendor repricing, special lunar-model increase or unrelated frontend redesign is included.

## Design

Price cards retain their authored source-price marker. The existing evaluator
uses the catalog's USD reference numbers as its arithmetic base; CNY-authored
card fields are normalized before partial overrides so an omitted field cannot
accidentally mix bases. Completed breakdowns are normalized once to the
configured persisted billing basis. These labels support arithmetic
compatibility and do not introduce customer wallet currencies or a second cost
ledger. Ratios, token thresholds, timestamps and counts are not monetary values
and are never converted.

## Release and rollback boundary

The default compatibility basis is `USD`. Changing that basis requires the
explicit data transaction and all-instance monetary/auth cache refresh. A
settings save does not convert data. Runtime failures retain a last-known-good
basis policy rather than silently changing billing arithmetic.

The owner approved the online procedure in
`deploy/currency-migration/ONLINE_CUTOVER_20260912.md`: keep request admission
accepting, deploy with monetary/auth cache bypass enabled, exclude the expired
database peer, and convert under the canonical maintenance lock. The SQL briefly
serializes monetary writes. A request admitted before the migration may deduct
its old numeric amount after conversion; the owner accepts this temporary
undercharge and no compensating extra debit is made.

After the online transaction, new financial facts continue immediately. Do not
restore a pre-migration database or use the pre-reopen rollback script. Keep the
compatible post-migration code, preserve the monetary manifest, and correct
forward if necessary. Existing unfinished payment/refund work and batch
reservations still fail the transaction's preconditions; public batch admission
is already disabled and remains so.

## Cutover operating constraints

Subscription-only foreground traffic does not exclude registrations, gifts, redemptions, payments, affiliate jobs or admin wallet edits. Verify all SQL preconditions at the transaction boundary. Keep cache bypass enabled until pre-cutover processes and queued writers naturally drain. The disabled platform flusher and empty dirty set are mandatory; never discard unflushed authoritative quota data.

The canonical lifecycle lock is `/run/sub2api-maintenance/sub2api-maintenance.lock` (see `deploy/README.md`); the legacy `/run/lock` path is obsolete. Snapshot and restore-smoke verification use the existing `sub2api-db-backup` and `sub2api-db-restore-smoke` tools on `sub2api-db`. Backups and per-row financial manifests stay on the protected server, outside Git.

## Confirmed retail decision

The owner confirmed future recharges are **1 CNY actually paid → 1 internal
wallet unit** on 2026-09-12. Use `recharge_factor=1`; retain nominal preset
bonuses and their descriptions. Existing wallet numbers were rescaled ×6.75
once during the completed migration. Receipt amounts and purchase eligibility
thresholds remain actual paid CNY. The owner also explicitly prioritizes
uninterrupted API use and accepts temporary undercharging during transition; the
earlier global admission-pause plan is superseded subject to verification of an
online transaction, cache refresh and exclusion of obsolete writers.

## Owner correction: standard OpenAI wallet rate

On September 12 the owner explicitly confirmed that standard OpenAI/Codex groups
keep their original USD reference prices, and their existing multiplier directly
produces internal wallet debits. There is no additional FX multiplication, new
setting, schema or price-card control. With rate 0.25, 1 reference unit consumes
0.25 internal units; an actual ¥500 recharge credits 500 units and therefore buys
2000 reference units before optional recharge bonuses.

The common usage finalization rebases the complete cost breakdown using its
captured compatibility rate before logging and atomic/legacy billing. Wallet,
wallet-key and platform usage persist the `CNY` migration marker; account
quota/statistics retain the `USD` reference marker. All monetary quantities are
generic internal units. HTTP, WebSocket and the generic gateway share the rule.
Existing user-specific and independent media rates retain precedence. Free Fast
retains its existing standard-tier customer charge and priority-tier reference.
CNY-authored overrides are still normalized to the USD reference basis first.

This applies only to explicitly standard OpenAI groups using the `CNY`
compatibility basis.
Subscription groups, including wallet fallback, and other provider platforms
retain their current numeric normalization rules. Catalog prices are not
rewritten. Existing wallet values have already been rescaled and must never be
multiplied again.

Production activation changes group 6 from 0.03 to 0.25 after deploying this
compatible code and naturally draining old binaries. Preserve its before-image,
refresh scoped auth caches and allow the existing group-rate cache to expire.
Do not start an older FX-applying binary against the 0.25 group: restore the old
group rate before such a rollback, or use the new compatible binary. Keep the
API accepting throughout; temporary undercharging is owner-authorized.

## Owner display correction: reference usage labels

The September 12 screenshot clarification explicitly prioritizes existing numeric
usage values over compatibility labels. Consumption tables, their token-price
and cost tooltips, and recent-use cards use the historical `$` marker without
converting any number. For the confirmed Codex example, input/output unit prices
are $5/$30 per million, original cost is $0.102952 and rate .25 yields displayed
$0.025738. The wallet debit remains 0.025738 internal units. Usage dashboards
and their top summaries again show existing raw aggregates as a rough API usage
reference; these historical mixed-period figures are not a fiat USD cost ledger.

This is presentation only: persisted compatibility markers, exports, pricing
cards, billing arithmetic, group multipliers and balance numbers are unchanged.
No data migration, new setting or extra FX calculation is introduced. This
supersedes the initial mixed-currency hiding decision in R6.
