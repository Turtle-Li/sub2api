# Sub2API project operations

## Payment catalog and reset-card purchases

- For the owner-only direct-link payment test, read
  `docs/operations/PAYMENT_DIRECT_TEST_ENTRY_20260912.md`. The independent
  `payment_entry_enabled` hides discovery only; server-side card audiences
  enforce purchase access. Enable payment last after restricted cards are ready,
  and disable payment before removing the last enabled recharge preset.

- Latest recharge-card refinement and closed-checkout release: read
  `docs/operations/RECHARGE_UI_LOCAL_REVIEW_20260912.md`. Keep ordinary
  purchasing closed until the owner completes real-flow testing.

- Recharge UI follows the subscription-card visual system; read
  `docs/operations/RECHARGE_CARD_STYLE_20260912.md` for the current layout,
  accessible content and completed single-origin release.

- Read `docs/operations/PAYMENT_CARD_ELIGIBILITY_20260912.md` for configurable
  card audiences, net paid recharge prerequisites and monotonic concurrency
  benefits. Public DTOs must omit private rules; order admission is server-owned.

- Read `docs/operations/PAYMENT_CATALOG_REFINEMENT_20260912.md` for the later
  GPT-only checkout/reset policy, configurable reset prices and revised bonuses.

- Read `docs/operations/PAYMENT_CATALOG_RELEASE_20260911.md` for the owner-approved
  Plus/5X Pro products, integer recharge bonuses, reset-card wallet purchases,
  and the September 11–12 customer-checkout release. Its explicit enablement
  replaces the older test-only checkout restriction below after release gates.
- Recharge stays fixed to six catalog amounts; do not impose its 599 ceiling
  on subscription method limits. Preserve purchase idempotency and financial
  records; group multipliers remain owner-managed.

## Order operations and invoices

- For order display, purchase snapshots and manual invoice requests, read
  `docs/ORDER_OPERATIONS_20260910.md` and `docs/INVOICING.md`. Payment and
  entitlement delivery are independent persisted facts; never rewrite financial
  states to change a badge. Preserve historical snapshots and invoice evidence.
- Keep customer order history and invoice processing accessible when new
  checkout is disabled. Reuse existing SMTP and Feishu credential injection;
  never expose tax identifiers, email recipients or PDFs in bot notifications.

## Internal credit denomination

- Read `docs/PRICING_CURRENCY_20260912.md` before changing wallet denomination
  or model/group pricing. Owner confirmed existing USD wallets convert to CNY
  at 6.75, with matching debit conversion and unchanged discount multipliers.
  Subscription entitlements remain USD. This supersedes the no-conversion
  decision in `docs/CREDIT_PARITY_MIGRATION_20260910.md`. A settings save does
  not migrate data. The owner later confirmed 1:1 future CNY recharge and
  uninterrupted API use with temporary undercharging allowed. Follow
  `deploy/currency-migration/ONLINE_CUTOVER_20260912.md` for cache bypass,
  obsolete-writer exclusion, verified backups and the online transaction.
  The live conversion completed on September 12; read
  `docs/operations/CNY_WALLET_CUTOVER_20260912.md` for the manifest, probes and
  compatible fallback. Do not rerun migration SQL or restore the old USD DB.

- Later owner correction: standard OpenAI/Codex CNY wallet billing uses the
  original USD reference price directly times the existing group multiplier;
  rate 0.25 means ¥500 buys $2000 reference usage. No extra FX or new setting.
  Read the correction in `docs/PRICING_CURRENCY_20260912.md`; subscription groups
  and other platforms retain the initial rules. Deploy compatible code and
  drain old binaries before raising the group rate; rollback must preserve this
  code/rate pairing. Never convert existing wallets again.

## Unified payment integration

- Read `docs/UNIFIED_PAYMENT_INTEGRATION.md` before changing unified payment
  routing, signing, callbacks or refunds; its fixed central-service source blobs
  are the contract reference. The detailed local continuation plan is in
  `docs/UNIFIED_PAYMENT_IMPLEMENTATION_PLAN.md`.
- Product signing keys stay in Vault and the memory agent. The settings UI only
  selects routes and reads server capabilities. The owner authorized controlled
  live WeChat/Alipay 1–2 fen tests and deployment on 2026-09-09; ordinary
  customer purchasing stays disabled. Local tests do not establish live readiness.
- Unified refunds persist one active attempt per order and recover balance only
  after a trusted success. Retain refund history and manual-review fences during
  rollback; never remove financial tables to undo an application release.

## Production host

- Use the configured SSH alias `sub2api-candidate` for current web/API releases;
  it is the serving Azure node and background owner. `sub2api-new` expired on
  2026-09-12 and is no longer a release target or fallback. Recheck node state
  before lifecycle changes, and exclude remaining obsolete database connections
  before currency cutover. Do not copy a raw host, port, or key into scripts.
- Read `docs/operations/WWW_ORIGIN_RECOVERY_20260912.md` before release or Caddy
  work. Preserve the live www site, automatic HTTP-01 certificate and static
  files; never replace the live Caddyfile with the API-only example template.
- The `sub2api-new` server is shared only with Turtle's GPT. Sub2API owns `/opt/sub2api`,
  its `sub2api*` containers, volumes, images, release logs, and loopback ports;
  do not inspect, modify, restart, prune, or reuse Turtle's GPT resources from
  a Sub2API task. Keep both projects' deployment directories, Compose projects,
  containers, volumes, ports, reverse-proxy sites, backups, and rollback paths
  isolated.
- Before any remote action, read `deploy/README.md` and inspect the root-owned
  release configuration read-only. Do not assume the production branch or
  active container from local state.

## GCP Taiwan line candidate

- Read `deploy/gcp-taiwan-line/README.md` before operating the retained Taiwan
  Premium candidate. Its exact static IP is the tested network identity.
- The candidate runs the documented HAProxy transport-only ingress on exact
  public TCP `80/443`; production API DNS points to this transport ingress. It
  has no project SSH identity, service account, runtime credential, app, data
  service, worker, or OAuth-egress role. Do not add another line protocol,
  widen ingress, release its address, or change DNS without satisfying the
  documented cutover gates and the matching Vault/registry workflow.

## Windows desktop test client

- Connect only through the configured SSH alias `turtle-windows`; do not copy
  its address, port, credentials, or relay details into repository files.
- This host is an authorized real-client canary source for Sub2API HTTP/WS and
  attachment tests. Keep work inside a dedicated temporary test directory and
  do not install, remove, or change unrelated desktop software or user files.
- Never print or copy Codex/API credentials. Use the desktop's existing client
  configuration and report only numeric Sub2 IDs plus privacy-safe byte/count,
  cache, timing, status, and transport metrics.

## Release boundary

- The documented production path is the explicitly dispatched blue-green
  release in `deploy/README.md`; ordinary pushes and tags must not start
  GitHub Actions. GitHub Actions builds and packages the exact `main` commit;
  production may only validate and load that image before the blue-green
  switch, not fetch source or compile it. Application state lives under
  `/opt/sub2api`, release configuration under
  `/etc/sub2api-autodeploy.env`, and logs under `/var/log/sub2api-release/`.
- A local test, build, or report does not authorize an SSH session, push,
  release, production configuration change, Caddy change, or cache deletion.
- Before a release, resolve the configured production repository and branch,
  confirm the current active container is healthy, and retain the documented
  automatic rollback path.
- Every planned database cutover, application-container lifecycle change, and
  Caddy upstream change must hold the shared production maintenance lock
  documented in `deploy/README.md`. Do not issue a raw `docker stop`,
  `docker restart`, or Compose lifecycle command against production application
  containers outside that boundary. Emergency recovery goes through
  `sub2api-runtime-guard.service`.
- Production may temporarily use `sub2api-blue`, `sub2api-green`, and the
  legacy `sub2api` name at the same time. Long-lived Responses WebSockets can
  keep an old color draining across a later release. Resolve the active color
  from Caddy and select only an absent or stopped target; never assume a fixed
  two-name toggle or stop a running drain container to free a name.
- Attachment Gateway releases must keep the feature disabled by default. Any
  canary enablement must be scoped to explicitly approved API-key, user, or
  group IDs; `allow_unscoped` stays `false` and Caddy limits are a separate
  change.

## Upstream update scope

- For v0.2.3 migration 236, read `docs/operations/UPSTREAM_V023_20260908.md`.
  Preserve canonical `models_list_config` data even when an alternate upstream
  column exists; do not rename the fork physical storage during routine merges.

- For v0.2.2 model allowlists and rolling upgrades, read
  `docs/operations/UPSTREAM_V022_ALLOWLIST_20260907.md`. Preserve the Ent
  legacy storage key and disabled production allowlists; upstream migration
  235 must not rename a column still used by draining/rollback binaries.

- For native Responses image-tool account admission, read
  `docs/operations/RESPONSES_IMAGE_ACCOUNT_ROUTING_20260907.md` for the fixed
  upstream reference, local behavior contract, and regression scope.

- Classify upstream updates against enabled production configuration, existing
  traffic, and locally owned behavior before expanding feature review. An
  upstream capability that is not enabled or used in production, and cannot
  alter an existing path through migrations, defaults, shared serialization,
  or cache materialization, should pass the normal merge, build, migration,
  and release gates without local feature repairs.
- Add compatibility work only when a change reaches an enabled production path
  or a locally owned optimization, or when repository evidence shows that an
  otherwise unused feature changes existing behavior through a shared boundary.
  Record the existing path that justifies the added work.
