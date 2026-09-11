# Order operations and manual invoice delivery

Task: ORDER-20260910 — feature, admin and customer order lifecycle.
Source baseline: `fork/main@ef6900c2c`; isolated branch `codex/order-fulfillment-invoice`.
Owner instruction: 2026-09-10, explicitly confirmed Sub2 is the target project.

## Confirmed requirements and acceptance

- ORD-01: List rows prioritize purchase title/type, customer (admin only), actual payment amount/currency, payment outcome, entitlement fulfillment, invoice state and time. Short order references remain searchable; full identifiers remain in details and can be copied.
- ORD-02: Payment and fulfillment are distinct. Paid but incomplete orders must remain visible as awaiting/failed delivery; refunds and manual review must not look like unpaid purchases. Status presentation and filters must use the same server facts.
- ORD-03: Show immutable purchase-time product snapshot, including stored price, duration and promised benefits. Current plan edits/deletion must not rewrite old orders. Legacy missing data is explicitly unavailable, never reconstructed from today's plan.
- ORD-04: Customers can list only their own orders, inspect details, request an invoice on eligible completed unrefunded orders and see request/email results. Existing authentication and ownership checks apply even when new checkout is disabled.
- ORD-05: Administrators filter payment, fulfillment and invoice statuses and process a dedicated invoice queue. Reuse manual request -> processing -> issued/rejected lifecycle from the existing implementation at `474138269` with compatibility updates.
- ORD-06: Official invoice issuance occurs outside Sub2. Admin uploads a validated PDF and invoice information; Sub2 stores it privately and emails it to the request recipient. Persist lifecycle independently from email errors, expose delivery failures and retry. Protect against duplicate processing, stale attempts, concurrent refunds and unauthorized reads/writes.
- ORD-07: New/corrected invoice applications notify via the project's existing Feishu configuration, with durable duplicate suppression and failure visibility. Do not send buyer tax identifiers, recipient email or PDF into Feishu.
- ORD-08: Preserve current payment/refund/fulfillment recovery and security boundaries. No real funds or existing balances are changed by this feature.

## Implementation and validation plan

1. Root confirms current contracts and architecture; inspect old invoice source/tests and current fulfillment/snapshot APIs.
2. Adapt invoice backend and additive migration to current schema; maintain current refund step-up and response sanitization. Reuse existing email delivery/template capabilities.
3. Add server-derived lifecycle fields and filters; use current creation-time snapshots and enrich only future order snapshots where useful.
4. Extend existing Vue order list/details/user entry and invoice queue using existing components, bilingual labels and mobile-safe content. Product is the primary list cell; order number is secondary.
5. Integrate existing Feishu sender/config at a separate durable business notification seam after its concurrent delivery change is available. Do not edit the other task's working tree.
6. Verify service/API ownership, lifecycle, upload validation, filters, financial regressions and migration compatibility; frontend tests/typecheck/build and browser desktop/mobile inspection. Independent QA and review follow frozen source.

Only one writer per worktree; independent research/QA may run concurrently. Invoice adaptation may be prepared in a separate worktree, then root integrates. No automatic merge/push/deploy. Rollback disables feature/runtime while retaining additive invoice evidence and private documents.

## Follow-up monetary change

After order work, review the existing currency research for owner-requested `1 CNY = 1 internal USD credit`. Actual channel amounts remain CNY; internal credits are not a foreign-exchange promise. Inventory balances, subscription quotas, group multipliers and model tariffs together before a migration. Historical paid amounts/invoices are immutable. Owner confirmed preserving existing users’ purchasing power. Produce a concrete before/after proposal before any production mutation.

Knowledge candidate: no — this is project-specific delivery work.

## Validation evidence

- Frontend: 36 focused tests cover invoice API calls, request/processing dialogs, shared
  currency display, projection facts, invoice queue and existing refund flow. Typecheck,
  lint and production build passed. Independent synthetic-browser checks cover desktop,
  390 px mobile, long product/reference text, disabled-checkout order history, PDF multipart,
  and delayed responses while switching invoice/detail dialogs. Zero quota renders unlimited.
- `deploy/tests/payment-invoice-migration-test.sh` runs the additive SQL twice against a
  disposable PostgreSQL 18 with no external network. It checks unchanged payment snapshots,
  one request per order, deletion restriction on invoice-linked financial orders, amount/
  status/tax-identifier constraints, one private PDF per invoice and transactional rollback.
  It passed locally. No production migration or real SMTP/Feishu send was performed.
- Currency follow-up: `docs/CREDIT_PARITY_MIGRATION_20260910.md` records the verified existing
  1:1 recharge policy and zero-change decision for balances and effective consumption prices.

- Service integration: actual PostgreSQL verifies an invoice transaction holds its order lock
  against refund start, and a refund committed first prevents issuance after lock acquisition.
  Twelve concurrent delivery claimers produce one winner; a reclaimed claim cannot be overwritten
  by its stale predecessor. Focused payment/invoice/SMTP/notification regression passed.
- Additional regressions cover private PDF MIME roundtrip/header injection, plan edits leaving
  purchase snapshots unchanged, missing optional group lookup, and a standby transition between
  notification claim and transport. Both issued and rejected email tests also switch to standby
  during the SMTP configuration read, proving no connection starts and durable retries remain.
  No outgoing SMTP or Feishu request is made in these tests.

- Independent QA passed the final 56-file backend snapshot (SHA-256 manifest
  `a2a28f0a604f6e967dc507caef2dc81957167db31f8e1570ae72d2aa97fd9324`).
  Final reruns include invoice/snapshot unit behavior, owner/public DTO privacy, audit-body
  omission and disposable PostgreSQL locking/delivery claims. Frontend independent QA and
  Standards/Spec review also passed, including same-order detail close/reopen races.
- Independent backend Standards/Spec review passed the same QA-frozen snapshot. Scope includes
  ownership/privacy, schema and migration contracts, order-lock/refund fences, revision/CAS,
  delivery ownership and recovery, lifecycle wiring, snapshots and server filtering. Generated
  Ent boilerplate was reviewed proportionally; no live delivery or release claim is made.

Implementation status: `IMPLEMENTATION_READY`; local QA and independent review passed.
Human acceptance and production release remain separate.

## 2026-09-11 refund and fulfillment safety follow-up

The order follow-up adds bounded sequential partial refunds and explicit reclaim
evidence. `payment_orders.refund_amount` is the cumulative trusted-success amount;
`payment_orders.refund_requested_amount` is the current in-flight request. A second
request is rejected while an attempt is pending, cumulative refunds are capped at the
paid/credited order amount, and the gateway receives the rounded cumulative delta so
repeated attempts cannot over-refund through independent rounding.

Balance-tier bonuses are automatically reclaimable only when the immutable snapshot
proves that `credited_amount` matches `PaymentOrder.amount`; the normal balance
deduction therefore removes the credited bonus as well. If the available balance is
insufficient, the actual deduction is recorded and the order is held for manual review.
Subscription bonus, reset-card and concurrency entitlements, plus partial subscription
refunds, remain behind a manual entitlement-review fence because this schema has no
safe reversible per-entitlement ledger. Reset-card grants now retain a payment-order
reference for that review; they are not silently deleted during an uncertain refund.

The local follow-up regression covers sequential 40/30/30 partial refunds, stale-plan
race rejection, pending amount immutability, subscription partial-refund rejection,
balance-snapshot bonus validation, fulfillment idempotence/recovery and migration
idempotence. No live payment, refund, invoice issuance or outbound Feishu/SMTP request
is implied by this local evidence; those checks are recorded only after the documented
post-deploy smoke has actually run.
