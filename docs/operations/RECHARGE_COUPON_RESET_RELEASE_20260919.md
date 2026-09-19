# Recharge, payment coupons and reset-card release — 2026-09-19

## Scope and owner decisions

The owner authorized production deployment of the compact recharge/subscription
UI and payment discount coupons, then reported reset-card checkout failure and
requested direct reset-card purchase navigation from My Subscriptions. An
interim CNY50,000 / 500 daily receipt proposal was explicitly superseded:
**Sub2 live collection has no daily amount or count limit**. Discounted payable
amounts round up to whole yuan. Coupons remain distinct from balance-gift promo
codes and are managed under `/admin/orders/coupons`; reset-card orders cannot
use them.

The release includes monthly-first/default Plus selection, aligned subscription
period controls, USD quota descriptions derived from group data, configurable
reset-card benefit copy, compact balance cards, server-authoritative coupon
scope/plan/user/expiry/usage limits, concurrency reservations, order/coupon
traceability, checkout quote/confirmation, and OAuth restoration. Refer to the
feature implementation/QA records for the complete financial contract.

`docs/bugs/BUG-20260919-reset-card-create-rejection.md` documents the observed
failure and repair: only a fresh definite rejection releases the creation fence;
prior uncertainty still uses the original order/key. My Subscriptions now shows
renewal and reset-card purchase together, and purchase deep links focus/highlight
the matching reset offer without automatically creating an order.

## Source, validation and target

- Target: authoritative SSH alias `sub2api-candidate` (Azure), `/opt/sub2api`;
  database alias `sub2api-db`. The expired old origin is not a deployment target.
- Previously serving source: `04a95a7b23574b5c56efb5460eeaf07729ee9af8`.
- Release source: `95075af97372ea2bcc572125ba55df13dfbbedc5` on
  `Turtle-Li/sub2api/main`. Includes safe integration of concurrent main work,
  coupon implementation, reset fix/shortcuts, and six strict errcheck corrections.
- Independent reset review passed; backend reset and unifiedpay tests, 78
  focused frontend tests and 3 i18n tests passed. Desktop and 393×852 browser
  journeys passed using local fixtures; no real order was created.
- Full CI at `396d3bee2` passed frontend, unit/integration, shell and Docker
  checks but failed six coupon errcheck omissions. The final commit explicitly
  handles Builder returns and uses a checked type assertion; coupon regressions
  passed locally. Final CI run `35433103805`, security `35433103697` and candidate
  build `35433103481` track the exact final source. Execution status follows.

Use only the canonical `sub2api-production-deploy.yml` explicit workflow,
verified image receiver and maintenance-lock/blue-green path. Do not compile
on production, recreate credential agents, change public purchasing flags,
overwrite live catalog examples, or enable a real model request probe.

Latest pre-release backup:
`/opt/sub2api-db-backups/sub2api-db-backup-20260919-165120.tar.gz`.
The database-host canonical backup succeeded; isolated restore verification is
running. This is a newer backup than the initial 15:49 preflight and includes the
subsequently observed reset-card order. No production restore is involved.

## Central payment companion — deployed

Central release `20260919-unlimited-daily` and forward migration20 completed at
08:48 UTC. Exact runtime ID
`sha256:d1d24599da8ab2be2750283d8ddf775af25cb97dde4f20840ce4d225e48fbdbd`,
PostgreSQL ID
`sha256:63626e9c0a585babed4cecf7a6b9d9b2ee1f2c45ff49e9c8aa0069f5c8ebdb0f`.
Alipay/WeChat Sub2 live bindings both have daily count/amount0 (unlimited),
QPS1/refund20fen unchanged, enabled true/emergency false. Two append-only audit
events record the CAS change. All services and four original credential agents
are healthy; pre/post encrypted backup restore checks passed.

Authoritative details and rollback boundary are in the central project document
`docs/operations/SUB2_UNLIMITED_DAILY_COLLECTION_20260919.md`. Never run an old
central API that interprets zero as a ceiling against these configured bindings.

## Sub2 rollout status

Pending final CI and canonical production workflow. No actual payment/refund
acceptance is claimed; the owner will test the released checkout later.

Rollback preserves the new coupon ledger and audit tables. Before the candidate
receives traffic, the canonical failed-health rollback may retain the previous
app image. After real coupon orders exist, do not assume a coupon-unaware old
binary is safe: quiesce payment writes and reconcile reservations/fulfillment,
or use a forward repair. Never clear usage counts, coupon reservations, payment
orders, idempotency fences or financial evidence to make rollback succeed.

Latest backup SHA-256:
`29cfd1dd5f5d1aab0ba719ce4e4274583b962eb8a8ab042e791b191f52ea15d2`.
The actual isolated restore passed PostgreSQL restore, Redis load and inner/outer
checksums: schema count309, schema hash`3b4bc5bcdcdde3981769463d10d36187`, Redis
snapshot3927 keys. Final source Go lint and frontend checks passed.

## Production result — PASS, 2026-09-19 09:06 UTC

Final CI `35433103805` and security scan `35433103697` both passed at release
source `95075af97372ea2bcc572125ba55df13dfbbedc5`. GitHub build-only run
`35433103481` produced the verified `linux/amd64` version0.2.5 artifact. The
local artifact transfer was slow; the same authenticated GitHub artifact was
instead downloaded directly to the Azure candidate, with its signed URL only
in process memory/stdin, never in logs, argv or a configuration file. Both
outer artifact and inner Docker archive hashes matched before release.

- GitHub artifact ZIP: 84,591,232 bytes, SHA256
  `70e8ec7c3bedb532bc3ad713e189b40aab47b5463de83f6020ef158d6d18bf55`.
- Docker archive: 84,590,413 bytes, SHA256
  `e104c666bcbdc2e88af677dcdd2728a836109f7abaeff994e941aeeff4e6f4d8`.
- Canonical installed receiver SHA256
  `8ccb62ae77776ff5f3298b3dc5ff0b3599ac5dc344db649f8f68f4a439f941d8`
  matched the reviewed source. It retained its normal maintenance lock,
  identity, blue-green, health and recovery gates; no host compilation occurred.
- Active container `sub2api-blue`, image
  `sha256:c63457a8e8e14603c282c3d6775cada4c1286991ff42715cca38e1f284782dd0`,
  tag `sub2api:auto-20260919-170637-95075af9`, revision exactly95075af97372ea2bcc572125ba55df13dfbbedc5.
- Blue is healthy with restart0 and background active. The canonical drain
  monitor handles the former green generation. Both existing Sub2 credential
  agents retain their original Sep9/Sep10 creation times and are healthy.
- Release counters: app_5xx0, app_fatal0, caddy_5xx0. Server evidence:
  `/var/log/sub2api-release/gha-20260919-170637-95075af9-2729718`.
- Database migration count309→311; coupon code/usage/audit/rate-limit tables
  and plan_ids column exist. `payment_enabled` and `payment_entry_enabled`
  remain true. No example catalog overwrite was performed.
- Public health/purchase/subscription endpoints return200 with a browser UA;
  purchase loads new asset `/assets/index-CobePtz4.js`. A real browser reaches
  the normal login screen with `/subscriptions` preserved as redirect.
  The bare Python default UA received403, while browser/Mozilla requests worked;
  no edge security setting was changed.

No new real checkout/payment/refund or manual financial-state rewrite was made.
Production deployment and read-only smoke pass; actual paid reset-card and
coupon checkout acceptance remain for the owner's later test.

Final HTTP checks also passed: `/admin/orders/coupons` serves the SPA (200),
and unauthenticated `/api/v1/payment/checkout-info` and
`/api/v1/admin/payment/coupons` both return401. No authentication bypass or
production coupon edit was used for smoke testing.
