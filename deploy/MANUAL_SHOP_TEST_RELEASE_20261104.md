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

## Completed release

- Source: `76d6158e68369da6a4ca4c27b1321e601a2a83b0`, pushed directly to fork
  `main` after independent QA and code review passed on the same 112-file snapshot.
- Successful explicit workflow:
  `https://github.com/Turtle-Li/sub2api/actions/runs/41802148430`.
  Completed at approximately `2026-11-04T08:58:34Z`.
- Compressed image archive: 93683771 bytes, SHA-256
  `70bbc1a611638e95debdbda0d40b2725260c558b3e8c54d888184777848af63f`.
  Active healthy green image `sub2api:auto-20261104-005750-76d6158e`, zero
  restarts/OOM. Node state reports `traffic=accepting`, green, `background=active`.
  Prior blue remains subject to the canonical drain monitor; no raw stop/restart
  or DNS change was performed.
- Server evidence:
  `/var/log/sub2api-release/gha-20261104-005750-76d6158e-273376/`.
- Fresh verified pre-release database archive:
  `/opt/sub2api-db-backups/sub2api-db-backup-20261104-163501.tar.gz`,
  337981398 bytes; checksum and NAS synchronization marker confirmed.
- Migration `265_manual_shop.sql` applied with the migrator's trimmed-content
  checksum `ec90b174433858c3fff75c410b94f3d9d7152cdf98122082f3c35dd99d02f931`.
  The configuration consumer required this exact ledger checksum, an empty shop
  catalog and a two-product postcondition before committing.
- Enabled products: ID 1, `ChatGPT Plus 代充`, `session_topup`; ID 2,
  `ChatGPT Plus 成品号`, `account_delivery`, guide `chatgpt-plus-account`.
  Both are 10 fen, one month, revision 1 at configuration time. No other shop
  product was present; existing recharge/subscription catalog data was untouched.
- Public smoke: health, shop shell and guide shell HTTP 200; anonymous shop
  catalog, admin catalog and order APIs HTTP 401 with `no-store`. All 13 served
  tutorial images match the committed source hashes; the served JavaScript
  contains the new shop/order/admin routes. SMTP host/from and the HTTPS
  `www.turtleligpt.com` notification-link host are configured; payment and Feishu
  remain enabled through existing runtime injection.

Owner test URLs: `https://www.turtleligpt.com/shop` and
`https://www.turtleligpt.com/admin/shop/orders`. Real checkout, manual delivery,
and external email/Feishu delivery remain owner acceptance checks. No real
purchase, paid-order injection or outbound test notification was performed by
the release executor.

## Payment choices and card refinement follow-up

- Owner-authorized source `0df04a812a86d03e57c78efea470cb603c6ebf74` fixes
  checkout-info compatibility: normally omitted `available` fields no longer
  hide returned Alipay/WeChat methods. Explicit disablement, CNY and amount
  constraints remain enforced. No backend, schema or configuration changes.
- Gemini 4 (`gemini-4-argon`, matching request/response, normal stop/DONE)
  supplied the shop and product card templates. Independent QA/Review approved
  the same six-file snapshot; final shop regression 22/22, payment regression
  27 files/336 tests, build/type checks and scoped lint passed. Actual browser
  captures cover desktop/390-width viewports, light/dark and keyboard selection.
- Explicit workflow
  `https://github.com/Turtle-Li/sub2api/actions/runs/41823520372`
  completed successfully at `2026-11-04T11:56:41Z` on the exact source revision.
  Active healthy blue image `sub2api:auto-20261104-035531-0df04a81`, revision
  `0df04a812a86d03e57c78efea470cb603c6ebf74`, zero restarts/OOM;
  `traffic=accepting`, `background=active`. Prior green was stopped by the
  canonical drain process, without a raw lifecycle command by this task.
- Public health/shop/detail/tutorial return HTTP 200. Anonymous user/admin
  catalog APIs return HTTP 401 with `no-store`. Served application chunks
  `ShopView-bnftO3sZ.js` and `ShopProductView-BWfRdcJE.js` return HTTP 200;
  the detail chunk includes optional availability and single-amount checks.
- Read-only production verification: both shop products enabled at 10 fen;
  Alipay/WeChat enabled and sourced from `unified_alipay`/`unified_wxpay`;
  minimum recharge 0.10 CNY, unlimited maximum and zero recharge fee.
  No real payment or external notification was triggered by the executor.
- Roll back through the canonical helper to the prior manual-shop-capable
  `76d6158e` image if required. Preserve shop/payment data and the two test
  products. This frontend follow-up needs no SQL rollback or backup restoration.
