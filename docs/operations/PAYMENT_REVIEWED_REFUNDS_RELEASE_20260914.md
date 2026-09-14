# Reviewed refunds release — September 14, 2026

## Final state — 2026-09-14

Production blue is healthy on `0bb18347fc9a1a47e04b3a6bd1312235b5938ac6`.
Old green stopped naturally at 16:24:57. The guarded CAS enabled reviewed
refunds at 16:27:16; purchase entry remains closed. Order #4 alone now has
its owner-authorized provenance row and one audit record; no refund attempt
was created and subscription expiry remains November 11. Chrome is left on
order #4's review dialog: ¥0.10, 30 unused days, proposed expiry October 12
23:13:43. The reason is blank and confirmation was not clicked.

CI attempt 2, security, independent source/artifact review and release health
checks passed. The first CI attempt's unchanged reset-card concurrency failure
and broader pre-existing race-suite limitations remain recorded below.
Historical preflight paragraphs describe their observation time; this final
state supersedes their pending-action status.

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

### Final source and installation gates

- Reviewed release source: `e5bf075e51f8737e753f61ce6063c00dfdf7c8c2`,
  fast-forwarded and pushed to fork `main`.
- Final CI: https://github.com/Turtle-Li/sub2api/actions/runs/34815708955
  — success (backend unit/integration, lint, frontend, shell, Docker bind mounts).
- Final security: https://github.com/Turtle-Li/sub2api/actions/runs/34815712360
  — success.
- Final independent helper review: REVIEW_PASS, prior sidecar P2 closed.
  Helper/test repair diff SHA-256:
  `f05c726b7bf22ea132ea7ad5ff48573be254f12e4ba69a21d9182ca8983d3595`.
- Exact-source installer package SHA-256:
  `6a1c2eeb2c84ab51fe5636b8c7d1615d390810a6607ec8c45ae9a3a43241bfa7`.
  Git archive's group-write permissions were rejected before installation;
  root-owned staging files were tightened and the canonical installer succeeded
  at 15:10 CST. Runtime config/Caddy hashes, agent IDs/running state and the
  disabled runtime-guard timer remained unchanged. Six installed controller
  scripts matched reviewed source byte-for-byte.
- Protected source/backup directory on the application host:
  `/var/lib/sub2api-autodeploy/installer-e5bf075e5/`. Prior blue-green helper:
  `/opt/sub2api/backups/blue-green-helper-20260914T071045Z`.
- A newer pre-release backup also passed isolated restore:
  `/opt/sub2api-db-backups/sub2api-db-backup-20260914-151119.tar.gz`, SHA-256
  `26b92f22632aa5c0ce87b5e3e7ae36d739e6fa65e4ad48d37d8a4803c96ee9be`.
  Schema count 299 / unchanged migration hash; Redis snapshot 1796 keys.
- The reviewed order #4 SQL was hardened with subscription/group deletion and
  active-state checks, exact provider-snapshot hash, old-refund-marker checks
  and closed purchase entry. The strengthened SQL passed the isolated backup
  rehearsal again, including duplicate refusal and unchanged financial hash.
  SQL SHA-256 `ac2d69ea704c7313b6e8be6bca5fde1c6ef532a3ae4453d6d585a21784440032`.
  Execution will use the existing application's TLS-verified database
  environment inside its container while the host process holds canonical
  maintenance FD 8; credentials remain inside the application namespace.
- Exact-main build-only workflow:
  https://github.com/Turtle-Li/sub2api/actions/runs/34816525818.
  Artifact and runtime evidence follow after completion.

### Verified image artifact

Build-only run 34816525818 completed successfully on exact `main` release
commit. Root verified GitHub run identity/event/branch/workflow, archive byte
count and compressed SHA-256, single-image manifest tag, config content hash,
linux/amd64 platform, and OCI source/revision/version labels.

- Version: `0.2.4`; archive bytes: `84047528`.
- Compressed archive SHA-256:
  `cca6a4d5f47661ab5a523b90fb6271844b70c489518d70b0981b37267d5b29a5`.
- Archive config ID:
  `sha256:81a577e4ff40e3bb822a7535407fe94c99c1c750fb00a192a23e607e730ec576`.
- Pre-receiver production observation: blue / ec7dc956d, healthy, accepting,
  background active, restart count 0, no OOM.


### First release and embedded-route repair

The e5bf075e image completed the canonical release at 15:25:17 CST, with
app 5xx/fatal/Caddy 5xx all zero; the previous ec7 blue stopped naturally by
15:26:18. Green was healthy/accepting/background active. Release evidence:
`/var/log/sub2api-release/gha-20260914-152254-e5bf075e-3061344`. Migrations
245/246 applied; purchase entry remained closed.

The guarded enable attempt refused the readiness response before CAS: the
embedded frontend returned HTTP 200 HTML for both new internal paths. The
refund flag therefore remained absent, with zero reserved reviewed attempts.
No production refund or provenance backfill occurred.

Repair `0bb18347fc9a1a47e04b3a6bd1312235b5938ac6` adds exact frontend
bypasses and verifies actual common routes behind both embedded middleware
variants. The same regression test fails on e5 with HTML/wrong status and
passes on the repair. CI now builds real frontend assets and runs embedded
route tests and lint. Canonical release/runtime rollback consumers additionally
require valid JSON with ready=true and an integer zero pending count; HTTP
status alone is insufficient. Malformed, HTML and nonzero responses retain
the existing recovery/admission safeguards.

Root validation: embedded web/routes race tests pass, embedded lint zero
issues, both full changed shell suites pass, ShellCheck/syntax/diff checks pass.
CI run 34819950888 and Security Scan 34819954309 were dispatched on the
repair; final results and replacement artifact/runtime follow below.

Independent frozen-change review: REVIEW_PASS, no actionable P1/P2. Repair
diff SHA-256 `e2f3a455465bf43c06b9da21c118fb80f8999ac826c11708a17dfba701ef8026`.
The reviewer independently built frontend assets and passed both embedded
route packages and both rollback shell suites. Security Scan 34819954309
completed successfully.

CI 34819950888 attempt 1 passed frontend (including real embed build/tests/lint),
Docker bind mounts, normal lint, shell and backend unit tests. Integration
failed only the unchanged `TestResetCardExternalOrderPostgresConcurrentCreateUsesOneLocalOrder`
line 267 with `409 RESET_CARD_ORDER_IN_PROGRESS`, identical to the independently
reproduced ec7 baseline failure above. Failure evidence is retained in
`/tmp/sub2-refunds-ci-0bb18347f-failure.log`; one failed-job rerun was dispatched
without changing code or test assertions. A later green attempt does not erase
this baseline-suite limitation.

The repair was fast-forwarded to fork main while the known-baseline failed
job reran. Build-only run 34821069873 targets exact 0bb18347f; this dispatch
does not contact production. The reviewed helper package was separately
installed under the canonical installer lock at 16:12 CST:
`/var/lib/sub2api-autodeploy/installer-0bb18347f`, archive SHA-256
`5985e9b3a6be64a1c6c7afacee86544acd07365b39ffb22df3eed3d8313b6a38`.
Seven controller scripts matched source; configuration/Caddy hashes, credential
agent IDs/running state and disabled/inactive guard timer were unchanged.
Prior helper backup: `/opt/sub2api/backups/blue-green-helper-20260914T081237Z`.
Green/e5 remained the accepting active app. The SHA-pinned order4 wrapper
was staged but not executed.

CI 34819950888 attempt 2 completed successfully, including integration tests.
Exact-main build-only 34821069873 also completed successfully. The replacement
archive has 84,087,554 bytes and SHA-256
`2a13103b5c26c887f4ab18ea74cb24610bfd6a2f23674cd49df19b269c863473`;
archive config ID `aad347b43763494a25e1f229ed9a36d0ee24bfc25401f12aa080b55b9f957e3d`.
Root validated actual compressed bytes, single-image manifest/config hashes,
linux/amd64 and exact revision/source/version labels, plus GitHub successful
main/workflow-dispatch provenance.

### Replacement release

Independent artifact QA passed. Canonical receiver completed at 16:22:56 CST:
`/var/log/sub2api-release/gha-20260914-162059-0bb18347-3099558`.
Blue runs exact 0bb18347f, healthy/accepting/background active, image ID
`sha256:13d81a3c53409932680dd5a798b66b71e4dec170ac6572886f508047be3e4179`;
app 5xx/fatal/Caddy 5xx all zero. Public www/API health returned status=ok.
In-container monitor-authenticated refund readiness now returns the correct
JSON: ready=true and reserved reviewed pending count=0.

Old green is owned by transient drain monitor
`sub2api-drain-sub2api-green-gha-20260914-162059-0bb18347-3099558`.
At 16:23:56 it still had two established connections; enablement remains
blocked until it naturally stops. Read-only SQL after release confirms closed
purchase entry, absent refund gate, orders3/4 completed with zero refund amounts,
unchanged subscription1 expiry, zero provenance rows for3/4, zero order4 refund
attempts and zero backfill audits. No actual refund was performed.

### Final enablement, provenance and user-visible review

Old green reached zero connections and exited at 16:24:57 CST; the canonical
drain log confirmed it stopped. The exact-revision helper completed its
expected-absent CAS at 16:27:16, enabling the private refund gate only after
writer exclusion and all Caddy/runtime proofs passed.

Transient service `sub2api-refund-provenance-order4-20260914-0bb18347f` then
executed the reviewed SQL under the canonical maintenance lock and matching
advisory lock. It committed successfully in 200ms: exactly one order4 grant
for October12–November11, zero reserved/refunded cash/time, zero extra benefit
fields, and one `REFUND_PROVENANCE_BACKFILL` audit. Root-only wrapper SHA-256:
`369b801741d4122b92d6601e8b05e58d1968cdb1c9d156a2e81b5ddfab915d8e`.
The SQL hash remained the rehearsed `ac2d69ea...440032`. No order3 grant exists.

Readback after the transaction and again after opening the dialog is identical:
refund gate=true, purchase entry=false, reserved reviewed attempts=0, order4
refund attempts=0, orders3/4 still COMPLETED with zero requested/refunded cash,
and subscription1 expiry unchanged at 2026-11-11 23:13:43.041622+08.

Native Chrome displayed order #4's server-reviewed quote with used time
0 minutes, remaining30 days, prorated cash ¥0.10 and proposed expiry
2026/10/12 23:13:43. The dialog remains open with blank reason and disabled
confirmation. No confirm, provider refund, email, or notification action was
performed. This proves the live read-only review flow; it does not claim a
real-money refund execution test.

Independent final evidence review: EVIDENCE_QA_PASS. Release/enable/backfill
and before/after SQL logs agree on exact healthy release, ordered activation,
onlyorder4 provenance, unchanged expiry and zero refund activity. The reviewer
validated saved evidence without production access; the visible browser quote
and untouched confirmation are root's direct native-Chrome observation.
