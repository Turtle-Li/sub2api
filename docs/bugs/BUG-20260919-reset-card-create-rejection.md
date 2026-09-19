# BUG-20260919-reset-card-create-rejection

## Report and evidence

The owner reported reset-card checkout showing the raw English
`reset card payment checkout is still being created` error. Read-only production
inspection found local order 5 pending for CNY 40 with no checkout URL or QR,
dispatch generation 1 marked uncertain, and a `RESET_CARD_CREATE_UNCONFIRMED`
audit. The central service had no matching admitted order or idempotency record.

Both enabled `sub2` / `live` channel bindings still had pilot limits of 10
orders and 20 fen per day. Central `OrderService.Create` rejects 4,000 fen
against that 20-fen limit before order admission with non-retryable
`400 invalid_request`. The actual failed HTTP response was not logged; this
cause is supported by the exact configuration, code path and persisted states.

Sub2 then classified every unified-pay creation error as uncertain, including
a definite first-attempt rejection. Immediate same-key replay therefore showed
the in-progress fence rather than an actionable message.

## Contract and repair

Contract source and fixed SDK blobs are recorded in
`docs/UNIFIED_PAYMENT_INTEGRATION.md`. The existing typed `APIError` and local
`ErrInvalidRequest` distinguish pre-admission validation from ambiguous network,
retryable and conflict failures. No SDK protocol, signature or payment-success
rules change.

Only a fresh dispatch with local invalid-request validation or a non-retryable
central `400 invalid_request` releases its fence via the existing generation
CAS. The local order and idempotency key remain reusable. A new sanitized audit
and localized `RESET_CARD_PAYMENT_REJECTED` response explain the failure.
An already-uncertain dispatch remains fenced even when a subsequent retry is
rejected; network failures, 409, retryable 400 and 5xx also retain uncertainty.
Existing in-progress/unconfirmed errors now have Chinese and English messages.

The frontend no longer yields before setting its submitting guard, preventing
two synchronous reset-card checkout events from issuing duplicate requests.

The owner superseded an interim 50,000 yuan / 500 orders proposal and requested
unlimited daily collection. The companion central change gives **0** explicit
unlimited semantics, retaining usage counters. Only Sub2 live channel bindings
are intended to become 0/0; other products and refund/security controls retain
their settings. This document does not claim that rollout has happened.

## Subscription navigation

Eligible active OpenAI subscriptions expose a “购买重置卡” action next to renewal.
It links to the subscription purchase tab with the exact subscription ID,
scrolls/focuses and highlights its reset-card offer after data loads, and does
not automatically quote, confirm or create an order. The subscription purchase
tab also exposes a compact top shortcut when an eligible offer exists.

## Verification

- Regression first failed for the two fresh definite-rejection cases; all
  `TestResetCard*` service tests passed after the repair.
- Seven classification cases cover fresh validation, prior uncertainty,
  conflicts, retryable rejection, server failure and transport loss. A released
  attempt reuses one order at generation 2.
- Payment view/payment-flow/i18n focused tests: 101 passed, including duplicate
  synchronous event suppression and the no-coupon reset-card contract.
- Quick-entry scoped tests: 81 passed; TypeScript, ESLint and production build
  passed. Independent review and browser results are recorded with the release.
- Production inspection and backups do not count as real payment acceptance.
  No new live payment, refund or manual financial-state update was performed.
