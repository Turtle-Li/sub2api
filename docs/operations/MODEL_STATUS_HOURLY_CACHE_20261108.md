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

Canonical release evidence is appended after completion. Before release,
follow `deploy/README.md`: verify current healthy
origin and exact main build, use the canonical natural-drain blue-green path,
then publish the three reviewed homepage files with the maintenance lock,
previous-HTML hash CAS, assets-first installation and origin hash verification.
Rollback the application through that same compatible path; keep all data.
Static rollback restores the publisher's saved
`/data/sub2-web/home/index.before-status-hourly-cache-20261108.html` atomically
under the shared maintenance lock. No Caddy reload is needed for static files.

The original probe-consumption trial and automation24remain unchanged:
2026-11-07T20:03:22Z through2026-11-08T20:03:22Z. This change adds no paid test
requests or observation-window restart.
