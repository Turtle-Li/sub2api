# Reviewed refunds release — September 14, 2026

## Scope and invariant

Server-reviewed balance/subscription refunds, paid-first wallet provenance,
pre-provider entitlement reservation, shared cache fencing, leased recovery,
and guarded rollout. Purchase discovery remains closed. No actual refund is
part of deployment or acceptance. Historical order #4 alone may receive the
owner-authorized, exact-tail subscription provenance record for read-only
review; order #3 is not backfilled or refunded.

## Source verification

- Core and retained-release repairs: `097eb65ae9dacfb7c09885516ab40d4443ae587d`.
- Host rollout helper integration: `236f06c44` (source-only descendant).
- Full backend unit suite passed; Wire check and server build passed.
- Frontend lint/i18n, all 301 test files / 2223 tests, typechecked production
  build passed.
- Changed server/repository/service/routes focused race suite passed.
- Real PostgreSQL refund/provenance/CAS tests passed. The new deterministic
  funding FK-lock regression failed before repair with a PostgreSQL deadlock,
  then passed three race-enabled runs after NO KEY UPDATE replaced the
  unnecessarily strong row lock. Both total balance and paid principal are
  asserted; no retry or swallowed financial error was added.
- golangci-lint 2.13.0: zero issues. Existing deployment CI shell matrix passed.
  Production release scripts pass ShellCheck; test scripts pass warning-level
  ShellCheck (existing SC2030/2031 informational subshell-fixture diagnostics).
- Independent finance/API QA: QA_PASS on core commit, reviewed manifest digest
  `7bb666e4af9eff4d7736df2fa82031a4191a4872d8b971f236bd779f9e35be74`.
  QA independently reran financial unit, CAS/standby-disable and real PostgreSQL
  race tests. Scope excludes host scripts, UI visual and live money movement.
- Independent core/recovery review round 2: zero open P1/P2; approves the
  funding lock repair. Final host-helper review is a separate gate.

Broad service race testing and full repository integration also ran. They
exposed unchanged shared test-state races and an existing reset-card checkout
409 expectation failure; baseline reproduction/disposition is recorded before
release below. These runs must not be reported as entirely green.

## Backup and migration rehearsal

The current authorized database host is `sub2api-db`; only serving origin is
`sub2api-candidate`. The expired old origin was not contacted. Root-owned backup
and restore helpers matched the project source SHA-256 before execution.

- Fresh archive: `/opt/sub2api-db-backups/sub2api-db-backup-20260914-125715.tar.gz`.
- Canonical isolated restore passed: schema count 299, schema hash
  `f595eb3e315a38605ed5999d485292ef`, Redis keys 2354.
- Additional network-disabled, disposable PostgreSQL restore applied exact
  migrations 245 and 246 after proving 244 present and both new migrations
  absent. Wallet values, order statuses/amounts and subscription expiry summary
  hash stayed `f345e27992358c026215808c7a350e5a` before/after.
- Historical paid-component values remained zero; funding/grant tables remained
  empty; private refund gate remained absent/false; readiness partial index
  exists. No production schema or financial row was changed by rehearsal.

## Release gate

Only the exact reviewed `main` commit may be built and dispatched. Keep existing
www certificates/static routes, payment/Feishu agents and data intact. Let the
canonical drain monitor stop the old application. Enable reviewed refunds only
through the new helper after that process has stopped. A nonzero refund
readiness result blocks incompatible rollback; retain all ledger/migration
records. Final CI, security, archive provenance, runtime and read-only dialog
results are appended after completion.

### Broad-suite baseline disposition

Independent read-only analysis reran the failures on the clean production base
`ec7dc956d8445b2045dafac53cc49c4281b57e72`:

- `TestRunCheck_QuotaProbeAttachesSnapshotToPrimaryRowOnly`: reproduced the
  unsynchronized shared HTTP capture handler writes.
- Grok scheduler/free-quota test pair: reproduced replacement of a shared
  `sync.Map` while a prior test's async refresh was still accessing it.
- Runtime-snapshot moderation/timezone test pair: reproduced assigning global
  `time.Local` while a surviving worker calls `time.Now`.
- The large WebSocket frame case passed alone on base; its broad-race-run
  timeout is not evidence of a new refund regression.
- `TestResetCardExternalOrderPostgresConcurrentCreateUsesOneLocalOrder`,
  race-enabled and repeated three times on base, reproduced the identical
  `409 RESET_CARD_ORDER_IN_PROGRESS` expectation failure. Its checkout path
  and integration test are unchanged by reviewed refunds.

These are recorded as pre-existing out-of-scope suite defects, not green
results. They do not invalidate the independent financial/API QA or the exact
changed-path race tests. No test assertion was removed, relaxed or skipped to
conceal them. Future fixes should give test fixtures explicit concurrency and
worker-lifetime boundaries; avoid rewriting unrelated runtime systems during
this financial rollout.

### Resumed release preflight

- CI run 34809614036 and Security Scan 34809625153 passed on
  `819daa0163193c1cb691617901cf93f87eea699a`.
- Live preflight found the existing network-isolated payment/Feishu Vault
  agents share the app source label. Independent helper review correctly
  rejected a writer inventory that would misclassify these required sidecars.
  The repair adds a narrowly verified sidecar exception and negative cases
  for a disguised app command, enabled network and extra mount. Root repeated
  the checks and the exact read-only verifier passed for both actual agents.
  Independent follow-up review and final CI remain required before merge.
- Backup SHA-256:
  `772635bbad2fbb1d0849e4cd7ff92c636345c50499f62611800ddf92c20c8284`.
- Order #4-only backfill also passed against that isolated restored backup
  after 245/246: exactly one provenance row, order #3 untouched, original
  wallet/order/expiry hash unchanged, duplicate execution rejected.
  Production backfill has not yet run.
- The production runtime-guard timer remains disabled; installer must preserve
  that existing state. No application or agent lifecycle change has occurred.
