# Fixed internal-credit parity — audited decision

Owner decision, 2026-09-10: `1 CNY = 1 internal USD credit`, without live foreign-exchange conversion.
Existing users must retain purchasing power. Order/invoice delivery is the preceding task.
The read-only live audit below selects a zero-change migration for existing balances and effective consumption prices. No production data was rewritten.

## Superseded proposal and current evidence

The previous 2026-08-26 reports under the deployment kit's `report/` directory proposed
`R=6.75`: `sub2api-hybrid-rmb-pricing-plan-r6.75-20260826.md`,
`sub2api-no-code-rmb-credit-migration-plan-20260826.md` and
`sub2api-rmb-credit-migration-review-20260826.md`. Their data inventory and consumer analysis
remain useful; their 6.75 conversions and vendor-RMB override tables are not authorized by the
new decision. The 2026-08-27 custom payment audit stated that migration had not been executed.
Those old observations are not a current production snapshot.

At source `ef6900c2c`, `payment_amounts.go` defaults balance recharge multiplier to 1.
`payment_order.go` keeps actual channel payment (`pay_amount`) distinct from granted credit
(`amount`), and `SubscriptionUSDToCNYRate` controls a separate subscription CNY conversion.
Runtime settings, group/channel overrides and user balances must be inventoried before selecting
any data update. Source defaults alone cannot prove that current production is already at parity.

## Invariants

- The payment channel and invoice keep real charged amount and ISO currency. Never relabel or
  rewrite historical CNY payments, completed refunds, invoice amounts or provider cost evidence.
- Internal credit is a platform accounting unit. It is not redeemable foreign currency or a
  claim that a market USD equals CNY. User-facing credit labels must distinguish upstream USD costs.
- For every supported workload, `available_before / charge_before = available_after / charge_after`,
  within the documented decimal precision. A user must not lose access because only a quota or
  only a price was converted.
- Commercial multipliers, upstream-cost multipliers and exchange factors are different things.
  Apply a denomination change exactly once to a debit path, never to both its base price and multiplier.
- Existing active subscription entitlements, API-key limits, user-platform quotas and reservations
  need the same unit analysis as wallets. Do not rewrite shared `ActualCost` globally: it feeds
  several distinct consumers in `gateway_usage_billing.go`.

## Selecting a concrete migration

1. If live audit proves the old 6.75 migration never occurred, and current wallet debit and credit
   quantities already use the same internal unit, preserve wallet balances and all effective
   consumption prices numerically. Set new CNY purchase parity explicitly; a zero-diff balance
   migration is a valid, auditable outcome. Subscription purchase FX is checked independently.
2. If a known uniform scale `k` was applied to both wallet balances and consumption prices,
   remove it consistently: `new_balance = old_balance / k`, `new_charge = old_charge / k`.
   For example, 67.5 credits at 0.675/request and 10 credits at 0.1/request both buy 100 requests.
   This is only a formula, not authorization to assume `k=6.75` or to use the example on live data.
3. If balances/prices are mixed or different workloads have inconsistent scales, stop the data
   mutation and produce per-cohort reconciliation. One wallet adjustment cannot preserve multiple
   incompatible price ratios. The owner decides any economic change beyond denomination.

## Required inventory and rehearsal

Capture a privacy-safe, versioned before/after manifest: user IDs and decimal wallet/reserved/
recharge values; active subscriptions and remaining quota; group and user-specific multipliers;
group/channel model overrides; image/video/search/audio charges; pending payment/refund and batch
jobs; gift, recharge-code, rebate and promotion paths. Hash the manifest and record the exact
application/config revisions. Never include credentials, customer email, tax identifiers or PDFs.

Rehearse in an isolated restored database with unchanged source price inputs. Compare identical
text/cache/image/batch workloads, quota enforcement, cancellation, settlement and refunds before
and after. The currently mutable LiteLLM price mirror needs a pinned revision before any source
price-catalog edit. Do not import old vendor prices from the historical proposal.

Before live changes, resolve authoritative deployment/Registry records and the current release,
take and verify the project's backup, prevent concurrent balance writers and follow the maintenance
lock/runbook. Use decimal arithmetic, expected-old-value guards and a unique migration ID. Retain
append-only migration evidence and a tested rollback. Re-open writers only after totals, sampled
workloads and dual-node configuration agree.

## Live audit and selected outcome

At `2026-09-10T05:13:38+08:00`, one `REPEATABLE READ READ ONLY` transaction queried
only the shared Sub2 database through its configured `sub2api-db` identity. The serving node's
allowlisted release configuration confirms `Turtle-Li/sub2api`, branch `main`. No credentials,
customer names, email addresses, API-key values or invoice data were selected.

- `BALANCE_RECHARGE_MULTIPLIER = 1.00`; recharge fee `0.00`; no recharge promotion options.
- `SUBSCRIPTION_USD_TO_CNY_RATE` is empty, which current code interprets as numeric direct payment.
- Ordinary checkout remains disabled. There are no subscription plan rows and only two payment
  orders, both `REFUNDED`; this is not authorization to reopen purchases or recreate plans.
- 28 nondeleted users have aggregate balance `268.64765737`; frozen balance is zero. One balance
  is negative. These are an observation, not values to restore over later legitimate consumption.
- All 13 live groups retain their existing commercial rates. Group 6 is `0.03`, group 16 `0.01`,
  Gemini 7 `1.1`, Claude 10 `1`, DeepSeek 17 `0.6`, Kimi 18 `0.8`. In particular the proposed
  `0.2025` / `0.0675` exchange-adjusted rates are absent.
- No group model-price overrides, channel model-price rows or per-user group-rate overrides exist.
  Image price overrides and independent image multipliers do exist and are preserved exactly.
- 25 subscription rows (11 marked active, 14 expired), 47 key quota rows and 136 platform quota
  rows are inventoried. Batch jobs are all in terminal states; historical batch pricing remains intact.

The privacy-minimized local manifest is `evidence/credit-parity/live-audit.json`, SHA-256
`4a5cd0bc7f9345f5055b3e7c2e7a63584ccd2c27f6db44025fde6bae38ff415e`.
It is intentionally excluded from Git because it contains user-linked balances and quota records.
The source of the obsolete 6.75 assumption is the earlier proposal, not current recharge behavior.

**Decision:** retain all existing balances, quota amounts, group multipliers and effective model
prices numerically. The existing CNY recharge path already grants one internal credit per yuan;
subscription purchase conversion is already inactive. Thus `balance_after = balance_before` and
`charge_after = charge_before` for every unchanged workload, preserving purchasing power without
rounding loss. No balance SQL, quota rewrite, price-catalog update, currency relabeling of financial
records, or production setting mutation is required. Existing promotional/commercial rates remain
business rates, not exchange rates. The legacy configurable exchange field is not a live FX feed.

Future pricing changes or purchase activation must use this fixed internal-unit policy rather than
revive the 6.75 proposal. If runtime settings have changed since this audit, repeat the inventory
before acting; never replay these observed balances. Internal `$` displays denote platform credit,
while channel/invoice ISO currencies and upstream provider cost reporting keep their meanings.
