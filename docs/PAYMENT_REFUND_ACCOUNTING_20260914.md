# Payment refund accounting and review

Status: implemented for `PAYMENT-REFUND-PRORATION-20260914`.

## Problem

The existing admin refund dialog treats every order as a balance recharge. It
defaults to the order's credited amount, optionally deducts the user's aggregate
balance, and the unified-payment adapter rejects subscription orders. This is
unsafe for recharge bonuses and subscription renewals because the database does
not currently distinguish paid balance from gifted balance or identify the term
created by each subscription order.

## Product rules

1. A refund review is calculated by the server. The browser never derives the
   maximum refundable amount from the order total.
2. Refund amounts shown and submitted by an administrator are payment-currency
   amounts. Internal wallet credit and subscription time are separate effects.
3. Wallet consumption uses available paid credit before gifted credit. Frozen
   credit retains the same paid/gift classification through hold, capture, and
   release. If an account is already negative, a new recharge repays that debt
   from the recharge's paid component first; only surviving paid principal is
   added to the account-level refundable component.
4. Gifted credit never increases the cash refund. When paid credit from an order
   is refunded, the still-unused proportional gift is reclaimed as a non-cash
   entitlement effect. If the paid component has been consumed, the cash refund
   is zero even when gifted credit remains.
5. A subscription refund defaults to the unused-time proration:

   `floor_to_currency(original paid amount * unused seconds / purchased seconds)`

   The submitted refund uses that server-calculated amount. The corresponding
   tail duration is removed; the browser cannot replace it with a chosen value.
6. Subscription rollback is automatic only when the order's recorded term is
   the current tail of the same subscription. A merged historical term, later
   renewal, manual extension, deleted group, missing grant, or non-reversible
   extra benefit requires manual review. Orders that raised account concurrency,
   issued reset cards or subscription bonus balance remain manual until those
   benefits have a reversible source ledger. An applied affiliate rebate also
   requires manual recovery before cash is returned.
7. A pending provider refund reserves its wallet components or subscription
   duration before the network request. Usage and renewals cannot consume the
   reserved entitlement. The same transaction enqueues durable subscription and
   API-key cache invalidation. Before a subscription refund reaches the provider,
   the service must also advance the shared Redis authorization fence, delete
   current and legacy subscription cache entries, and publish peer-L1 eviction;
   failure leaves the reservation pending with zero provider calls. A trusted
   provider failure releases it, an unknown result keeps it reserved for leased
   reconciliation, and success captures it exactly once.
8. Historical state is never guessed. Existing balances enter the component
   model as unclassified/non-refundable gift. Orders without a proven grant are
   shown as manual review.

## Data model

The migration is additive and keeps `users.balance`, `users.frozen_balance`, and
the existing order refund fields for compatibility.

- `users.wallet_available_paid`, `users.wallet_frozen_paid`, and
  `users.wallet_component_version`: the paid component inside each existing
  aggregate wallet bucket and its monotonic revision. Gift/unattributed credit
  remains implicit as aggregate credit minus the paid component. A PostgreSQL
  trigger follows every `users.balance`/`frozen_balance` mutation so legacy
  debit paths cannot bypass paid-first accounting.
- `wallet_principal_events`: append-only evidence for explicitly named payment,
  refund, and administrative component transitions. Ordinary request billing
  keeps using the existing usage records, avoiding a second write on the hot
  usage path.
- `payment_wallet_fundings`: one immutable source record per fulfilled balance
  order, containing original paid credit and gift credit plus cumulative refund
  and gift-reclaim counters. This preserves the receipt even when some paid
  principal immediately repays a pre-existing negative account balance.
- `payment_subscription_grants`: one source record per fulfilled subscription
  order, containing the exact subscription row and term interval plus cumulative
  reversed/reserved duration and refunded payment amount.
- `subscription_cache_invalidation_outbox`: transactionally records every
  subscription authorization change and delivers it twice through a leased,
  retrying worker. It is separate from `auth_cache_invalidation_outbox` so an
  older binary can continue processing its fixed `CHAR(64)` API-key payload
  during a rolling deploy or rollback.
- `unified_payment_refund_attempts`: extended with refund kind, quote revision,
  wallet paid/gift reservation, subscription duration reservation, valuation
  time, and payment-currency amount. The existing provider identifiers and
  idempotency key remain authoritative. A second additive migration adds the
  lease, retry time, attempt count and bounded error state used by the automatic
  reconciler; it does not create a second financial state machine.

The wallet component invariants are:

```
0 <= users.wallet_available_paid <= max(users.balance, 0)
0 <= users.wallet_frozen_paid    <= max(users.frozen_balance, 0)
```

Refund reservations move from available balance into frozen balance, preserving
the paid component. They cannot be spent while a provider result is pending.

## API and transaction boundary

`GET /api/v1/admin/payment/orders/:id/refund-review` returns a discriminated
balance or subscription review with a state revision, cash maximum, default
amount, and the exact entitlement effects. Manual-review responses include the
reason and disable submission.

Subscription valuation is frozen to a one-minute review window. The revision
binds the valuation time, exact refundable seconds and resulting expiry; once
the window changes, submission is rejected as stale and the dialog reloads the
review before the administrator can retry.

`POST /api/v1/admin/payment/orders/:id/refund` submits only the reason and
review revision. Under one database transaction the service locks the order
and relevant wallet/subscription rows, recalculates the review, rejects a stale
revision, creates the durable attempt, reserves the entitlement, and marks the
order pending. A subscription attempt must then pass the shared authorization
cache fence before it can call the payment provider. Any pending reviewed
attempt is resumed by a bounded, leased worker with the exact persisted
provider idempotency key.

Lock order:

1. payment order;
2. for wallet refunds, the user followed by its order funding row;
3. for subscription refunds, the order grant followed by its subscription row;
4. refund attempt and audit rows.

The result transaction uses the same order and grant lock order. Terminal state
is idempotent. Contradictory provider evidence keeps the reservation and routes
the order to manual review.

## UI

The dialog first loads the review and shows payment amount separately from its
effect:

- balance: paid credit remaining, gift excluded from cash, gift reclaimed, and
  cash refundable now;
- subscription: purchased term, time used, time remaining, expiry adjustment,
  and prorated cash refund.

The balance-deduction checkbox and force-refund override are removed from the
reviewed flow. A stale quote refreshes the review instead of retrying an old
amount. Orders with non-reversible extra benefits or historical data that cannot
be proved show the reason and cannot be submitted automatically.

## Delivery DAG

1. Add migration and wallet/refund storage primitives.
2. Record grants during balance and subscription fulfillment.
3. Add review calculation and stale-revision validation.
4. Reserve, finalize, and release entitlements in the durable unified-payment
   refund path; legacy provider orders remain manual-only.
5. Replace the admin dialog with the typed review UI.
6. Run backend unit/integration/migration tests and frontend component/type/build
   checks.
7. Freeze a commit for independent QA, code review, and release-readiness audit.
8. Merge and deploy the exact `main` artifact through the documented receiver;
   keep the public purchase entry disabled.

## Rollback

Application rollback is safe because all schema changes are additive. Pending
attempts created by the new code must be reconciled before rolling back the
application: their reservations cannot be understood by the previous binary.
The deployment runbook therefore pauses new refunds, queries pending attempts,
and requires a zero-pending result before binary rollback. Ledger and grant rows
remain as audit evidence and are not deleted. For a post-switch rollback, the
canonical server release drains candidate request admission in place and calls
its monitor-token-protected `GET /internal/refund-rollback-readiness` endpoint.
Only a `2xx` result permits old-generation takeover. A non-`2xx` or unreachable
endpoint restores the traffic state that preceded the check (normally
`accepting`) without replacing its bind-mounted inode, and retains the
candidate, Caddy direction, and local release transaction. The candidate must
finish automatic reconciliation before an operator runs
`sudo systemctl start sub2api-runtime-guard.service`. That service takes the
canonical maintenance lock and calls `sub2api-node-state.sh recover-local`
against the verified Caddy-selected generation; do not rerun the release script,
delete the local transaction, or make a direct Caddy/container rollback. The runtime guard also
applies the same readiness gate before any automatic historical fallback. If
readiness has passed but a later source check or rollback-helper step fails,
admission is restored only while every Caddy view still points to the candidate;
an old or ambiguous Caddy direction remains fenced for the same recovery
transaction. An older binary safely ignores the dedicated subscription cache
outbox while the existing API-key invalidation outbox retains its original
schema and worker contract.

## Verification

- Unit coverage exercises paid-before-gift consumption, zero cash refund after
  paid principal is exhausted, negative-wallet funding, stale review rejection,
  wallet and subscription reserve/capture/release, and legacy fail-closed paths.
- PostgreSQL race coverage verifies wallet component triggers, concurrent
  recharge debt classification, subscription renewal/refund exclusion, exact
  term provenance, and durable subscription cache event creation.
- Frontend coverage verifies review loading and failure states, read-only
  balance/subscription effects, MFA request freezing, and stale-quote refresh.
