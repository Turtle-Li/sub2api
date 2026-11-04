# Shop feedback corrections — 2026-11-05

The owner requested tracked fixes after testing the published manual shop flow.
The continuing publish-and-test authorization covers these corrections. The
15-item result is in [the feedback checklist](../docs/operations/SHOP_FLOW_FEEDBACK_20261105.md).

## Result and source identity

- Deployed source: `f198b83832146b136458f727b36eacc7def2c0c9`, version `0.2.21`,
  owner repository `Turtle-Li/sub2api`, exact `main` at dispatch. Baseline:
  `6074cfd9568e538a13617d868be9037810d0e353`. Later record-only commits do not
  change the deployed source and do not trigger another release.
- Explicit canonical workflow [41902609251](https://github.com/Turtle-Li/sub2api/actions/runs/41902609251)
  completed successfully at `2026-11-04T20:11:32Z`. Image deployment completed
  `20:10:57Z`; verified AWS Caddy activation completed `20:11:01Z`. The record
  uses the task date; these are exact UTC timestamps (the following day in CST).
- Active healthy blue image: `sub2api:auto-20261104-121024-f198b838`,
  `linux/amd64`, matching OCI source/revision/version, restart count zero,
  OOM false. Node: `traffic=accepting`, `active_container=sub2api-blue`,
  `background=active`; explicit state files show blue active, green standby.
- Compressed archive: `93681834` bytes, SHA-256
  `51fcadd199c105e8e69f33173939dc481057c7b498f2ff8dfe54d584cb8df4d3`.
  Loaded image ID:
  `sha256:3c87187b9fe4a0b41de453c55654f011571fd73379193192f4932e691bf0a32a`.
  The archive digest is the transport identity; local daemon IDs are diagnostic.
  Receiver evidence:
  `/var/log/sub2api-release/gha-20261104-121024-f198b838-874827/`.

## Behavior and verification

Cancellation stays in the existing purchase/payment surface. Embedded and
dedicated popup returns render compact, server-confirmed status. Each popup
owns an exact sessionStorage checkout binding across provider COOP navigation;
absent or invalid context cannot choose another globally stored paid order.
Normal top-level recovery, fixed registered ReturnURL, signatures, authoritative
payment facts, refund and concurrency contracts remain intact.

Buyer Session instructions have dark/light padding and a bounded Session link;
long JSON wraps without reserializing the original input. One Submit performs
the original server check and save with stale-request guards. Admin uses Copy
Session with inline Copied feedback and unchanged step-up/version protection;
confirmation prose no longer exposes implementation versions. Delivery actions
match existing compact peers and require admin-only batch-projected eligibility.

Feishu titles distinguish recharge input/payment and account fulfillment.
Manual shop orders no longer produce automatic paid-incomplete or resolved
false alarms; historical incorrect pending deliveries are silently suppressed.
Successful same-incident notices are spaced at least 20 minutes while other
real orders, automatic entitlement failures and financial review remain eligible.
No schema migration, credential, product or payment-configuration change.

Independent functional QA and final code Review passed on the same final source.
The previous freeze passed 181 affected payment tests and six independent
counterexamples. Final two-file compatibility correction passed 50 component
tests plus independent omitted/true/false and old-caller integration checks.
Go service/admin, real PostgreSQL 16 notification cooldown/history, missing-task
eligibility and both TLS SMTP modes passed independent QA. Types, scoped ESLint,
i18n 3/3 and the production build passed. The 30-file source manifest SHA-256 is
`a906d87352afb63110fda227b49ca0c413419d421aa74c7cc0af058a84455bdd`.

Earlier failed QA and Review records were retained: wrong-order popup recovery,
missing randomUUID, optional recovery-cache failure and Vue Boolean-default
compatibility were fixed and independently rechecked before publication.
Two-pass visual review inspected 36 actual CUA JPEGs. Evidence does not include
a recreated before matrix, every mobile-light bottom, cancelled admin row or
delivery-focus screenshot. Synthetic provider COOP tests do not establish real
Alipay settlement. Later compatibility corrections changed no production CSS.

Online checks passed: both public health endpoints and shop/detail/HTML-guide
pages (six total), four anonymous buyer/admin API boundaries (401, no-store),
15 affected JavaScript chunks and 13 guide-image hashes against the verified
build/source. The entry is `/assets/index-BcKFjRBI.js`; the extracted
`manualShopPayment` helper is included in the comparison. The smoke harness
initially omitted two chunk selectors; selectors were corrected without
changing product code or weakening the hash/marker assertions. Actual browser
navigation to the shop reached the expected sign-in page.

Post-release bounded read-only catalog verification again found exactly two
enabled products, IDs 1 and 2, recharge and account delivery, each 10 fen for
one month, revision 1. No real payment, Session submission, manual delivery or
test Feishu/email message was issued by the executor. Synthetic fixtures,
screenshots, `.omo/`, `data/` and local preview files were not committed or shipped.

## Completion mail evidence

Production order 65 completion mail is recorded SENT, one attempt, at
`2026-11-05T02:51:41.209102+08:00`. Payment and fulfillment are completed;
the obsolete-job skip does not explain this record. The deployed send path
returns success only after SMTP DATA acceptance. This supports SMTP acceptance,
not final inbox delivery. No sending-side failure was evidenced, SMTP settings
were not changed, and the already-SENT message was not resent.

## Backup and rollback

Follow project `AGENTS.md`, `deploy/README.md` and AWS migration/candidate
records. Approved operator aliases remain `sub2api-aws-candidate` for the
root-owned `/opt/sub2api` app and `sub2api-db` for the dedicated data host.
Retired Azure and expired `sub2api-new` are excluded.

Existing automatic archive
`/opt/sub2api-db-backups/sub2api-db-backup-20261105-031632.tar.gz` is
`338082718` bytes; fully recomputed SHA-256
`2a3f1908e62ec7acd738c99043189b698e80c5f71a8cab50f8fe0c417167298b`.
Backup service exited success at `03:17:48 CST`, NAS sync at `03:41:34 CST`,
and the exact archive's `.nas-synced` marker exists. Timers and retention remain
unchanged; no manual backup, restore or production SQL write was performed.

Previous healthy green image `sub2api:auto-20261104-091056-e77a4d39`, source
`e77a4d39118713478e9b456de931c221cdcc89ed`, is retained as the compatible
manual-shop rollback generation in standby. Use only installed canonical
drain/readiness/maintenance-lock helpers; preserve all orders, monetary facts
and intentional notification audit suppressions. No raw container restart,
Caddy lifecycle, DNS change or parallel release bypass was used.

Owner acceptance: [shop](https://www.turtleligpt.com/shop) and
[manual shop orders](https://www.turtleligpt.com/admin/shop/orders).
