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
   API-key cache invalidation. Before a refund reaches the provider, the service
   must also replace the applicable shared Redis authorization fence and delete
   current and legacy cache entries. Subscription changes additionally publish
   peer-L1 eviction. Any fence failure leaves the reservation pending with zero
   provider calls. A trusted provider failure releases it, an unknown result
   keeps it reserved for leased reconciliation, and success captures it exactly
   once.
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
order pending. Both balance and subscription attempts must then pass their
shared authorization cache fences before they can call the payment provider.
The balance fence uses an expiring, unique token: expiry cannot reset to a
previous value, so a delayed cache reader can never refill an old balance after
the token has expired or been replaced. Any pending reviewed attempt is resumed
by a bounded, leased worker with the exact persisted provider idempotency key.

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
Only a `2xx` JSON object with `ready: true` and an integer
`entitlement_reserved_reviewed_pending_count: 0` permits old-generation takeover.
An invalid JSON body, non-`2xx` or unreachable
endpoint normally restores the traffic state that preceded the check without
replacing its bind-mounted inode, and retains the candidate, Caddy direction,
and local release transaction. A server-coordinated Caddy reload additionally
persists `RECOVERY_OWNER=server-wrapper` and
`LIVE_RELOAD_ATTEMPTED=true` atomically before its live reload call. Therefore
the coordinator treats that retained transaction as a possible candidate
exposure even if all three Caddy views have returned to old: it drains, waits
for zero candidate in-flight requests, and checks readiness before any old
generation restoration. For an invalid JSON body, non-`2xx` or unreachable result with old or
ambiguous Caddy views, admission remains `draining` and both transactions plus
the candidate remain intact.

After automatic reconciliation, recover this state only through
`sudo /opt/sub2api/scripts/sub2api-server-release.sh --recover-retained-caddy-exposure`.
It repeats the gate, explicitly restores and verifies the host, startup, and
Admin Caddy views to old, runs `abort-local` to restore admission, and removes
the retained target. Do not rerun the blue-green helper directly, delete either
transaction, or make a direct Caddy/container rollback: a normal helper
invocation refuses this wrapper-owned live-reload transaction. If restoration
has already completed but local finalization was interrupted, use
`sudo systemctl start sub2api-runtime-guard.service` only to run the existing
`recover-local` finalizer against the verified Caddy-selected generation. The
runtime guard also applies the same readiness gate before any automatic
historical fallback. If readiness has passed but a later source check or
rollback-helper step fails, admission is restored only while every Caddy view
still points to the candidate; an old or ambiguous Caddy direction remains
fenced for the same recovery transaction. An older binary safely ignores the
dedicated subscription cache outbox while the existing API-key invalidation
outbox retains its original schema and worker contract.

`PAYMENT_REVIEWED_REFUNDS_ENABLED` is private deployment state, not a public
application setting. Keep it absent or `false` for the first compatibility
release from `ec7`. Enable it only through its compare-and-swap transition
after the candidate has committed and every old `ec7` request process has
stopped. The gate covers reservation and provider creation; a refund with a
known provider ID remains queryable while disabled so uncertain external money
can converge.

For a planned rollback to an incompatible binary, drain request admission and
wait for request in-flight count zero before changing the gate. Leave the gate
enabled while durable pending attempts finish, require refund rollback
readiness zero, then compare-and-swap it to `false` immediately before the old
binary is restored. Admission remains drained across that sequence, preventing
a new reservation after the readiness check. Caddy selection or a healthy
candidate alone does not authorize a direct enable.

## Verification

- Unit coverage exercises paid-before-gift consumption, zero cash refund after
  paid principal is exhausted, negative-wallet funding, stale review rejection,
  wallet and subscription reserve/capture/release, and legacy fail-closed paths.
- PostgreSQL race coverage verifies wallet component triggers, concurrent
  recharge debt classification, subscription renewal/refund exclusion, exact
  term provenance, and durable subscription cache event creation.
- Frontend coverage verifies review loading and failure states, read-only
  balance/subscription effects, MFA request freezing, and stale-quote refresh.


## Rollout transition contract (September 14 continuation)

The monitor-token-protected `POST /internal/reviewed-refunds-rollout` is a
narrow CAS operation, not a generic settings API. Enable accepts expected
absence (`""`) or exact `"false"`; disable accepts exact `"true"`. A stale
expectation returns 409 and never overwrites another transition. Invalid or
missing fields, unknown fields and trailing JSON are rejected. Operational
failures return only a safe failure shape, never database details.

Both transitions acquire PostgreSQL advisory transaction lock
`ReviewedRefundRolloutLockID`, revalidate explicit runtime state, require zero
reserved reviewed attempts, and change the setting in that transaction.
Reviewed reservations acquire the shared form of the same lock **before**
order and entitlement locks. Thus a concurrent reservation commits before the
rollout counts it, or reads the disabled flag after the CAS commits. The lock
is deliberately scoped only to these low-frequency financial operations.

Enable requires an active process, accepting request admission and healthy
PostgreSQL/Redis. Disable permits active or standby background state in a live process, explicitly draining
admission and zero in-flight requests; it remains reachable while draining and
does not count its own request. Host topology remains the responsibility of
the root-only maintenance-lock-owning helper. The single-origin deployment
assumes retired remote writers remain excluded by the existing database and
Redis source allowlist. A future multi-origin deployment must add a complete
writer inventory/fence before reusing enable; ownership is the deployment
maintainer.

Continuation gates: T1 backend CAS and reservation serialization (root) ->
T2 host topology helper/installer (isolated worker, root integration) ->
T3 exact-source unit/race/PostgreSQL/frontend/deployment validation and
independent review/QA -> T4 merge into `main`, CI/security, verified build,
isolated backup restoration/migration -> T5 canonical receiver, natural old
process drain, guarded enable and read-only order #4 review. A failed gate
blocks its dependents. Preserve purchase-entry closure and all financial
history throughout; no real refund is part of acceptance.


### Concurrent funding regression

Full PostgreSQL race validation reproduced a lock-upgrade deadlock when two
redeemed recharge codes referenced the same user. Each `used_by` foreign key
held KEY SHARE before principal classification requested FOR UPDATE. The
classification now takes NO KEY UPDATE: user ID is unchanged, both references
remain valid, and balance/principal updates still serialize. A deterministic
two-transaction regression holds both FK-equivalent locks before invoking the
actual funding repository, then verifies both credits and paid-first debt
classification. No retry or financial-error suppression was added.

The readiness partial index covers every reserved reviewed attempt, including
manual/terminal anomalies, rather than only the worker's retryable subset.

### Embedded production routing

Both embedded frontend middleware variants bypass the two exact internal
refund readiness/rollout paths. These monitor-token-protected handlers must
retain their JSON status/body contract in an actual `embed` build; a SPA
fallback must never turn a rejected internal probe into HTTP 200 HTML. CI
builds frontend assets and runs embedded web/common-route integration tests.
Canonical release and runtime rollback consumers independently reject invalid
readiness bodies, including HTTP 200 HTML and nonzero counts.

## Provider-balance pause and operator recovery

A WeChat `HTTP_403_NOT_ENOUGH` response means the merchant refund account did
not have enough available funds. It is not a pricing or proration result. The
central refund remains `UNKNOWN`, retains its original request and provider
refund numbers, and keeps the product entitlement reserved. Sub2 projects only
the bounded `provider_status`, `failure_code`, and central update time; raw
provider messages and bodies never enter the product database.

Migration 251 can hydrate those safe fields for an existing attempt from the
latest strictly correlated `UNIFIED_REFUND_RESULT` event. Both the product
refund number and central refund request ID must match. This lets a known
balance shortage appear as a paused recovery state after deployment without a
provider query or a new money operation.

The admin order view distinguishes the following states: waiting for merchant
balance, retry queued, processing, manual review, succeeded, and failed. A
waiting-balance order explicitly says that cash has not been confirmed and its
benefit is still reserved. Two fresh-TOTP actions are available only for the
exact balance-shortage fence and only while no separate correlation or signed
event conflict exists:

- **Balance replenished, retry** calls the central `resume` action with a stable
  idempotency key for the current balance-shortage generation. If WeChat
  rejects the resumed request for insufficient balance again, the later
  trusted central update creates a new generation and therefore a new key;
  network retries inside either generation keep the same key. The action never
  creates a second Sub2 attempt or central refund.
  The central worker first queries the original provider refund number and may
  resubmit only when the provider proves that exact number does not exist.
- **Refunded externally** records evidence for money already returned outside
  the provider API. The browser supplies method, external reference, refund
  time, and a bounded evidence summary. It cannot supply or change the amount.
  Its idempotency key is stable for the same proven shortage generation and
  the same normalized evidence. It changes after either a later trusted
  provider update or an administrator correction to the method, reference,
  time, or evidence, so a cached stale-fence rejection cannot poison a later
  valid confirmation while an exact network retry remains safe.
  The central money authority atomically records the manual settlement,
  creates the refund transaction and debit fund event, releases the monetary
  reservation, advances the order/refund state, and enqueues the ordinary
  signed success webhook. Sub2 reclaims the reserved entitlement only after
  that trusted central success is observed.

An uncertain HTTP response leaves the local reservation and manual fence in
place. Refreshing or repeating the same action is safe through central
idempotency and the signed webhook path. Operators must not use external
confirmation until the customer has actually received the money.

### Legacy whole-second subscription reservations

Builds before the sub-second preservation repair could leave an already-held
future subscription refund with both its live expiry and `valuation_at`
truncated to the start second, while the grant itself starts later in that
same second. A trusted terminal capture normalizes only this provable legacy
shape to the grant's exact `term_start_at`, and atomically repairs the live
subscription and grant together. A larger or different drift remains blocked
as an integrity error; capture never guesses a term boundary. New reservations
retain their exact sub-second boundary and do not enter this compatibility path.

## Historical subscription grant repair

Historical subscription orders still require audited grant backfill before
automatic refund review. The repair uses the immutable product snapshot,
`SUBSCRIPTION_ASSIGNED` audit, exact purchased duration, user/group identity,
and the current subscription timeline. A tolerance of at most one second is
allowed only when comparing legacy timestamp precision; the recorded grant
uses the exact purchased duration and must not overlap any later grant. Larger
drift, missing evidence, or overlapping entitlement remains blocked. New
refund reservations retain subsecond precision so the historical mismatch does
not recur.
