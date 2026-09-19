# PAYMENT-DISCOUNT-20260919

Type: feature. Status: IMPLEMENTATION_READY; independent source QA and review passed. Source: main 9c789f0b7 plus
local compact-card changes. No production permission implied.

## Requirements / acceptance

R1: recharge concurrent request baseline is 3; advertise only targets >3.
R2: keep registration balance gifts and add a payment-discount category in the
same admin coupon page. Discounts use fixed currency amounts or percent off.
One code per order, full original product entitlements, positive cash payment.
R3: each code configures starts_at, required expires_at, global max_uses and
per_user_max_uses (0 unlimited), optional target_user_id (null everyone), currency,
enabled, discount kind/value. Default per-user max is one. Never hard-delete audit.
R4: user can apply/remove code before confirming payment. Server quote returns
original charge, discount and final charge; stale quotes require re-confirmation.
Changing product, method or code invalidates the quote. No client-authoritative
price or identity. No zero-payment fulfillment path in this iteration.
R5: authenticated quote and create are rate limited. Generic invalid-coupon errors,
crypto-generated random default codes, normalized bounded ASCII codes, strict
finite precision/currency/date/cap checks, target-user enforcement on server.
R6: lock coupon and count reserved+consumed transactions; each order has one
immutable use snapshot and idempotent request identity. Unknown provider state
retains reservation. Confirmed cancellation releases. Trusted paid events consume
once; released late-paid orders reacquire hard capacity or record payment in a
manual-review state without automatic fulfillment. Refund never restores uses.
R7: order details show original/discount/actual paid and code; admin coupon detail
shows order/user/status/amount/time usage records. Administrative changes audited.

## Architecture decision

Separate payment_discount_codes / payment_discount_uses SQL tables reuse the
PaymentService/PaymentOrder boundary without expanding legacy registration-gift
Ent records. Payment discount data is snapshotted into product_snapshot. Money is
computed with decimal/minor units; final charge is clamped to one smallest currency
unit. Discount applies to the charge including fees. Paid recharge credit is
scaled by discounted_charge/original_charge; the remaining credit is a gift so
refund principal cannot include a discount. Historical and non-coupon paths stay
compatible. Parent locks serialize capacity; ledger counts avoid cache drift.

Order locking order is order then coupon for existing-order transitions; creation
uses existing product/provider locks, then coupon, then its new order/ledger.
Do not use catalog expiry or a browser timer as proof the provider closed payment.

## Task DAG / ownership

T1 root: concurrency presentation and acceptance/architecture (R1).
T2 backend worker: additive SQL migration, payment coupon CRUD, quote math,
reservation/consume/release/replay storage helpers and scoped tests (R2,R3,R5,R6).
T3 root after T2 contract: payment creation, callback/cancel/recovery, handlers and
routes; authoritative discounted totals, snapshot and refund classification (R4–R7).
T4 frontend worker after API contract: admin category, payment input/confirmation,
order trace, types/client and tests (R2–R4,R7), preserve compact-card edits.
T5 independent QA then Review on frozen combined changes: concurrency real-DB
checks, malicious requests, race/late-paid/replay/refund, UI states and regression.
Each writer owns disjoint paths; root owns integration and final checks.

Rollback: an older binary cannot maintain the coupon ledger. Disable new coupon
creation and drain every reserved coupon order through trusted payment or confirmed
closure using a coupon-aware binary before considering an incompatible rollback.
Do not switch while a gateway result or paid-review order remains unresolved.
Prefer a compatible forward fix. Preserve both coupon tables, snapshots and audit
rows; no destructive down migration. No release until source gates and owner
authorization. A paid-review order is a recorded payment without entitlement
delivery, not an unpaid failure; investigate the provider transaction and linked
usage before any manual financial resolution. Generic fulfillment retry is blocked.

## Fixed reference and local differences

Inspected upstream Wei-Shaw/sub2api at
bdb42e22f81fcb633ff0a060961211dd2bcb515b:
backend/internal/service/payment_order.go and promo_service.go. Registration
promo usage and original payment provider/order boundaries are retained. New
payment discount admission, immutable ledger and monthly/compact local UI are
local extensions, not copied upstream discount behavior. Existing LGPL-3.0
notices and distribution duties remain unchanged.

Affiliate rebate for discounted purchases uses discounted principal: balance
uses its paid-credit snapshot, subscription scales the original rebate base by
actual/original charge. Coupon value must never become withdrawable referral
principal. Existing non-coupon rebate behavior remains unchanged.

## Local visual acceptance

The local preview uses actual payment, confirmation, coupon administration and
order-detail components with intercepted API responses; it creates no real orders.
At 390px the payment view has no horizontal overflow, coupon input shows original
and discount values, and confirmation shows the discounted charge. The 99 tier
omits baseline concurrency. Desktop and mobile card alignment, monthly Plus
selection and configured gift descriptions were checked (see the compact-card
record). Light/dark confirmation and admin editing were inspected.

A reserved coupon's late payment that cannot reacquire capacity is presented as
paid and awaiting manual review in both the shared order list and admin detail,
without implying a refund. The generic review flag is separate from refund
entitlement review, and direct fulfillment retries are rejected before an audit
entry is written. Regression tests cover both coupon review and genuine refund
review. Browser console error checks were clear; the temporary viewport override
was reset after inspection.

Preview entry points: `/previews/recharge/index.html`, `?view=coupons`, and
`?view=confirmation` on the local preview server. `SAVE2026` demonstrates a 20%
discount. No live catalog data, payment-provider configuration, or production
schema has been changed. Database migration 253 must be applied by the normal
release process before this feature is enabled in a deployed environment.

## Source validation

Frontend frozen scope v3 passed the complete suite: 323 test files / 2,479 tests,
locale completeness, TypeScript, ESLint and the production build. The final scope
contains 34 frontend files; before/after SHA-256 guards matched. Its manifest
SHA-256 is `c6fc96ff3f117e007dc2488f9a6854e4e8f5d6c0961b9109216ff0ef9f29bbbd`.
The independent QA report is retained locally at
`/tmp/sub2-coupon-frontend-qa-v3.md`; this summary is the durable project record.

Backend QA passed service payment/refund/reset-card/WeChat regressions, focused
coupon lifecycle and invoice-presentation tests, handler/route tests and `go vet`.
The real PostgreSQL test applied migration 253 and verified that concurrent
reservations against a one-use code admit exactly one order. The final three-file
manual-review correction passed independent delta QA, retaining real refund and
reset-card retry behavior. The final backend manifest covers 21 files, SHA-256
`5db29d9cdec788cb27d6afeef958eba7f6983ed029a23f65270b0ca71cf3d9b3`.
Local reports: `/tmp/sub2-coupon-backend-qa.md` and
`/tmp/sub2-coupon-backend-qa-v2.md`. No live provider callback or production
migration was exercised; mock-browser acceptance and automated lifecycle tests
do not claim real-payment acceptance.

Independent backend review initially identified the missing generic manual-review
projection and pre-audit retry guard. Both were repaired, independently re-QAed,
and re-reviewed with no remaining actionable findings. The v2 review confirmed
both user and admin serializers use the corrected projection; final file hashes
remained unchanged. Review record: `/tmp/sub2-coupon-backend-review-v2.md`.

The frontend review identified an OAuth display boundary: authorization may start
before an order exists, so no order-bound local recovery snapshot can be saved.
The correction returns the sanitized persisted `payment_discount` snapshot from
both normal order creation and idempotent replay responses. Confirmation already
prefers that server response. A regression begins with `order_id=0`, verifies
there is no recovery snapshot, resumes using only the signed token, and checks
that server discount details appear and persist with the newly created order.
No unsigned coupon fields are added to the resumed payment request.

The OAuth response correction passed independent incremental QA on both sides.
The final frontend scope v4 (34 files) has manifest SHA-256
`b7442ee243be759e0e944d77ccce53133f471813e7021cc868e937b92f59901c`;
its production delta is comment-only, with 59 PaymentView tests, TypeScript and
ESLint passing. The final backend scope v3 (22 files) has SHA-256
`d93e60acc69cd9c464cd6ba3e0cc62335edfb6e4a943d1bd383f01f2a533c332`.
New response and stored replay tests, handler/admin/routes compilation, `go vet`
and source guards passed. QA verified that only persisted sanitized coupon data
is exposed, money remains JSON strings, and non-coupon responses omit the field.
The full earlier QA gates remain applicable to unchanged files. Local reports:
`/tmp/sub2-coupon-frontend-qa-v4.md` and `/tmp/sub2-coupon-backend-qa-v3.md`.

Final independent cross-boundary re-review passed: the OAuth display finding is
closed for creation, pending replay and terminal replay. Both final manifests
matched before and after review, with no remaining actionable findings in the
reviewed scope. Record: `/tmp/sub2-coupon-frontend-review-v2.md`. This concludes
local implementation verification; owner visual acceptance and any later release
are separate. No commit, push, production configuration change or deployment was
performed by this task.

## Owner follow-up: applicability and whole-unit payable amounts

Status: implementation complete; independent incremental review recorded below. Extends the same local worktree; earlier
frozen manifests remain historical evidence and do not certify this extension.

- R8: configure coupon applicability to balance, subscription, or both; optional
  subscription plan IDs narrow the subscription scope. Reset-card purchases never
  accept new coupon quotes/orders. Enforcement is server-side, including locked
  reservation, direct API calls and signed resume. Existing financial facts and
  paid-order processing remain immutable.
- R9 (owner confirmed): round the discounted payable upward to a whole currency
  unit, e.g. 99 with 20% off yields 80 paid and 19 discount. Use decimal arithmetic;
  a coupon that cannot produce a positive discount after rounding is unavailable,
  never an excuse to charge above the original amount. Ordinary non-coupon orders
  are unchanged. Balance coupons retain percent as the UI default.

Incremental DAG: T7 backend coupon worker owns additive applicability storage,
CRUD/audit validation, quote/reservation scope and integer payable tests. T8 admin
UI worker owns the matching form/API/locales/tests after the agreed DTO seam.
T9 root owns customer reset-card exclusion, examples, subscription presentation
and integration. A read-only researcher traces group-to-checkout refresh before
subscription edits. Distinct file ownership follows the owner's explicit parallel
work preference. Then freeze the extension for independent scoped QA and review,
followed by bounded desktop/mobile local visual inspection. No push/deployment or
live configuration change is included. Preserve earlier coupon ledger data on
rollback; use a compatible forward fix if new scope semantics are in use.

- R10 (owner correction): payment-discount configuration belongs to the existing
  Order Management navigation, at `/admin/orders/coupons`, using AppLayout and
  the existing payment coupon panel. Registration gifts stay at
  `/admin/promo-codes`; the earlier same-page category/tab decision is superseded.
  Both require admin authentication. Coupon administration/history remains
  available when new public checkout is disabled, like order and invoice history.
  The independent local component preview is not a separate production product.

Extension verification: the backend worker passed normal/unit-tagged payment
discount tests, the additive scope migration contract, and real PostgreSQL
concurrent reservation tests including 253 → 254 and repeated 254 execution.
Customer card/reset/refresh tests (89), route/sidebar tests (27), and admin
coupon tests (11) passed; production build passed. Independent frontend checks
passed 7 files / 158 tests, typecheck and scoped lint. Review found one selection
consistency defect when a selected plan changes period while another plan keeps
the old period available; refresh now follows the selected plan's new period.
Its regression passed in the 63-test PaymentView suite.

Frozen extension manifests: backend 25 files, SHA-256
`c6dd4de9a58257835ef8e6f241e794a914c248d5459b152b50c9d8e132c3700e`;
frontend 42 files including the restored registration view, SHA-256
`93d6ad94bcf2425443bcf9f8b086b1968a7b18b666a0fbe37198d66d78d23385`.
Files are `/tmp/sub2-coupon-{backend,frontend}-scope-extension.json`.

Independent backend incremental review passed without actionable findings,
including signed-resume tests and persisted-order authority checks. Evidence:
`/tmp/sub2-scope-backend-review.md`. The backend/frontend manifest hashes were
rechecked after the period fix and matched; `git diff --check` passed.

Independent frontend incremental QA/review passed after verifying the P2 fix:
7 files / 159 tests, TypeScript and final 42-file manifest passed, with no remaining
actionable frontend findings. Evidence: `/tmp/sub2-scope-frontend-review.md`.
The extension is locally implementation-ready, not deployed or live-payment
validated. No commit, push, deployment or live configuration writes were made.
