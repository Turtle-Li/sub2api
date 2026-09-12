# Online USD wallet to CNY execution packet

Owner decision: existing USD wallets ×6.75; future paid recharge 1:1 in CNY; nominal recharge bonuses unchanged. Keep APIs accepting. Old USD requests that complete after conversion may deduct their original numeric amount (temporary undercharge); no compensating extra debit. Group/model discounts and subscription USD entitlements remain unchanged.

The production transaction committed at **2026-09-12 13:40:29 CST**. See
`docs/operations/CNY_WALLET_CUTOVER_20260912.md` for the actual release,
backup, reconciliation and final operating state. Do not rerun this packet
against the already-converted database.

This packet supersedes the earlier admission-pause plan for this specific run. A PostgreSQL transaction still briefly serializes writes; do not promise zero latency. No payment, refund, invoice, order snapshot or used redeem record is rewritten. The live preset bonuses are now 0/0/5/20/75/145 from the separately released payment catalog; preserve that catalog.

## Verified preflight, September 12 daytime

- Active origin is `sub2api-candidate`, accepting/background active, healthy green at `cd8932c5c`. Public health and authenticated `gpt-5.6-sol` models/responses probes return 200. Claude resumed and independently approved the existing currency code and the online approach subject to cache and flusher conditions.
- Registry and `WWW_ORIGIN_RECOVERY_20260912.md` identify `sub2api-new` as expired, unavailable over SSH and unsuitable for rollback. Its six old PostgreSQL sessions are idle, but must still be excluded before denomination changes. Preserve the newly recovered www site/static assets and certificate in every release; do not replace Caddy with the API-only template.
- Current PostgreSQL writers are observed by source; Redis clients are Azure/local only. Application configuration has no platform flusher override (default false), dirty set is empty, and no user has a positive platform-quota limit. Reconfirm these facts before execution, including the actual replacement container's config/env.
- Batch image public admission is already disabled by `BATCH_IMAGE_ENABLED=false` (also Vertex/delivery false). Preserve that effective override during this run: a new cached-USD reservation racing the online transaction is outside this packet. The queue switch alone does not enable public admission. All existing batches must remain terminal at the SQL gate.
- Payment setting is enabled, despite the owner's recollection of a closed entrance. Do not assume no wallet writers. Only two historical balance orders exist, both REFUNDED; no active/frozen wallet or batch state may pass unnoticed. The SQL's preconditions remain mandatory.

## Execution sequence

1. Verify the reviewed temporary cache-bypass implementation and impacted checks. Deploy the exact reviewed build through the existing build-only GitHub workflow and lock-owning receiver, preserving accepting/background-active state and www. Before starting the new application, back up its protected config and set `billing.currency_cutover_cache_bypass_file` to `/app/data/currency-cutover-cache-bypass`; create that marker in the existing application data volume. Monetary cache reads then use DB, queued/synchronous cache writes are suppressed, and authentication bypasses both L1 and L2. Flusher must be disabled. Existing admitted requests continue; use the canonical drain monitor for the old generation.
2. Exclude the expired peer from the database host's existing exact-source public firewall allowlist; retain the verified Azure source. Back up firewall/HBA state first. Remove obsolete HBA allow entries as applicable and terminate only the observed expired-peer PostgreSQL sessions. Verify no old Redis clients, no old PG sessions and continued Azure authenticated probes. Do not touch unrelated SSH, ingress or backup connectivity. If an old Tailnet path belongs to that peer, exclude it too after identifying its owner; do not guess address ownership.
3. Acquire and hold the current origin's canonical `/run/sub2api-maintenance/sub2api-maintenance.lock` across the monetary change. The old peer is excluded at the data boundary, not treated as a functioning second app node. Keep traffic accepting and do not stop the active app. Check all preconditions again. Take and retain a fresh canonical PostgreSQL/Redis backup with checksums; use `rehearse-backup.sh ARCHIVE 1` for an isolated restore and migration/rollback rehearsal. Run `test-online-migration.sh` locally for debit/conversion ordering evidence.
4. Confirm cache bypass is active in every compatible serving generation, flusher false, dirty set zero, and no unknown database writer. Run `wallet-to-cny.sql` with `recharge_factor=1` and `apply=true`. Its row manifest records exact monetary values at the transaction boundary, even though the earlier backup was taken with live requests. Never automatically repeat a successful migration. A lock timeout means no conversion committed; inspect before retrying.
5. Immediately run `refresh-wallet-caches.sh --bypass-confirmed` on the DB host. It requires CNY policy and empty dirty set, purges only monetary/auth cache namespaces, and publishes every active API-key hash to the existing L1 invalidation channel without exposing credentials. Leave subscription cache untouched. Wait for the 15-second policy caches to converge, repeat cache refresh after 60 seconds, and keep bypass enabled throughout verification. The repeat catches older MVCC readers that can finish and refill Redis after the transaction; compatible processes bypass those stale values.
6. Use the existing protected release-probe credential for real wallet calls. Identify its numeric user/key IDs securely and take DB snapshots immediately before/after. The CNY wallet decrease must match the unique usage row's `actual_cost` within stored decimal precision; standard platform usage must increase consistently. Zero-configured key quota/rate limits remain unlimited and are not falsely claimed as exercised limits. Verify a real subscription request still records USD and consumes its subscription window, with wallet unchanged. All calls remain subject to ordinary authentication and use a nonzero-price model. Preserve only privacy-safe evidence and IDs.
7. Compare every wallet against its saved converted value plus legitimate post-transaction debits/credits; confirm recharge multiplier 1, CNY policy/rate 6.75, unchanged group multipliers and subscription limits. Monitor API 5xx, insufficient-balance/quota errors and usage for at least 15 minutes. A unique live test request must not be accepted as proof of whole-site availability.
8. Keep cache bypass enabled until every pre-cutover application generation and its queued writers have stopped under the canonical drain monitor. Clear scoped caches once more, remove the marker, and run a final real debit/DB-cache parity probe. Do not force-stop user WebSockets to accelerate this step. If old sessions remain, document the temporary bypass state rather than silently re-enabling stale caches. Restore ordinary cache operation as soon as that condition is satisfied.

### Corrections established by the live execution

- The image entrypoint recursively changes `/app/data` ownership. A root-owned
  marker alone is therefore insufficient: on this ext4 named volume the empty
  marker was set root:root, mode 0600 and `chattr +i` on the host. Container-root
  `chown` was verified to fail, and real requests did not recreate monetary/auth
  caches. The entrypoint tolerates the failed ownership change. At cleanup use
  host `chattr -i` before removing this exact marker. Recheck these properties
  after every container start while bypass is needed.
- Both blue and green were replaced by the same compatible image before SQL.
  The stopped legacy `sub2api` container was renamed to
  `sub2api-pre-cny-legacy-20260912` under the maintenance lock, preserving its
  data while excluding it from the runtime guard's hard-coded fallback names.
  Never allow automatic recovery to start an incompatible USD binary.
- The actual protected release probe is **key 46 / user 1 / group 16**.
  While bypass is active, `HasUserPlatformQuotaLimit` conservatively returns
  true on cache miss, so even unlimited platform counters accumulate in DB.
  Expired windows can legitimately reset on their first request. After normal
  caches are restored, a cache hit with all limits NULL skips that counter
  write. Verify the expected branch; neither branch imposes a new cap.
- Close stdin (`</dev/null`) when invoking the cache helper from a parent SSH
  heredoc. Its `docker exec -i` SQL calls otherwise consume the remaining
  parent script. Production records distinguish the committed transaction
  from subsequent readback and cache-refresh commands.
- Installed UFW accepts `ufw allow ...` and `ufw delete allow ...`, without
  `--force` on these subcommands. The retirement helper and its command-level
  regression check now use the tested syntax.

## Recovery

Online traffic creates new financial facts immediately. Do not use `rollback-before-reopen.sql` or restore an old full database after cutover. Preserve the per-row manifest, backups, usage and billing dedup evidence; correct forward while keeping CNY-compatible code. On a cache-related error, retain bypass and use the database as authority. On a pre-commit failure, the SQL transaction rolls back and USD remains active; investigate without relabeling data or retrying blindly.

New test credentials are unnecessary for the existing release probe. Do not delete ordinary users, financial/audit rows or usage history to make before/after figures look clean. Record exact code/image/archive identities and the tested source allowlist change in project operations and the private registry when execution completes.

## Latest rehearsal evidence

`sub2api-db-backup-20260912-121620.tar.gz` (262641006 bytes, SHA-256 `5e0388caf0a386436744e8c7e73313298d7fac04fd50fac9e294beba5e04f6db`) passed archive/dump/Redis checksums and isolated restore, dry run, `recharge_factor=1` conversion, duplicate refusal and exact wallet rollback. A root-only copy and checksum are retained outside rotating backups at `/opt/sub2api-migration/currency-20260912/backups/`. This is rehearsal evidence; the live SQL manifest will capture the exact later transaction boundary.

All registration/first-bind default gifts and plan balance bonuses are zero. One pre-existing promo code carries 5 USD credits; it is an outstanding existing credit promise and follows the existing redeem/promo conversion ×6.75. Future paid recharge and its preset bonuses retain nominal CNY amounts.
