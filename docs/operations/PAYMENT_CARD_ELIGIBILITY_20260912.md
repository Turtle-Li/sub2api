# Configurable payment cards and purchase eligibility

Task PAY-ELIG-20260912, feature extension to payment catalog refinement. Source: owner's September 12 request. Existing UI/reset refinement is locally frozen at b2b62fe67 and not yet published.

## Requirements

- ELIG-01: Admin can edit recharge card label, description, amount, bonus, recommendation and enabled state, and subscription card existing content/entitlements. Provide a visual editor; preserve advanced/unknown settings during unrelated edits and reject malformed settings rather than silently erasing them.
- ELIG-02: Each card can restrict visibility to explicitly selected user IDs. Empty list means all users. Unauthorized users must not receive hidden products or the private audience list from customer checkout APIs, and direct purchase must reject.
- ELIG-03: Each card can set a minimum cumulative balance-recharge threshold. Authorized viewers below the threshold see the card disabled and the required/current amount. No new higher tier is activated by this change; current six tiers remain unrestricted until admin configuration changes.
- ELIG-04: Checkout API derives eligibility using the authenticated user. Order creation and reset quote/purchase recheck current rules before external order creation/wallet debit. Frontend flags are informational and never authority. Preserve old successful order/grant/replay/fulfillment evidence.
- ELIG-05: All default/absent rules preserve current availability. Admin validation rejects negative/nonfinite thresholds and invalid user IDs. Hidden audience IDs are never included in public purchase snapshots or customer card data.
- ELIG-06: Reset card price and display content can be configured per monthly plan, with separate optional reset-card purchase rules. Existing price override remains authoritative; no subscription duration or usage-reset behavior change.

Confirmed decision: threshold accounting is delivered CNY balance-order pay_amount minus the gateway projection of confirmed refund_amount, excluding bonuses, subscriptions, pending/failed orders, reset wallet purchases and foreign currencies without verified historical conversion. Owner confirmed net paid accounting. Concurrency: Plus unchanged; all 5X Pro terms target 6; recharge 5/49 unchanged, 99/199/399/599 target 3/4/5/6. Existing higher user concurrency is never lowered. No eligibility threshold is enabled for current cards.

## Implementation plan and boundaries

T1 root: requirements and repository-backed architecture review; read-only explorer maps existing data contracts. T2 one backend writer: shared purchase rules validation, private-rule/public-eligibility projection, cumulative-recharge query, order/reset authoritative checks and tests. T3 root in isolated sub2api-payment-eligibility-ui-worktree, in parallel with T2: frontend types, reusable admin purchase-rule form, recharge visual editor preserving raw JSON compatibility, disabled card/rail behavior and test coverage. Independent read-only QA/review may run alongside a writer on a different frozen scope. T4 independent QA and review on combined frozen snapshot; scoped actual PostgreSQL and browser tests. T5 root authorized release through existing build-only/canonical dual-node path, guarded current-catalog changes, live readback/Chrome verification.

No schema rewrite, no balance migration, no user-rate edits, no public audience list, no retroactive eligibility checks during fulfillment. Historical orders remain fulfillable if a user later loses eligibility. New threshold amount is not reserved or consumed; it is an admission condition evaluated at checkout/order creation. Concurrent refund may affect later purchases; never rewrite paid orders to enforce a later threshold change.

Knowledge candidate: no; project-specific catalog policy.

## Architecture review and confirmed accounting

Reuse the payment service as the authority for admission, with shared rules normalization and public eligibility projection. No new entitlement table, worker, dependency, or aggregate cache is introduced. JSON metadata extends existing admin persistence; public DTOs strip audience IDs and return per-user eligibility. All three customer entry points (config, plans, checkout-info) must apply the same projection. Customer results must not mutate shared configuration objects.

Accounting follows existing durable facts: `PaymentOrder.CompletedAt != nil`, `order_type=balance`, and `PaymentOrderCurrency(order)==CNY`. The latter uses immutable provider snapshot currency with the existing legacy CNY fallback. Per-order confirmed gateway refund is `calculateGatewayRefundAmount(Amount, PayAmount, RefundAmount, CNY)`, since `refund_amount` is stored in internal order units. Net total subtracts that rounded channel amount, never `refund_requested_amount`. Query only the authenticated user's orders, with keyset paging, and only when a visible configured threshold requires it. `payment_invoice.go` already treats CompletedAt as fulfillment evidence after later refund status changes. Dashboard raw-payment aggregation is not reused.

New order insertion must recheck the locked current plan/group/rules before persisting a chargeable order; if commercial terms changed from outer validation, reject instead of mixing a new plan with an old computed gateway charge. Reset purchases retain subscription-before-user lock ordering and durable replay before mutable rule checks. A reset offer requires visibility of its monthly source plan plus its own reset audience/threshold; the subscription threshold itself does not govern reset purchases. Existing completed orders are never invalidated during fulfillment by later catalog changes.

Current front-end source `2b09868e5` passed independent QA/Review: rule/admin save integration 59 tests, earlier card/page scope 76 tests, locale completeness 3, typecheck/lint/diff checks. Invalid loaded/raw rules are rejected before save; immutable advanced recharge fields are preserved. Earlier pre-eligibility layout was visually inspected in Chrome; new eligibility visual inspection is pending because both native Chrome page/screenshot reading and the Chrome tab channel stopped returning usable content. No alternative browser was substituted.

## Rollback boundary

Before returning either accepting node to a binary that does not understand card rules or reset-price overrides, disable new customer checkout through the existing global checkout switch. Keep order history, existing payment recovery, balances and grants intact. Do not remove rules to make an older binary appear compatible. Restore checkout only when all accepting nodes enforce the configured rules and prices.

Front-end production build on frozen `2b09868e5` passed (including locale validation); no dependency changes. The current catalog keeps thresholds and audiences absent. 5X Pro concurrency and recharge cap grants apply on future successful purchases/renewals; no mass update of existing users is performed.
