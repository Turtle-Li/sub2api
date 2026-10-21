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
