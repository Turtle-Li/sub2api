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
was52.46%. Other GPTgroups16/12/4 had97.18/95.01/97.76%. Selected Claudegroup10
had no real usage in180minutes or24hours; Claudegroup14 had96.33%. Therefore
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

Pending reviewed source publication and canonical release. Resolve the live
active container and root-owned repository/branch before dispatch; retain
natural draining and the automatic rollback path. The one-day window starts
after the accounting fix is live, with exact UTC bounds recorded here.

At19:36:35UTC configuration version5 had six selected/enabled public targets:
6,10,17,18,35,36. Group29 remained hidden/paused. No target/model/name change
was made by this task. A later owner save must be preserved and reported.

Use `deploy/model-status-probe-consumption.sql` on the documented `sub2api-db`
runtime identity with explicit `trial_start`/`trial_end`. It reports each
group/model's attempts, outcomes, pending and missing-usage counts, input/output
and maxima. The observed window must be24hours and `window_complete=true`.
Unreported provider usage is unknown; token totals are not a monetary bill.
Probes are separate from customer usage/billing. At most144attempts/group/day
are scheduled, with the existing unique group/slot claim and no monitor replay.
