# Best complete-hour cache — owner November8

The owner replaced the90% hide/show rule with recent high cache performance
measured in hourly units. The public figure is explicitly **best-hour cache**,
chosen from the24complete UTC hours before the current hour. It is a historical
maximum over qualified hours, not a current rate, typical rate or24h average.

## Contract

- Reuse the dashboard's real usage and the current bound-account scope from
  `MODEL_STATUS_REAL_REQUEST_CACHE_20261108.md`, including active same-platform
  subscription traffic without publishing private identities.
- Independently compute read / (uncached input + cache creation + cache read)
  for each UTC hour. Each needs at least20valid token-bearing requests or the
  higher configured minimum. Choose the largest ratio, latest on a tie.
- Current incomplete hours, sparse spikes, negative token buckets, zero-token
  placeholders, media and synthetic checks cannot supply the figure. Valid
  low-cache requests still contribute; no raw usage is deleted or rewritten.
  No ratio floor is applied, and old highs expire when outside the24h window.
- Publish `cache_peak_hour_rate`, `cache_statistic=best_complete_hour`,
  `cache_lookback_hours=24`, and the measured hour's start/end. Keep
  `cache_scope=shared_account_pool`. Leave the retired `cache_read_rate=null`
  so existing clients cannot mislabel a peak as their old rolling figure.
  Updated clients require the new metadata, a valid one-hour interval and a
  finite0–100ratio. Missing/old metadata is unknown.
- Display the measured hour on the card; explain the best-hour selection in
  the statistics disclosure and homepage. Homepage assets are immutablev6.
- Availability, real group success/latency/history, user billing, dashboard
  values, selected groups/models and three-attempt checks are unchanged.
  No migration, new timer, paid diagnostic, dependency or configuration write.
- The query remains one bounded batch with a3-second deadline and resolves
  live account bindings on every read. Escalate only on measured deadline risk;
  do not cache a revoked pool or keep a permanent high-water mark.

Pinned comparison rechecked: ranxi2001/sub2api
`5f0b6321a50153a738aef3fbade57c969dbe710c`, LGPLv3,
`frontend/src/components/charts/TokenUsageTrend.vue` and
`backend/internal/service/channel_monitor_v2_test.go` (disjoint denominator
1+3+4=8). Account-pool scope and labelled hourly maximum are local differences.

## Validation and release

Implementation verification: scoped service/handler/routes tests, actual
PostgreSQL18/Redis integration,57frontend tests,17static tests, frontend
typecheck/build and Go build pass. Actual desktop1512/mobile390Chrome verifies
the dedicated cards and homepage, historical-period labels, independent
readiness, low/unknown fixtures and the disclosure. A mobile scope-wrap defect
was corrected; no overflow or console errors remain. Local evidence is
`.omo/status-hourly-cache-qa.md` and adjacent logs/screenshots.

Independent frozen-source review approved Standards and Spec with no blockers.
The bounded production query used the account/time index and executed in
140.144ms; no deadline increase or persistent cache was needed.

Canonical production release completed on2026-11-08UTC:

- Source `3a9ecff5965490271a9baee1e562a325439311ed`, version0.2.21,
  includes the concurrent API alias/TLS fix `c47d7a34e` unchanged.
- GitHub run[42463084116](https://github.com/Turtle-Li/sub2api/actions/runs/42463084116)
  succeeded. Image `sub2api:auto-20261108-003801-3a9ecff5`; compressed archive
  SHA256 `e0ef800435b6b8305af63c7e5f1767f2760f90dd33a68c3bbbb7023fd4d79079`.
- Canonical evidence directory
  `/var/log/sub2api-release/gha-20261108-003801-3a9ecff5-2663078`.
  Release verified08:38:36UTC with app5xx/appfatal/Caddy5xx all0.
- Caddy routes new connections to healthy `sub2api-green` at that exact
  source,0restarts/OOM and the sole active background generation. Its fence is
  mounted read-only and matches the host state. Canonical drain monitoring
  retired oldblue naturally; the post-release check found blue stopped.
  No old connections were terminated or persistent drain override enabled.
- API and www origin smoke both show only the six selected groups, all
  operational. Live best-hour values: GPT97.8562%,Claude97.6972%,
  DeepSeek99.6377%. Their selected hours are respectively
  November7UTC20:00–21:00,18:00–19:00and18:00–19:00. Other groups have no
  eligible hourly cache sample and do not invent a value. Retired
  `cache_read_rate` is null; plaza remains price-only.
- Before/after configuration is identical: version5, public enabled,
  group-probe MD5 `ec263ba0fb0fddbde71cc8786192c207`, selected IDs6/10/17/18/35/36.
  Exact models, names and switches remain owner-managed.
- Immutablev6 homepage publication passed the shared maintenance lock,
  previous-HTML hash CAS, assets-first installation and origin verification.
  Evidence `/var/log/sub2api-release/model-groups-20261107.ikBtH7`.
  HTML SHA256 `577f73ebdf628c200a57a7d9c03fbe498b63dd17128eb5d404a25fe69bc58e66`;
  module `644239e08cb52a7a5539f50d46751979a7c3d275364edfece5fb104dc842c3d3`;
  CSS `b9b4975e1d6bbd2fd769fb5fa98aa3b68b22734a6bcb29299118566d754dc095`.
- Actual Chrome desktop1512/mobile390 verifies both live surfaces, visible
  measured hours, six operational groups, responsive cards and no overflow or
  console errors. Live screenshots are `.omo/status-hourly-cache-live-*.png`;
  API proof is `.omo/status-hourly-cache-production-smoke.json`.

Rollback the application through the canonical compatible path; keep all data.
Static rollback restores the publisher's saved
`/data/sub2-web/home/index.before-status-hourly-cache-20261108.html` atomically
under the shared maintenance lock. No Caddy reload is needed for static files.

The original probe-consumption trial and automation24remain unchanged:
2026-11-07T20:03:22Z through2026-11-08T20:03:22Z. This change adds no paid test
requests or observation-window restart.

The owner's concurrent mail question was diagnosed from server-side delivery
records and existing probe/cooldown logs without changing notification
preferences. See `MODEL_STATUS_ALERT_DIAGNOSIS_20261108.md`.
