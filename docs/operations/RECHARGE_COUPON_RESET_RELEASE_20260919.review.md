# Reset-card unblock and purchase-entry review

## Verdict

PASS. No actionable defects found in the requested incremental scope.

## Reviewed target and scope

Base: `1728c8659` plus the working-tree changes reviewed on 2026-09-19.

Included:

- `backend/internal/service/payment_reset_card_dispatch.go`
- `backend/internal/service/payment_reset_card_external_test.go`
- `frontend/src/components/payment/ResetCardShop.vue` and its test
- `frontend/src/views/user/PaymentView.vue` and its test
- `frontend/src/views/user/SubscriptionsView.vue` and its test
- the matching English and Chinese error/entry translations

Excluded: the concurrently-added `frontend/previews/recharge/main.ts` change and untracked `frontend/node_modules`.

## Safety assessment

- A UnifiedPay create lease is released only for a fresh dispatch
  (`!previouslyUncertain`) and either the local pre-send
  `unifiedpay.ErrInvalidRequest` or a non-retryable API `400 invalid_request`.
  Transport failures, retryable responses, conflicts, server errors, and any
  replay following an uncertain attempt retain the reconciliation fence.
- The release is generation/token/version fenced and reopens the existing
  `PaymentOrder`; it does not create another local order. The browser retains
  the reset-card checkout key for an unchanged quote, so the correction does
  not weaken same-key idempotent replay.
- The new tests cover the fresh rejection cases and the retained-fence cases
  (prior uncertainty, idempotency conflict, retryable 400, 503, and transport
  failure), including the assertion that only one local order exists.
- The subscription shortcut carries the exact subscription id. The purchase
  view suppresses renewal-picker selection for that query, focuses the matching
  reset-card offer after subscriptions load, and does not request a quote or
  create an order until the user activates that offer and confirms checkout.

## Evidence

- `go test -tags=unit ./internal/service -run 'TestResetCard' -count=1` passed.
- `go test -tags=unit ./internal/payment/unifiedpay -count=1` passed.
- `pnpm exec vitest run src/components/payment/__tests__/ResetCardShop.spec.ts src/views/user/__tests__/SubscriptionsView.purchaseEntry.spec.ts src/views/user/__tests__/PaymentView.spec.ts` passed: 78 tests.
- `pnpm run check:i18n` passed: 3 tests.
- `git diff --check` passed.

The PaymentView suite emits an existing jsdom unsupported-navigation warning in
an OAuth recovery test; the test file completed successfully.

## External dependency

The safe-release branch depends on the central payment contract asserted for
this incident: a non-retryable `400 invalid_request` from Create is returned
before the central service admits a payment order. That central behavior is not
implemented or independently verifiable in this checkout. If the central
service changes that contract, this classification must be revised or covered
by a cross-service contract test.
