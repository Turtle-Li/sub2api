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

Pending frozen evidence, independent review, exact-main canonical app release,
immutable homepagev8 publication and live smoke. Preserve livev7 and exact
previous HTML backup. Do not backfill new cache columns from total usage.
Allow ordinary scheduled checks to accumulate; six new cache receipts take
roughly one hour. Unsupported/absent cache will remain unknown.

Rollback: canonical compatible application image plus exact previous static
HTML under the shared maintenance lock/CAS. Leave additive columns and paid
receipts intact. Natural connection draining remains mandatory.
