# Manual shop test release — 2026-11-04

The owner authorized submission, an explicit production release, and exactly two
enabled shop products for testing: ChatGPT Plus top-up and ChatGPT Plus account
delivery, each CNY 0.10 for one month. Existing API recharge and subscription
catalogs are outside this configuration change. The owner performs real checkout
and manual fulfillment acceptance.

## Access and operations boundary

- Application: the configured `sub2api-aws-candidate` SSH alias, root-owned
  `/opt/sub2api`, release configuration `/etc/sub2api-autodeploy.env`, fork
  `Turtle-Li/sub2api`, branch `main`, release mode `activate`. The read-only
  preflight found healthy blue revision `3008402d56f1ec53a1b72704b3d95789d7813b23`,
  zero restarts/OOM, and persistent TOTP encryption plus payment/Feishu enabled.
- Data: the configured `sub2api-db` SSH alias is Registry host
  `srv-jp-sub2api-db`, dedicated PostgreSQL/Redis under `/opt/sub2api-migration`.
  Use the approved device-local SSH identity; do not copy keys or passwords.
  Existing containers are `sub2api-migration-postgres` (PostgreSQL 18) and
  `sub2api-migration-redis-public`. Backups belong to this host, under
  `/opt/sub2api-db-backups`, via `sub2api-db-backup.service` and
  `sub2api-db-nas-sync.service`; existing daily timers remain enabled.
- Follow `deploy/README.md` and the digest-bound GitHub production workflow.
  Its concurrency group and the receiver's shared maintenance/upload locks
  serialize releases. Do not compile source on production or issue raw
  application-container lifecycle commands.
- Migration 265 is additive and seeds no products. Preserve its tables and all
  payment/fulfillment/notification data on rollback. Disable the two shop
  products before an application rollback; reconcile any paid manual orders
  before exposing an older application that lacks the manual-shop contract.
- Product configuration is reversible operator data. If the owner is not
  interactively signed in, use a bounded PostgreSQL transaction over the
  approved data-host SSH identity, scoped exclusively to `shop_products`,
  with an empty-catalog precondition, integer `price_fen=10`, and a two-enabled
  product postcondition. Do not fabricate an administrator JWT, retrieve a
  human password, inject a paid order, or alter existing payment catalogs.

## Source and verification

Business source hashes: `.omo/evidence/manual-shop/session-release/source-manifest.json`.
Independent QA and Review live under their respective `session-release`
evidence directories. Session checks use synthetic fixtures; the real owner
example is excluded from source, logs, artifacts and third-party requests.

Release commit, workflow, backup, product IDs and live smoke results are to be
recorded after the authorized release completes. No real purchase is performed
by the release executor.
