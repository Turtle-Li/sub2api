# Account-test instructions

## Problem and ownership

The Responses connection test previously added `openai.DefaultInstructions`
to every text probe. That embedded file identifies the agent as GPT-5 Codex,
even when an API Key account targets a third-party model or an OAuth account
tests a newer GPT model. A user prompt of `hi` therefore carried an unrelated
coding-agent prompt as well.

This behavior is inherited from upstream, rather than introduced by the local
ModelTrace/custom-prompt changes. The unconditional assignment is present in:

- Repository: `https://github.com/Wei-Shaw/sub2api`.
- Adopted v0.2.17 release: `060d0160417782cd962a0c7f460f6cd4565d4916`;
  merge parent: `b0a02a3304a0a78cdd003ddda1dd23ba02c03084`.
- Upstream main fetched on 2026-10-21:
  `624874b313241c46a34c68eb843c021c7dbdacaf`.
- Source: `backend/internal/service/account_test_service.go`,
  `createOpenAITestPayload`, including the unconditional instructions assignment
  at line 2800 in those upstream revisions.
- Prompt source: `backend/internal/pkg/openai/constants.go` and
  `backend/internal/pkg/openai/instructions.txt`.
- Related upstream tests:
  `backend/internal/service/account_test_service_openai_test.go`.

Preserve upstream LGPL-3.0 notices and the project's existing licensing
obligations. This fix reuses local prompt selection; it adds no copied prompt.

## Local behavior

- API Key Responses text probes send the selected/mapped model and the user
  prompt, without synthetic `instructions` or OAuth-specific `store`.
- ChatGPT OAuth text probes retain `store: false` and use
  `defaultCodexSynthInstructions` with the normalized upstream model, matching
  ordinary Codex forwarding. Model aliases are normalized before selection.
- DeepSeek/Kimi adaptive Responses probes and model-routed Responses probes
  reuse the API Key payload and need no separate instructions deletion.
- User prompt defaults, reasoning fields, authentication, transport identity,
  model mapping, SSE completion and error handling retain their existing
  contracts. Scheduled tests and ModelTrace inherit the shared payload fix.

The intentional difference from upstream is at the shared test-payload builder,
not a new provider-specific branch or a change to the global prompt constants.

## Ordinary requests

Do not interpret model-aware prompt selection as a model whitelist.

Ordinary API Key forwarding does not automatically add this Codex default.
Codex OAuth Responses forwarding may supply model-aware instructions when
the caller supplies none; an unknown model name still has a fallback. Existing
non-empty caller instructions are not replaced by the automatic default.
Chat/Messages compatibility bridges skip the synthetic coding-agent default;
an explicitly configured forced template is a separate behavior.

This fix changes connection-test request construction. It does not change
ordinary forwarding or the unknown-model fallback contract.

## Regression coverage

`backend/internal/service/account_test_service_instructions_test.go` exercises
the public account-test entry point and inspects the outbound HTTP request:

- API Key probes for a compatible Claude model, OpenAI, fixed-protocol
  DeepSeek/Kimi and an unknown model with the default user prompt.
- OAuth probes for GPT-6, GPT-6.1, GPT-5.2, a normalized model alias and legacy
  Codex. Expected instruction sets come from the existing selector and explicit
  fixture upstream model IDs; no natural-language prompt wording is pinned.

The initial regression run failed all five API Key cases and four OAuth cases
under the old implementation. Existing adaptive-provider, compact, image,
scheduled-test, ModelTrace and ordinary-forwarding tests provide adjacent
coverage. See the task change report for executed checks and release status.

## Production release — 2026-10-21

The owner authorized commit, merge and release. Fix commit
`a2cb792eb85700a11f031a9d7ff6efa0068e78e6` was fast-forwarded to fork `main`
and deployed by GitHub Actions run
[`39599344999`](https://github.com/Turtle-Li/sub2api/actions/runs/39599344999).
Both the image release and Caddy configuration steps succeeded; the workflow
completed at `2026-10-21T12:35:19Z`. Version remains `0.2.17`.

- Target: the existing AWS production host through `aws-candidate`, using the
  documented verified-image receiver and canonical maintenance lock.
- Active slot: `sub2api-blue`; image
  `sub2api:auto-20261021-053409-a2cb792e`, local image ID
  `sha256:88b8edbbeb2c7ccb8d9174294dbeef93c25f0e15f12e5bc3db545dba27ec06a0`.
- Compressed archive: 86,988,318 bytes, SHA-256
  `beb5f687c2cb7e3cee715a103c16cf9dbf97a4772eaac004a6649557172fd219`.
- Caddy host/startup/Admin views all select blue; traffic is `accepting` and
  blue is the active background owner. Container healthy, restart count zero,
  no OOM, no retained Caddy or local-release transaction.
- Authenticated release smoke: `gpt-5.6-sol` Models and Responses both HTTP 200.
  Pinned-origin API/www health, homepage and login checks all HTTP 200.
  Public API health is 200 and anonymous Models is the expected 401. Local
  anonymous www probes return the same 403 before and after deployment; this
  does not establish a successful browser journey through that edge.
- Application/Caddy log audit from `2026-10-21T12:34:00Z` through the
  post-release check found zero 5xx or panic/fatal lines. Old green drained and
  exited normally; it remains the prior compatible generation for guarded
  recovery. No rollback was needed.
- Host log directory:
  `/var/log/sub2api-release/gha-20261021-053409-a2cb792e-3692464`.

The source passed 1,255 related checks including subtests with race detection
and shuffled order; the 10 new request-capture scenarios passed independent
execution. Changed-file lint is clear; affected-package lint still has 69
existing findings in unchanged files. Release smoke checks ordinary model
traffic, while the local public-entry regressions directly inspect account-test
payloads. No production administrator-test payload was captured.

This release has no database migration. In external dependency mode, the
application release does not run a local full database backup; the existing
data-host backup/NAS pipeline remains responsible for database backups. Keep
the documented drain/readiness/maintenance-lock gates for recovery.
