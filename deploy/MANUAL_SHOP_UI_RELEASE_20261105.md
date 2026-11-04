# Manual shop UI and local Session preview — 2026-11-05

The owner explicitly authorized publishing the reviewed local shop candidate and
testing the full flow in production. Scope: the approved compact product cards,
Kimi-designed subsequent buyer/operator/guide interfaces, and offline Session
preview on paste. No backend, API, database migration, payment configuration,
credential, product price/duration, DNS or background-policy change is planned.
The two existing Plus products remain CNY 0.10 for one month, subject to a
read-only production configuration check.

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

Developer evidence: seven affected frontend test files, 140 passing tests;
build i18n checks 3/3, Vue type/build and scoped ESLint passed. Actual browser
inspection covered buyer states, admin queue/processing/delivery/notifications,
guide, product editor and offline Session preview with synthetic data. Repeat
applicable independent QA/Review on the frozen candidate before pushing it.

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
after successful deployment; do not mark this preparation record as completed.
