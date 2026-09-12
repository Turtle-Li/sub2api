# Recharge card style — 2026-09-12

## Product direction

The owner chose the existing subscription card design as the visual authority.
The supplied third-party card screenshot is inspiration only. Recharge cards
share the subscription card shell, title, price, spacing and selected treatment.
Two columns require at least 34rem of actual card-area width; smaller areas use
one column so an expanded sidebar and the order rail do not squeeze cards.

Configured descriptions are rendered as text. Credited balance and its unit
remain distinct from the gateway charge; the bonus is readable on the same row
and omitted when absent, with no reserved empty gift panel. No separate select
or selected text is added. The full rendered card supplies its accessible name,
including the description, credited amount, bonus, benefits and purchase condition.

Subscription and recharge concurrency benefits now say how many requests can
run at once. Fulfillment still only raises existing concurrency; the wording
change does not alter entitlements. Prices, bonuses, audiences, thresholds,
subscription renewal, GPT reset cards and financial behavior are unchanged.

## Source and validation

PR: https://github.com/Turtle-Li/sub2api/pull/13

- Initial style source: 8d2599148cd62522570d337a4b73e9b854c6adc6.
- Final reviewed source: 513083cec1baa0dce79bf8a0f3366c002693379d. It removes
  the price/title-only accessible-name override and adds a regression assertion.
  Independent QA/Review PASS: 44 focused tests, scoped ESLint and diff check.
- Local baseline: 59 focused tests, ESLint, typecheck and production build passed.
  Follow-up: 11 AmountInput tests and ESLint passed.
- Root inspected the real components in external Google Chrome: light/dark
  desktop cards and 430px mobile cards, including the 599/744/+145 tier.
  Chrome's accessibility tree confirmed full card names after the follow-up.
- Temporary preview files use a disabled payment action and are excluded from
  release source. No real payment/refund/reset debit was performed.

## Release target and rollback

Only `sub2api-candidate` is the current release target. The old `sub2api-new`
origin expired and is not a fallback. Read `WWW_ORIGIN_RECOVERY_20260912.md`.
Use the documented GitHub build-only image and installed lock-owning receiver.
Preserve the live www routes, HTTP-01 certificate, static files and background
ownership. There is no migration or catalog SQL in this release.

Preflight: active green at cd8932c5 is healthy, background active; blue is stopped.
The Caddyfile hash was 18cea556d49367f60b0e95b90f04ba2ed44abb1802386dc415da4132fbd147cf.
Homepage hash was c5b06aa5d590e978aeb883944ba8c40cd4755362cc573e66e8b6f6d13c42fe1a.
Rollback uses the retained prior runtime through the canonical helper and keeps
all current financial records, catalog settings and website configuration.

Final CI 34672464018 and Security Scan 34672466070 passed. PR 13 merged as
c91eb7ad3a823c2d8b60fad7fbd07c17301c31fb; its tree matches the reviewed source
(2912ef45439dca16828d1db036b7e02ad1e218b1). Build-only 34672999448 succeeded. Archive 83,784,039 bytes;
SHA-256 f121a2203955be0bfc3cba651a5c48af71b6b0874888106ed9981873dbbd5b11.
OCI manifest d546331577b97f307a9a843eacc6d25f034f5df729aae387c4518be647662efd
references config c87bd9ef984ac03c287c40863d9fd62d1d9ddd7f58a53ac8d0ea76a9ab1741de.
Root validated metadata, archive hash/size, descriptor identities, source labels,
linux/amd64 platform and 13 layers. Independent post-build ARTIFACT_QA_PASS verified all blob digests, rootfs layer
identities, runtime config and embedded frontend markers, including the new
concurrency wording and container query; old technical wording is absent.

## Production result

Deployed successfully on 2026-09-12 at 12:48 CST through the installed receiver.
Release log: /var/log/sub2api-release/gha-20260912-124710-c91eb7ad-1304330.
Candidate serves healthy sub2api-blue, accepting/background active, revision
c91eb7ad3a823c2d8b60fad7fbd07c17301c31fb, image manifest d5463315...62efd.
Receiver exited 0, real-request/health gates passed, app 5xx/fatal/Caddy 5xx all 0,
restart count 0 and no OOM. Payment and Feishu agent identities remain unchanged
and healthy. The previous green generation belongs to the canonical drain monitor.
No manual stop, database/catalog mutation or old-origin deployment was performed.

The Caddyfile differs only by the intended green-to-blue upstream replacement;
new hash 02569427fe7c2b264fe2340a52584eb0cbbdcc83d60314c2934e6ecd16b1b861.
The public homepage hash is unchanged, helpcenter returns 200, and www/API health
both return status ok. Website routes, static files and certificate policy remain.

Final external Chrome acceptance passed on the live /purchase page: all six
recharge products, no-bonus tiers, readable bonus and credited amounts, 599
selection with the 744-credit summary, subscription card alignment and the plain
5X Pro concurrency text. No real payment, refund, reset debit, invoice or message
was submitted. Temporary preview server/files and root-only release wrapper were
removed; the live recharge page remains open.
