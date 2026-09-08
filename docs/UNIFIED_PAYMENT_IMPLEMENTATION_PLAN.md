# Unified payment integration continuation

Source: the latest development request in task `01a0778b-2cde-7c62-9812-7da4a98712f7`.
Continue in the existing checkout at `1b1b4a7f34ae2913d68680377826637b2bc2e23b`;
preserve its interrupted changes. Local checks do not authorize a production release.

## Scope and acceptance

- R1 / AC1: administrators can select unified Alipay or WeChat without entering
  channel credentials in Sub2. Runtime capabilities are read-only; explicit
  legacy provider selection also reaches the actual load balancer.
- R2 / AC2: signed, scoped payment notifications and trustworthy queries drive
  fulfillment. An unavailable gateway cannot make cancellation appear final.
- R3 / AC3: administrators can refund a plain balance order through the unified
  API. Persist integer amounts and stable request identity before networking;
  accepted or unknown requests stay pending. Only a trusted success may recover
  balance, once. Retain contradictory evidence and stop further refunds for review.
  Existing self-service and irreversible-entitlement restrictions still apply.

## Dependency plan

| Task | Depends on | Owner / allowed writes | Evidence and failure boundary |
| --- | --- | --- | --- |
| T1 Routing and settings | R1 | Root: settings, route selection, UI and tests | Canonical source, disabled runtime and explicit provider regression tests; isolated UI fixture |
| T2 Cancellation | R2 | Root: lifecycle and tests | Query/runtime failure leaves pending; legacy cancellation regression |
| T3 Refund API adapter | fixed pay-v1 contract | Gateway worker: optional payment types and new unifiedpay refund files | Signed local HTTP tests, exact amount/scope/identity validation |
| T4 Refund persistence and terminal processing | T3 | Root: additive migration, refund service, webhook and tests | One active attempt per order, no transaction across HTTP, restart-safe retries, atomic funds result/balance/audit |
| T5 Integration and independent review | T1–T4 | Root integrates; separate agent reads frozen changes | Targeted backend/frontend tests, PostgreSQL concurrency where available, actionable review resolved |
| T6 Test-runtime activation and QR journey | T5 plus documented runtime authority | Root after applicable deployment authorization | Correct sandbox/live scope and Vault enrollment, scan/paid/cancel/refund evidence; never report mocks as real channel acceptance |

Independent files may be written in parallel under the user's explicit delegation
instructions; the root integrates them. No overlapping ownership is allowed.
The fixed central source blobs and intentional differences are recorded in
`UNIFIED_PAYMENT_INTEGRATION.md`. Changed contract/source invalidates T3–T5.

Rollback before activation is to retain the disabled runtime and omit the new
release. The additive attempt table must be retained after any refund is submitted;
do not delete financial evidence to roll back an application. A manual-review
attempt or unresolved request identity blocks a new refund. Missing deployment,
credential or environment authority blocks T6 only, not local implementation.

Plan: `PLAN_READY`. On 2026-09-07, T1–T5 reached `IMPLEMENTATION_READY` for local
implementation and scoped independent review. The final PostgreSQL race check,
backend regressions, frontend tests/typecheck and deployment-script mocks passed.
Exact commands, review disposition and limitations are recorded in
`UNIFIED_PAYMENT_INTEGRATION.md`. T6 remains an environment activation and real
channel acceptance step; this plan does not sign QA acceptance or release approval.

Knowledge candidate: no — this is project-specific work pending acceptance.
