# Unified payment live pilot preparation — 2026-09-09

Owner authorized development, production deployment and same-entity WeChat/Alipay
1–2 fen tests. Ordinary customer purchasing remains disabled. This record does
not assert a real payment or refund has succeeded.

## Source and database compatibility

Release worktree is based on production fork main
`4f40caad8260c207a4f07d771a3c28c243793a7e` (v0.2.3). Payment changes were
transplanted from the earlier development worktree without reverting subsequent
production changes. New refund tables append as migration
`237_unified_payment_refund_attempts.sql`; already applied migrations 234–236
are unchanged. The service fixture reads the same embedded migration 237.

Binding writes retain the raw integration setting for older readers. A separate
monotonic revision and digest survive manual-mode deletion. The raw value,
revision and exact idempotency lease response commit in one PostgreSQL transaction.
Do not allow old and new binding writers concurrently. On rollback, stop binding
writes; retain revision metadata and financial tables. Metadata mismatch fails
closed and requires an audited reconciliation, not automatic rebasing.

## Verification on integrated source

- Payment, repository, admin handler, server wiring and both Vault command unit
  packages passed. Final full service package passed (7,005 pass events and two
  skips in Go JSON output); payment/idempotency service race tests passed (8.755s).
  An initial run caught the migration-fixture rename omission, which was fixed.
  One unrelated OpenAI cold-cache test failed transiently; its isolated rerun
  and the final full service rerun passed without changing that module.
- Real PostgreSQL race checks passed for atomic binding, stale claims, lost
  responses, ABA, refund state/evidence and webhook inbox concurrency (7.953s).
- Central HTTP + Sub2 gateway joint test passed (45.12s): config, 1/2 fen mock
  orders, idempotency, trusted state, refund, close and signed webhook verification.
- Frontend: five affected test files, 58 tests passed after MFA integration; typecheck passed; final production
  Vite build passed (11.57s). Existing chunk-size and router-link test warnings
  do not imply live payment acceptance.
- Config, Vault sidecar and blue-green external-runtime shell suites passed.
  Initial live-enabled installation is rejected; the exact installed disabled
  profile must precede an explicit runtime enable. No script enables purchasing.
- API template permits only exact POST `/api/v1/payment/webhook/unified` as the
  payment callback; it does not expose product orders or admin settings. Route
  drift tests, cache policy check and 12 existing image route-contract tests pass.
  The installed production Caddy binary adapted the public template without
  changing runtime configuration; effective routing allows that exact POST and
  rejects GET, extra path segments, product-order and admin-settings paths.

## Current production evidence and pending activation

Read-only SSH checks confirmed both nodes still run healthy blue containers at
`4f40caad…`, restart count zero. `sub2api-candidate` is traffic accepting/background
active; `sub2api-new` is traffic accepting/background standby. Preserve these
roles, including explicit `preserve-standby` for releases on the old origin.
The configured production repository is `Turtle-Li/sub2api`, branch main.
Unsigned POST to the public callback returned backend `503 retry`, proving current
edge reachability while the unified runtime is unavailable. No Caddy mutation was
needed for this check.

Central live images and isolated Compose have been staged on the payment host;
no live runtime or merchant credentials have been activated. The local Vault
unlock attempt failed to establish an owner session. Actual source-field metadata,
WeChat bound AppID/platform key ID and split credential assembly require verification
before injection. Do not infer these values or reuse sandbox profiles.

Live Sub2 activation requires explicit `--sub2-host sub2api-candidate` or
`--sub2-host sub2api-new`; payment SSH uses alias `totools-pay-sandbox`. It injects
independent coherent Ed25519 material into memory agents and emits a public bundle.
It does not register SQL, change runtime config or enable purchasing. Complete
public central enrollment, binding and scoped runtime checks before owner scan tests.

Build/release uses the existing manually dispatched exact-main GitHub workflow
and verified image receiver under the maintenance lock. Local builds alone are
not deployed. Preserve the current background owner, stop on a failed health or
identity gate and retain existing rollback images, database backups and evidence.

The Registry filename guard flags the existing tracked `deploy/.env.example`.
Its exact staged change was reviewed: only public method names and callback URL
were added. All other staged paths and added lines pass the same filename and
private-key-block checks; no credentials or runtime state are included.

## Final review repairs and bounded limitation

Refund submission and query/finalization unconditionally require the existing
session TOTP step-up grant; admin API keys and sessions without that grant are
rejected even when optional global step-up is disabled. The view reuses the
existing verification dialog, retains immutable order/payload snapshots across
verification and serializes refund/query actions. Cancelling verification is a
silent no-op.

Binding recovery explicitly opts into a required atomic finalizer. An expired
processing lease can then be reclaimed before the 24-hour response-retention TTL;
legacy executors with ID-only completion do not inherit this behavior. Missing
keys cannot use observe-only bypass, and a non-finalizer result fails closed.
Actual PostgreSQL coordinator recovery plus all previous atomic binding tests
passed under race detection (6.797s); service/admin idempotency tests passed.

The legacy admin refund HTTP API does not yet offer caller-key response replay.
Unified pending attempts retain the same durable channel request/key, and terminal
refunded or partially-refunded orders cannot reserve another successful attempt.
Do not broaden this pilot into repeated partial-refund workflows until a separate
admin HTTP idempotency contract is implemented. Central signed product refund
creation already requires an idempotency key. This limitation does not assert that
mock coverage proves actual channel behavior.

Final MFA integration compile checks: server package and complete admin handler
package passed; frontend five-file suite passed all 58 tests.
