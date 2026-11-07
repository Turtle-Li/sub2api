# Model status retry and Kimi correction — 2026-11-08

## Diagnosis

ANALYSIS_READY (Kimi and check retries). The owner reported false Kimi outages,
requested three check attempts before declaring failure, and asked to clear
Kimi's status history. Existing customer retry recovery is examined separately;
customer cancellation, admission/quota errors and intermediate retries must not
be promoted to provider outages.

- Symptom / reproduction: group18, `kimi-k3.1-pro`, repeatedly records
  `upstream_error` although the actual WorkBuddy account246 returns HTTP200.
- Call chain: group prober → normal group scheduler → WorkBuddy Chat
  Completions → bounded SSE recorder → local check verdict.
- Evidence: the exact16-token request returned zero text characters,59
  reasoning characters, `finish_reason=length`,16 reasoning/output tokens,
  6369 input tokens (6144 cached), and no error. The deliberately small output
  budget ended a valid reasoning response; requiring visible text made this
  an incorrect outage classification. This is not evidence of a WorkBuddy outage.
- Impact: scheduled checks with reasoning models; genuine empty/error/incomplete
  streams must continue failing. Customer forwarding/billing is unchanged.
- Recommended change: recognize nonempty reasoning at an explicit output-budget
  terminal as connectivity evidence, retain the small budget, and add at most
  three runner-owned attempts under the existing unique group/slot claim.
- Risks / safeguards: recovered checks publish one success; cancellation,
  runtime fencing or configuration withdrawal stays unknown; each attempt has
  a45-second deadline and rechecks authorization. Persist cumulative private
  usage before another attempt. Missing usage is unknown consumption.

ANALYSIS_READY (cache display): account249 serves both the high-cache and
low-cache groups. Cached Luna requests in group6 report95–99% cached input,
but many large HTTP/WS requests persist zero cached input. This explains why
an input-token-weighted aggregate is much lower than cached continuations.
Read-only inspection of14recent official Codex Proxy RS trace files found138
Luna receipts with input>1000tokens:82cached and56zero-cache, with5750243
input tokens in the latter. One zero-cache receipt reports138944input and
an explicit source `/input_tokens_details/cached_tokens` value0. Thus the
zeros are already present upstream, rather than introduced by the status
aggregator. This sample is a different window from the24h Sub2API aggregate;
do not equate their counts or claim a precise cross-log reconciliation.
No evidence justifies changing the cache formula, rewriting billing history,
or replacing low observed ratios with a promotional percentage. Exact upstream
cache-miss causes (model/session/pool behavior) remain unproven.

## Execution and acceptance

| Order | Scope | Evidence / stop condition |
| --- | --- | --- |
| R1 | Service verdict/runner and regressions | Reasoning budget termination succeeds; empty/error streams fail; failure→success stops; three failures produce one failure; authorization/cancellation stops further paid attempts |
| R2 | Additive migration/repository/operator report | Unique claim survives; attempt receipts and token totals survive progress writes; cleared status history stays available to private consumption reporting |
| R3 | Independent review and focused Go/PostgreSQL checks | No customer fact/billing mutation, no public private fields; stop on incorrect accounting or unsafe migration |
| R4 | Exact main canonical release and scoped Kimi cleanup | Healthy new upstream; natural old-connection drain; hide pre-correction Kimi monitoring rows while retaining claims and token receipts |
| R5 | Existing24h follow-up | Preserve the original UTC window and report actual retries, missing usage and cleared monitoring history; do not send manual extra checks |

Reference: ranxi2001/sub2api revision
`5f0b6321a50153a738aef3fbade57c969dbe710c` (LGPL-3.0), existing probe source,
tests and claim contract recorded in `MODEL_AVAILABILITY_GROUPS.md`. The owner's
new three-attempt policy intentionally supersedes the local single-attempt
policy. WorkBuddy diagnostic uses its running official revision
`1df3b836dd91326078e690a9b3d98cdbb7ac9f9c`; only sanitized numeric receipt data
was retained. No provider credentials, prompts or raw errors are published.
Its source is `misakano7545/workbuddy2api-panel` (MIT), notably
`internal/upstream/thinking.go` (only verified DeepSeek thinking-switch support),
`internal/upstream/cn_contract_test.go`, and `internal/server/handler.go` for
streamed Chat completions. The monitor does not copy their implementation or
change the gateway's thinking policy; it accepts the observed valid truncated
reasoning response while retaining its original16-token budget.

Customer retry recovery already uses the final gateway status: a recovered
HTTP200 with upstream error history is excluded from terminal error facts.
The existing PostgreSQL `TestChannelMonitorV2AggregatesHTTPFinalOutcomeOnce`
checks recovered success, duplicate terminal errors, reused external request
IDs and499cancellation independently. Customer-side retries with independent
request IDs cannot be safely collapsed into an invented shared request.

## Validation and production release

Local red/green regressions cover the Kimi reasoning-only receipt, recovered
checks, three failed attempts, configuration withdrawal, missing receipts and
progress-write failure. Native WorkBuddy forwarding retains6369input/16output.
Real PostgreSQL18/Redis integration verifies claim ownership, durable progress,
hidden history, and the actual operator report SQL. Independent review caught
and corrected a zero-attempt cancellation being counted as a legacy attempt:
legacy claims now retain NULL, new claims explicitly start at0. Focused service,
repository/handler/migration checks and `go build ./...` pass. Independent
Standards and Spec re-review both approved with no blockers.

Owner-authorized canonical production release:

- Source `6d39b7edb066bae4572e1e0731f44a6d3bc12718`, version0.2.21.
- GitHub run[42414927792](https://github.com/Turtle-Li/sub2api/actions/runs/42414927792)
  completed successfully. Ordinary pushes still do not deploy.
- Image `sub2api:auto-20261107-132452-6d39b7ed`; archive110002794bytes,
  SHA256 `95850e101357a89b0c8f14fa1250bdf8453e644c26a3edd52bc7c7eb7e4f5128`.
- Canonical evidence directory
  `/var/log/sub2api-release/gha-20261107-132452-6d39b7ed-1856014`.
- New green started21:25:14UTC, healthy and sole background owner. Caddy
  routes new connections to green. Blue went standby and naturally stopped
  after draining, confirmed stopped by21:27:31UTC; no request connections were forcibly ended.
- Migration269is installed. Probe configuration remains version5/digest
  `ec263ba0fb0fddbde71cc8786192c207`, the six owner-selected groups unchanged.
- Under the canonical shared maintenance lock, hid13legacy group18Kimi
  monitoring rows (`attempt_count IS NULL`) after verifying the active reviewed
  image and the group's Kimi identity. Unique claims and82869input/240output
  tokens are preserved. Customer usage and financial records were untouched.
- First corrected scheduled slot21:30UTC: all six groups succeeded on attempt1;
  Kimi6369input/16output, no missing usage. The real browser shows Kimi running
  normally, a new green cell, and gray cleared history. No fabricated history
  or cache percentages were inserted. No static homepage publication was needed.

The original trial window stays2026-11-07T20:03:22UTC through
2026-11-08T20:03:22UTC. Existing thread follow-up24was updated rather than
duplicated. The private baseline records the policy change and one direct
WorkBuddy diagnostic at20:51:34UTC (6369input/16output) outside the check table;
the report must list and add it separately. Cleared monitoring rows remain in
the consumption report. Input/output totals include recovered failed attempts;
missing usage and pending checks remain unknown. A zero-attempt new cancellation
does not invent a paid execution. Do not report a completed24h trial early.

Rollback preserves migration269, claims and all private usage receipts. Use the
canonical rollback flow and prefer a corrected compatible image. Intentionally
restoring an older verdict requires disabling the affected monitoring first to
avoid republishing this known false outage; never restore an older database or
delete consumption records as a rollback mechanism.
