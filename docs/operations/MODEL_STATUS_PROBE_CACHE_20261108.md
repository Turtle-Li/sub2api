# Native scheduled-check cache fallback, November8

Owner request: domestic cards should also use cache returned by existing
ten-minute checks when real traffic is absent. Standing release authorization
continues; keep operator models, schedules, limits, alerts and the original
24-hour consumption trial/automation24 intact. No extra paid diagnostics.

## Evidence and implementation

The gateway already normalizes cache-read tokens, but the probe result and
persistence only retained total input/output. The sanitized existing WorkBuddy
Kimi fixture reports6369input/6144cached tokens, demonstrating the missing
recording path; it is not a claim about a new live request. A regression test
failed before implementation (`.omo/status-probe-cache-red.log`).

Migration273 adds nullable paired final-success cache input/read columns.
Historical and old-writer receipts remainNULL. The runner retains cumulative
charged input/output across attempts separately. No prompt, model, token budget,
scheduler, account admission, usage ledger or notification behavior changes.

Require native numeric cache presence matching the normalized usage bucket.
Raw ChatCompletions supports nested details and the existing flat aliases,
including DeepSeek `prompt_cache_hit_tokens`; native Anthropic uses disjoint
input/creation/read. Missing/invalid fields remain unknown; explicit0 is real.
Responses-to-Chat and OpenAI-to-Anthropic converters do not preserve presence
uniformly. Exclude converted receipts entirely, including positive cache, to
avoid a positive-only sample. Upgrade only with native presence provenance.

The public aggregate needs six successful valid cache receipts, current chosen
model, last180minutes, enabled/fresh checks. It is sum(cache read)/sum(totalinput),
with actual cold and low receipts retained. Real shared-pool best complete hour
remains preferred; otherwise cards show “检测缓存占比”, “本分组 · 定时检测” and
“近3小时 · Token加权”. Raw counts/tokens/models/accounts stay private.

Fixed reference rechecked: ranxi2001/sub2api
`5f0b6321a50153a738aef3fbade57c969dbe710c`, LGPLv3; service
`channel_monitor_v3_probe.go`/`channel_monitor_v3_probe_test.go`, v2/v3 cache
aggregation and existing documented references. Attribution is retained.
Separate native check cache and source guards are intentional local extensions.

## Validation and rollout

Source496683426a6f9653dd5be3105e6e158f41272607 safely pushed after freshfetch,
staged secretguard and25-file frozen independentAPPROVE. ScopedGo service,
handler/routes and realPostgres pass; Go/frontend builds pass.90frontend tests
in5files include actual Chinese/English rendering;12static tests pass. Native
usage, retry isolation, nullable/zero persistence, source/qualification/privacy
and client priority/guard cases are covered. Evidence is under `.omo/` in this
task, including `status-probe-cache-frozen.json`, executor QA and independent
`evidence/status-probe-cache-code-review.md`.

Canonicalrun42471380790SUCCESS:
https://github.com/Turtle-Li/sub2api/actions/runs/42471380790
Image `sub2api:auto-20261108-023429-49668342`; host evidence
`/var/log/sub2api-release/gha-20261108-023429-49668342-2808040`.
Activegreen is healthy with0restarts/OOM, sole active generation with verified
readonly background fence. Oldblue2a7267d2 naturally drained and stopped by the
normal drainer; no forced stop/connection interruption. Release-window
application5xx/fatal and Caddy5xx counters are0.
Migration273's two nullable columns exist. Both API/www origin status endpoints
pass source, window, privacy and readiness assertions; plaza stays price-only.
Config5/publictrue/probeMD5ec263ba0fb0fddbde71cc8786192c207, selected
6/10/17/18/35/36 and owner model choices are unchanged.

Homepagev8 published under shared maintenance lock and previous-hash CAS:
HTML `c47e0a20bc7fe356fe4f9b4e7245e7fd8251cab4f51868a7552a64127865ed65`,
JS `50be52571d58768b07cebbf695ae4327e60cd1d0c2e2e3f73596deede176e43a`,
CSS `b9b4975e1d6bbd2fd769fb5fa98aa3b68b22734a6bcb29299118566d754dc095`.
Evidence `/var/log/sub2api-release/model-groups-20261107.aS95Qi`; priorv7 HTML
backup `/data/sub2-web/home/index.before-status-probe-cache-20261108.html`.
Its SHA is `a500c437fa3f7a3c22c89ad957216e2f17ee9624f5c0c1c131578b8193658e45`.
Dedicated task uploadNV0XS8 was removed after verified publication.

Actual Chrome1440/390 status and homepage checks pass no horizontal overflow,
correct six public groups, source labels, existing real metrics and unknown
new-cache sample state. Local dark fixture validates fallback/zero/missing
states; live status light and homepage dark snapshots are under
`.omo/evidence/status-probe-cache-*.png`. Homepage has no console errors. The
existing authenticated browser profile logged two background user-refresh
errors; status fetch/rendering continued successfully, with no card/cache
exception. This release does not change authentication; anonymous API smoke
passes independently of that browser session.

First ordinary scheduled slot10:40UTC on November8: all six checks succeeded
with one attempt each. Native final receipts (not yet a qualified3h aggregate):
DeepSeek6528/6691cached input(97.56%), GLM6656/6701(99.33%),
MiniMax6464/6471(99.89%); Kimi0/6369 and Claude0/523 explicitly report no cache
hit. GPT's converted receipt intentionally hasNULLcache provenance; existing
real-pool cache remains available. Missing and explicit zero are distinct.
New native cache samples correctly report `insufficient` until six qualify.
No historical backfill, paid warm-up, prompt expansion or extra probe was used.
The original Nov7–8 UTC20:03:22 trial and automation24 remain intact.
Allow ordinary scheduled checks to accumulate; six new receipts take roughly
one hour. Unsupported/absent cache remains unknown.

Rollback: canonical compatible application image plus exact previous static
HTML under the shared maintenance lock/CAS. Leave additive columns and paid
receipts intact. Natural connection draining remains mandatory.
