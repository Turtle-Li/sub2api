# Responses native image account routing repair

Status: repair contract and source evidence. Runtime publication and DNS
state are recorded separately in the dated operational release reports.

## Evidence and root cause

Bug ID: `RESPONSES-IMAGE-ACCOUNT-20260907`.

Both observed application nodes ran revision
`286d9033c16708308628c8ff5b7392767124e6d0`. The controlled DNS test was
rolled back after an image request selected account 55 and returned HTTP 400
(`Tool choice 'image_generation' not found in 'tools' parameter.`). A control
request selecting account 11 succeeded. This comparison alone does not prove
that the DNS transport caused the error or that the provider error has only
one cause. Subsequent exact account model restrictions made the dedicated
Images canary pass through the Taiwan/Azure path.

A separate confirmed code defect remains: native Responses requests carry
their text model at the top level and their image model inside an
`image_generation` tool. HTTP and WebSocket initial account selection checks
only the text model. The image model is resolved later for billing. A
text-only account can therefore pass selection despite an explicit image
model whitelist. WebSocket connections retain one account for subsequent
turns, so their per-turn admission boundary also needs the image requirement.

Implementation baseline is fork `main`
`0537df3dc4a9ea05b017703b18a5b58b0d49c177`. The affected scheduler,
handler, and image intent sources are unchanged from the observed runtime.
Work is isolated on `codex/responses-image-account-routing`.

## Acceptance and scope

- AC1: A native image tool request requires its text model and every distinct
  declared image model to be supported by the selected OpenAI account.
  An omitted tool model uses the existing `gpt-image-2` default.
- AC2: The same requirement applies to advanced and legacy scheduling,
  previous-response and sticky reuse, fallback, and fresh database checks.
  Existing model-specific and image-family cooldowns also veto native image
  admission while preserving ordinary text eligibility.
  A text-only account must not win because it has higher scheduling priority.
- AC3: HTTP and WebSocket first-turn selection carry requirements derived
  from the original request through text model mapping and retry paths.
- AC4: Later WebSocket turns recheck native image eligibility and group image
  permission before forwarding, including passthrough forwarding. An
  incompatible bound account requires reconnect rather than silently moving
  an established conversation to another account.
- AC5: Ordinary text, passive `image_gen` namespaces, and user-defined image
  functions preserve their existing behavior. Other provider platforms are
  outside the OpenAI native image constraint.
- AC6: Existing exact/wildcard/empty mapping and passthrough semantics are
  preserved. No plan-name heuristic, tool removal, credential change, schema
  change, or new dependency is part of this repair.

## Delivery and validation

1. Read-only Bug Analysis: complete (`ANALYSIS_READY`).
2. Developer: add a request-scoped native image model requirement, demonstrate
   the original selection regression, implement shared admission checks, and
   run focused service/handler regression tests.
3. Independent QA: pin the completed source and run Level 1 affected-boundary
   validation, including negative selection and WebSocket cases.
4. Independent review: inspect the same QA-passed source for correctness and
   compliance with the acceptance criteria. Any repair creates a new snapshot.
5. Release preparation: identify the exact GitHub-built artifact and retain
   the audited blue-green rollback. A source test result is not a runtime test.

The root agent owns integration, documentation, and final verification; the
implementation worker owns backend changes. Existing account configuration,
operational evidence, unrelated dirty worktrees, and concurrent admin changes
are preserved. Public DNS remains on the old origin and Azure remains standby
through repair validation. Existing operational reports in the standby
release worktree remain the authority for the completed rollback.

## Upstream comparison

Reference repository: `https://github.com/Wei-Shaw/sub2api` at fixed revision
`ab99d56e9626e6cd731592dae8553c9758a0efa2`.

Relevant sources and tests:

- `backend/internal/handler/openai_gateway_handler.go`
- `backend/internal/service/openai_account_scheduler.go`
- `backend/internal/service/openai_gateway_scheduling.go`
- `backend/internal/service/image_generation_intent.go`
- `backend/internal/service/account.go`
- `backend/internal/service/model_rate_limit.go`
- `backend/internal/service/model_rate_limit_test.go`
- `backend/internal/service/openai_account_scheduler_test.go`
- `backend/internal/service/image_generation_intent_test.go`

That fixed upstream snapshot retains the same text-only Responses selection
boundary. This local repair intentionally adds native image model admission
while preserving its model mapping, image default, and connection affinity
contracts. The repository's existing LGPL-3.0 license and notices remain in
force; this change introduces no third-party dependency or license replacement.

## Bounded operational observation during repair

At approximately 09:50 CST on 2026-09-07, both runtime generations still
matched the pre-repair image and state-file inodes. Old blue was healthy and
background active; Azure green was healthy and standby. Both had zero
restarts, no OOM, no outstanding local transaction, and available maintenance
locks. No application lifecycle or DNS mutation was performed for this check.

A subsequent 30-minute old-origin log sample contained nine completed
`/v1/responses` requests, all HTTP 200, with total durations 8.308–104.301
seconds (median 24.996 seconds). Total generation duration alone is not a
first-token latency or instability diagnosis. No sampled application 5xx or
fatal was observed. Four paired `RESET_CREDIT_QUERY_FAILED` /
`OPENAI_QUOTA_REQUEST_FAILED` warnings concerned account 11's background quota
query. The source identifies these as failed upstream quota fetches; the
sanitized logs do not establish the underlying transport cause. No reset
credit consumption or unrelated quota-policy change is part of this repair.

During integration, source inspection also confirmed that the existing
`openai:image_generation` family cooldown is selected by an image model or a
separate service-context image flag. The HTTP forward hint is a Gin value and
does not set that scheduler flag. The new native model requirement therefore
also consults `GetModelRateLimitRemainingTimeWithContext` for each image model,
reusing its existing model mapping and family cooldown behavior. This is an
image admission fix; it does not alter cooldown duration or upstream quota
reset policy.
