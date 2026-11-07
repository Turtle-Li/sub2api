# Model-status layout and24-hour probe trial — November8

Owner request: fix the singleton-platform left-column layout; keep the selected
platform/group checks running for one day and report their actual Token use.
The owner also questioned missing Claude cache metrics and GPT's50–60% share.
Owner-facing date is November8; host evidence retains its exact UTC timestamp.

## Metric investigation

ANALYSIS_READY: the public cache formula is correct for persisted, disjoint
input/cache-write/cache-read buckets. OpenAI's recorder subtracts cache buckets
before saving ordinary input. The pinned ranxi2001/sub2api revision
`5f0b6321a50153a738aef3fbade57c969dbe710c` uses the same denominator in
`backend/internal/repository/channel_monitor_v2_repo.go` (metricAccumulator).
Source/license and intentional local differences remain in the feature contract.

Read-only production evidence at19:30UTC: selected GPTgroup6's24h cache share
was52.46%; the owner's usual traffic belonged to different groups. Selected
Claudegroup10 had no real usage in180minutes or24hours. Therefore
this change does not rewrite the formula, borrow other groups' traffic, or fill
empty metrics with synthetic samples. Public copy explains this group's scope
and the distinction between request statistics and connectivity checks.

The trial investigation identified a separate private accounting defect:
native Claude check receipts omitted cache-read/write input buckets. The real
forwarding regression initially returned9ordinary +20write +70read as9input;
after repair it returns99. OpenAI already reports total input. Customer billing,
passive statistics, prompts, scheduling and retries remain unchanged.

## Implementation and verification

The dashboard shares columns between platforms with one selected group. A
multi-group platform spans the row and lays out its cards using the same fluid
minimum22rem grid. Filtering automatically recomputes spans. Homepage namespace
`turtleroute-status-20261108-v4` preserves prior immutable assets and uses a
fluid minimum352px grid. Model plaza remains price-only.

Local evidence (not shipped assets) is in `.omo/status-layout-qa.md`:
102frontend tests,14static tests, scoped Go service/handler/routes tests,
frontend production build and Go build pass. Native Claude cached input is
covered by a RED/GREEN provider-forwarding regression. Real Chrome verifies
six singleton platforms, mixed multi/singleton platforms, search reflow/empty
results,1440/980/390px, and the independent homepage with no plaza enrichment.
No production configuration was saved or paid QA request sent.

## Production and trial

Released source `267192cad8634c76f9d12d89b9494fd512eae720` includes layout
commit `2493d83bb`. Canonical GitHub run
[42407817218](https://github.com/Turtle-Li/sub2api/actions/runs/42407817218)
completed successfully. Runtime is healthy `sub2api-blue`, image
`sub2api:auto-20261107-115647-267192ca`, backgroundactive with a readonly
generation fence,0restarts/OOM. Previous green is stopped through the existing
natural-drain flow. The persistent retained-drain option remainsfalse.
Canonical audit: app5xx/appfatal/Caddy5xx all0. No production-side compilation,
raw application lifecycle command or forced connection close was performed.

Compressed image archive110013705bytes, SHA256
`04b5cfc2bea213015c46bc2a357e9b83f66cf1271ca1000b8dc602fa72d8f16f`.
Runner image `sha256:a9285ef33fb9134eb12e4b3dc3e39bc75e0cfef9a10048716a0c76fee6fdf261`.
Server release log:
`/var/log/sub2api-release/gha-20261107-115647-267192ca-1748308`.

Static publication used the reviewed maintenance-lock/CAS helper. HTML SHA256
`1be1a057a1038013985b801e68fcdce743a6e9835185a9a49c2b24e7b1e29668`;
module `f2ea33755054d441c7b4e1f4cee950a07d1c5d6eadf80f344af0f7cd49a5c05a`;
CSS `2948f51f70fe53a7974231f8eb4d25e5474093096c4a2e1c0bfbda159c7f9f85`.
Publication evidence `/var/log/sub2api-release/model-groups-20261107.0RkKVC`;
rollback HTML `/data/sub2-web/home/index.before-status-layout-20261108.html`
inside the Caddy container. Prior immutable assets remain available.

Origin smoke passes: six selected public cards, no selected-model disclosure,
no-store responses,18ten-minute cells, sample guards and price-only plaza.
Real Chrome verifies live1440px3columns/2rows and390px single350px cards,
no overflow. Live homepage has six cards and namespacev4, separate from pricing.
Screenshots: `.omo/status-layout-live-desktop.png`,
`.omo/status-layout-live-mobile.png`, `.omo/status-layout-live-home.png`.
Kimi still has real check failures; the owner's exact model remains configured.

At19:36:35UTC configuration version5 had six selected/enabled public targets:
6,10,17,18,35,36. Group29 remained hidden/paused. No target/model/name change
was made by this task. A later owner save must be preserved and reported.

At20:03:22UTC after release, configuration remainedversion5 with exact
group_probe MD5 `ec263ba0fb0fddbde71cc8786192c207`, identical to pre-release.
Fixed trial: **2026-11-07T20:03:22Z through2026-11-08T20:03:22Z**.
Local baseline: `.omo/status-probe-trial-baseline.json`. Active thread heartbeat
ID24, “模型状态检测24小时用量复盘”, is scheduled24hours later to report the fixed
window in this conversation. After one report it pauses the follow-up; the
actual checks keep their current settings. No completed24h result is claimed yet.

Use `deploy/model-status-probe-consumption.sql` on the documented `sub2api-db`
runtime identity with explicit `trial_start`/`trial_end`. It reports each
group/model's attempts, outcomes, pending and missing-usage counts, input/output
and maxima. The observed window must be24hours and `window_complete=true`.
Unreported provider usage is unknown; token totals are not a monetary bill.
Probes are separate from customer usage/billing. At most144attempts/group/day
are scheduled, with the existing unique group/slot claim and no monitor replay.
