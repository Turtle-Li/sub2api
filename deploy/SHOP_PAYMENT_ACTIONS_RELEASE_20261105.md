# Shop payment actions and DeepSeek day-pass release

Owner authorized generic payment hooks, actual Claude review and optimization,
then instructed completion tonight. The reviewed feature is committed and
pushed directly to the owner fork/main. This note records the production
native-adapter rollout; no real payment was executed for QA.

## Source and artifacts

- Application revision: `f03ed1a3854aa13c9a21cf1d8547a3f39243e897`.
- Version: `0.2.21`; source: `https://github.com/Turtle-Li/sub2api`.
- GitHub Actions run: [41915487576](https://github.com/Turtle-Li/sub2api/actions/runs/41915487576), build-only, Linux/amd64, successful.
- Archive:94151552bytes, SHA256 `92f07c65918f3a0704469605a175e6372307fa9c5053a009d517dae4c250eb60`.
- Loaded image: `sha256:3c4379334a4185329090df8561d4b3ff8daee3f1fd64c0250bf4df2479df4eb9`.
- Final active tag: `sub2api:auto-20261104-134213-f03ed1a3`; Caddy selects `sub2api-blue`.
- Additive migration266: `266_shop_order_actions.sql`, SHA256 `37da0b61adb617e412af07d7e53b84551dc06c79b9e4fab6bcdee002a3c89cbd`, verified applied on production PostgreSQL.

No source checkout, dependency install or application compilation ran on the
production host. The image receiver verified archive digest, platform, full
revision and source labels before the canonical blue-green helper ran.

## Runtime boundary and ordered activation

Use project authority in deploy/README.md and deploy/AWS_CANDIDATE_20260923.md.
Only approved `sub2api-aws-candidate` at `/opt/sub2api` is the application target;
`sub2api-db` is the separate existing PostgreSQL/Redis host. Azure and historical
retired production targets are excluded.

1. Stage the global/integration switches disabled and keep product3 disabled.
2. Install the reviewed canonical helpers with `--install-blue-green-helper
   --no-enable-runtime-guard`; keep both release/guard timers disabled.
3. First release `gha-20261104-133425-f03ed1a3-959223` selected green at
   2026-11-04T21:34:51Z. Its checks reported healthy/activate and zero app5xx,
   appfatal and Caddy5xx. Let the old `f198b838` generation finish draining.
4. The maintenance-locked enable helper verified every running application had
   the exact f03 revision and healthy state. Protected config backup:
   `/opt/sub2api/staging/shop-actions-config-enable-20261104T214143Z`.
5. Second canonical release `gha-20261104-134213-f03ed1a3-974430` selected blue
   at2026-11-04T21:42:37Z, loading enabled static YAML. Same verified image;
   healthy/activate and zero app5xx/appfatal/Caddy5xx. Any draining color now
   has the same action-aware revision. No active WebSocket was force-closed.
6. Save the versioned native binding, then enable product3 last through the
   existing owner admin UI. The persisted product is enabled, revision3.

Before activation, the latest verified PostgreSQL backup was
`/opt/sub2api-db-backups/sub2api-db-backup-20261105-031632.tar.gz`,338082718bytes,
with successful NAS sync. Protected helper backup:
`/opt/sub2api/backups/blue-green-helper-20261104T211727Z`; initial staged config:
`/opt/sub2api/staging/shop-actions-config-stage-20261104T212211Z`.

Native integration `newapi-domestic@1`, adapter `newapi.daypass.v1`, targets
`http://100.108.15.49:17867`. Only AWS Tailnet source100.121.157.55 is allowed.
The dedicated `sub2api-daypass-vault` agent is healthy, networkless and holds
only `vault://secret/data/ops/newapi/daypass#bearer` in memory. The application
mounts volume `sub2api_daypass_vault` read-only at `/run/sub2api-daypass-vault`.
The daypass bearer is absent from application environment and argv. Re-inject the agent
through the approved Vault consumer after its restart; an unready integration
blocks new checkout. Human website passwords were never collected or stored.

## Native receiver and customer behavior

New API guard.4 runs independently on the approved domestic WorkBuddy host,
with public `https://ds.turtleligpt.com` through the accepted Cloudflare Tunnel.
Image `sha256:c59c715f3f96e1e1bd4a1e416f2ad7f2f4564f3f6f3f6ff2b9d9effe1bca3a8f`.
Its separate non-root control nginx unit starts after tailscaled; it is not
included in shared nginx.service. All public80/443/Tunnel control-prefix
locations return404 with `X-Daypass-Control-Boundary: isolated`. Pre/post-upgrade
marked probes and exact-source private authenticated Ready passed. All15 sibling
container identities were preserved.

Full AGPL corresponding source is available from
[guard.4 source](https://ds.turtleligpt.com/source/new-api-daypass-guard.4.tar.gz),
verified public200/7064258bytes, SHA256
`591f3c4e491f6cb5b3f96de2e2025e188c4fe95daecf172345c41e70cfec107c`.

- Product3: DeepSeek24hour day pass,600fen/CNY,1day, `account_delivery`, guide
  `deepseek-daypass`, binding `primary-delivery`.
- Parameters: `duration_seconds=86400`, `default_concurrency=2`.
- Each trusted paid order receives its own key and native subscription. Both
  expire at original `paid_at+86400`; retries preserve receipt/key/expiry.
- Default2 concurrent generations per key, independently adjustable1..32 in
  New API API Keys → Shop day-pass keys. Same canonical IP during generation
  plus15min after successful completion; third overlap429, other IP403.
- Models: `deepseek-v4.1-flash`, `deepseek-v4-pro`; unlimited total tokens,
  subscription-only, zero wallet fallback. Keep nonzero model prices.
- Buyers use their existing Sub2 order to view the encrypted delivery and
  instructions. No New API customer registration/login; generation does not
  pass through the Sub2 application.

Shop: https://www.turtleligpt.com/shop/products/3 . Guide:
https://www.turtleligpt.com/shop/help/deepseek-daypass . Owner Key management:
https://ds.turtleligpt.com/keys . Human sign-in stays owner-interactive.

## Review and acceptance

Actual configured Claude reviewed the design, implementation and final release
scripts. Adopted changes include immutable versioned integrations, same-transaction
outbox, idempotency and durable compensation, strict HTTP response/SSRF rules,
full public-server denial coverage, independently started control listener,
marked proxy probes, bounded rollback and automatic-delivery notification text.
Final incremental release review: PASS. Raw review transcripts and screenshots
are retained in local ignored operations evidence rather than the public repo.

- Merged Shop/payment/Feishu race/shuffle unit suite: PASS18.134s.
- Frontend:111tests across5files PASS; full vue-tsc/Vite build PASS14.34s.
- Final automatic/manual-projection and notification race regressions: PASS9.752s.
- Earlier real PostgreSQL integration and affected config/handler/router race
  suites passed; local browser acceptance covered the changed UI.
- Native deployment regressions:4PASS; isolated unit verification passed.
- Live no-payment native issuance replay preserved key/receipt/original expiry;
  Flash200, Pro200, two complete SSE streams and third429, actual other-network
  `account_ip_in_use`403; expired clock410 and revoke-before-issue410.
- Native synthetic receipts901793827537/539 remain revoked tombstones; token22
  is disabled and no probe subscription remains active. Do not erase the audit.
- Real owner UI created unpaid checkout68 at6yuan and cancelled it. Database
  state is CANCELLED, paid_atNULL, fulfillment WAITING_PAYMENT and zero action
  tasks. This verifies the actual application/agent/private Ready checkout path.
  No Sub2 PAID row was inserted or fabricated; no real payment occurred.
- Published product/admin/guide and cancelled-order screens were checked in
  the real browser. Native public management correctly requires human sign-in;
  the owner's expired private UI session was not bypassed or password-recovered.
- Post-release daypass agent healthy; no shop action recovery warning observed.

## Pause, recovery and future integrations

Disable product3 first to stop new sales. Pause new action execution through
the reviewed deployment configuration while retaining the registered original
integration, receipts, exact revocation and encrypted delivery history. Use
original-task retry and original native-grant revocation through current
step-up-protected admin operations. Refunds remain the existing manual approved
process; revoke an issued grant before refunding. A retry never purchases or
issues a second key and never extends the original expiry.

After enabling automatic sales, do not promote a binary that cannot understand
payment actions, and do not restore a pre-release database. Recover with this
action-aware revision or a reviewed compatible successor. The old image and
protected backups are retained as evidence, not blanket rollback permission.
Keep timers in their existing disabled state; any later recovery automation
must retain these version/mount/fence constraints.

The reusable HTTP adapter and product binding are implemented. Enabling a new
external HTTP integration additionally requires its own approved isolated
signing-secret agent/socket mount and receiver acceptance under
docs/SHOP_PAYMENT_ACTIONS.md. No generic receiver, arbitrary endpoint or shared
payment-agent secret access was enabled by this native day-pass release.
