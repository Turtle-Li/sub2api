# Manual shop UI and local Session preview — 2026-11-05

The owner explicitly authorized publishing the reviewed local shop candidate and
testing the full flow in production. Scope: the approved compact product cards,
Kimi-designed subsequent buyer/operator/guide interfaces, and offline Session
preview on paste. This release changes no backend, API, database migration,
payment configuration, credential, product price/duration, DNS or background
policy. Read-only production checks before and after deployment confirmed the
two existing Plus products remain CNY 0.10 for one month.

## Release contract

- Source repository: `Turtle-Li/sub2api`, `main`; ordinary pushes do not deploy.
- Target: the documented `sub2api-aws-candidate` operator SSH alias. Follow
  `deploy/README.md`, `AGENTS.md` and `AWS_MIGRATION_RUNBOOK.md`; retired Azure
  and expired `sub2api-new` hosts are excluded.
- Use the exact-main `.github/workflows/sub2api-production-deploy.yml` dispatch
  with `deployment_target=aws-candidate`, `build_only=false`. Its concurrency
  group and installed receiver maintenance/upload locks serialize deployment;
  do not replace these with a parallel manual image/container/Caddy operation.
- Read only allowlisted release config fields and node status during preflight.
  `sub2api-autodeploy.sh --check` is not a read-only host check and is not used.
- Preserve existing protected runtime/Vault injection. No raw credentials or
  user Session samples enter source, logs, screenshots or release artifacts.
- Do not ship the untracked local preview harness, `.omo/`, `data/`, provider
  generation output or screenshots. Independent checks bind to the committed
  production source, and GitHub builds from a clean checkout of that revision.

## Verification and stop/rollback boundaries

Developer evidence before release freeze: seven affected frontend test files,
140 passing tests plus one added buyer-refresh regression;
build i18n checks 3/3, Vue type/build and scoped ESLint passed. Actual browser
inspection covered buyer states, admin queue/processing/delivery/notifications,
guide, product editor and offline Session preview with synthetic data. Repeat
applicable independent QA/Review on the frozen candidate before pushing it.
The final candidate also restores the pre-existing buyer status-refresh control
through its unchanged order-load function; it introduces no polling or write.

Require a healthy active source, accepting traffic and active background owner
before dispatch. GitHub verifies compressed archive digest, loaded platform and
OCI source/revision/version; the canonical receiver performs health, protected
real-request, edge route and blue-green checks. Failed identity/probe/traffic
checks must retain the serving generation; never force a raw lifecycle change.

After release, verify workflow conclusion, active source/image/health, node
state, public shop/detail/guide pages, exact served changed chunks, and protected
anonymous API responses. Use the existing database-host path only for bounded
read-only product/backup verification. Real owner purchase, Session submission,
manual recharge/delivery and external email/Feishu delivery remain acceptance
checks; the executor does not fabricate paid orders or send test notifications.

Rollback uses the retained previous manual-shop-capable image through canonical
drain/readiness/maintenance-lock helpers. This frontend-only change needs no SQL
rollback, backup restoration, or modification of shop/payment/notification data.
Record exact source, workflow, artifact, active slot and rollback provenance
only after the canonical workflow and online checks succeed.

## Completed deployment and online verification

- Deployed source: `e77a4d39118713478e9b456de931c221cdcc89ed`, version `0.2.21`.
  Exact main source was pushed after independent QA and Review approved all
  18 manifest files. Manifest SHA-256:
  `f6f06d7e494eab1c65cf4730a1e48630da75780bd9f6ae1133e1aead323ddafb`.
- Independent QA passed 141 tests and the broad buyer/admin/browser matrix on
  the preceding freeze. The final four-file correction passed 51 affected tests,
  build/types/i18n, lint and four desktop/mobile queued-order scenarios with
  both update permissions. Final Review is CLEAR/APPROVE, no outstanding
  findings. The queued account summary stays visible above the collapsed
  editor, and the buyer status refresh uses its existing read-only order load.
- Explicit production workflow succeeded:
  <https://github.com/Turtle-Li/sub2api/actions/runs/41873177845>.
  Image deployment finished `2026-11-04T17:11:31Z`, Caddy activation
  `2026-11-04T17:11:35Z`, workflow completion `2026-11-04T17:11:57Z`.
  These are captured UTC timestamps; the release record uses the task date.
- Compressed archive: 93702298 bytes, SHA-256
  `f060c25ffac2ccea7d65c918109e7714b6505239620d630a65264cefe0650abb`.
  Runner/loaded image ID:
  `sha256:abac8bea502f2c0a3d349b9cd3ac9d8aa9111de08e5da34ad53ffa53b53aab70`;
  archive config ID:
  `sha256:ae0995be410e6131f4ead80a40bea646293c02b8f21abd218e4327e3ebf0f631`.
  The archive digest is the transport identity; daemon-local IDs are diagnostic.
- Active healthy green image `sub2api:auto-20261104-091056-e77a4d39`, exact
  source label, zero restarts and no OOM; node state is `traffic=accepting`,
  `active_container=sub2api-green`, `background=active`. Server evidence:
  `/var/log/sub2api-release/gha-20261104-091056-e77a4d39-713606/`.
  Prior blue revision `0df04a812a86d03e57c78efea470cb603c6ebf74`, image
  `sub2api:auto-20261104-035531-0df04a81`, remains the manual-shop-capable
  rollback through the installed canonical drain/readiness helpers. No raw
  container lifecycle, Caddy, SQL rollback or backup restoration was issued.
- Fresh pre-release DB archive:
  `sub2api-db-backup-20261105-004022.tar.gz`, 337962043 bytes, fully recomputed
  SHA-256 `5521398a8c398bdc0648af23c71e2045f24b44a3c92f7482125c442cb3402487`.
  Existing NAS sync completed successfully (`Result=success`, exit status 0,
  `2026-11-05 00:50:48 CST`); daily timers were preserved.
- Public `www`/`api` health, shop, both product details and HTML tutorial return
  HTTP 200. Anonymous buyer/admin catalog and buyer order APIs return HTTP 401
  with `no-store`. Entry `/assets/index-BWMCaw1g.js` references the new shop
  assets; all nine shop/admin/order JavaScript chunks and all 13 local tutorial
  images match the verified build/source SHA-256 values. No preview harness or
  synthetic Session fixture is present in these served chunks.
- Actual in-app browser navigation to the shop reaches the expected sign-in
  screen in an unauthenticated session. No administrator JWT, human password,
  real Session, paid-order fixture or external test notification was used.
  Production read-only catalog verification again found exactly IDs 1 and 2,
  enabled, 10 fen, one month, revision 1, top-up and account delivery respectively.

Owner acceptance: <https://www.turtleligpt.com/shop> and
<https://www.turtleligpt.com/admin/shop/orders>. The owner tests real payment,
Session submission, manual recharge/account delivery, and receipt of email and
Feishu notifications. The release executor verified deployment and anonymous
boundaries; it does not claim end-to-end settlement or delivery acceptance.

Private operations checkpoint:
`infra-ops-private/projects/sub2api-shop-ui-release-20261105.md`.
