# Claude web upstream SSE compatibility

## Problem and scope

A Claude web adapter returned valid SSE frames with `event`, `id`, and `data`
lines. The Chat Completions bridge assumed adjacent `event`/`data` lines,
discarded every frame, and returned an empty streaming response or a buffered
502. The native Anthropic route returned text but also relayed the web-only
`message_limit` notification, which Cherry Studio's event validator rejected.

`within_limit` with session utilization `0.08` means the upstream reports about
8% of its five-hour allowance consumed. This notification is not a quota error
and does not contain response token usage. Official Messages API event
documentation does not list it; the API also asks clients to tolerate unknown
event types: https://platform.claude.com/docs/en/build-with-claude/streaming.

## Fixed upstream reference

- Repository: https://github.com/Wei-Shaw/sub2api.
- Revision: `060d0160417782cd962a0c7f460f6cd4565d4916` (`v0.2.17`).
- Compared local baseline: `a2cb792eb85700a11f031a9d7ff6efa0068e78e6`.
- Implementation base: `25962d05f` (`fork/main` after fetch); its only change
  after the compared baseline is account-test verification documentation.
- Sources: `backend/internal/service/gateway_forward_as_chat_completions.go`,
  `gateway_upstream_response.go`, `gateway_anthropic_passthrough.go`, and
  `openai_gateway_response_handling.go`.
- Reference tests: `gateway_forward_as_chat_completions_test.go`,
  `gateway_anthropic_apikey_passthrough_test.go`,
  `gateway_service_streaming_test.go`, and `openai_gateway_service_test.go`.
- Both defects exist at the fixed upstream revision. Reuse its existing
  `openAICompatSSEFrameParser` and payload event-type fallback; add no SSE
  dependency. Preserve the repository's LGPL-3.0 license and existing notices.

## Local behavior to preserve across upstream merges

The streaming and buffered Chat bridge consume complete SSE frames, including
IDs, comments, reordered fields, data-only events, multiline JSON and a final
frame without a blank delimiter. Existing conversion state machines continue
to own thinking, tool calls, usage and completion conversion.
Each pending Chat frame is bounded by the configured maximum line-size budget;
an oversized frame returns an error instead of successful finalization.

Both native response modes suppress the complete `message_limit` frame before
writing it. Match either its SSE event name or JSON `type`; an explicit error
event or error payload takes precedence over suppression. Other unknown
extensions retain existing behavior; this is not a global event allowlist.

API Key passthrough buffers one frame before writing, retaining the original
lines for every retained frame. Its pending frame is bounded by the configured
`Gateway.MaxLineSize` (or the existing default) to avoid unbounded accumulation
of short lines. Flush pending ordinary frames on EOF, read error or interval
timeout so partial-frame usage and disconnect handling remain observable.
Keepalive must not split a pending upstream frame. Continue draining usage
after a downstream write failure.

No account, key, billing policy, database, production configuration or release
is changed by this local repair. Existing Chat handling of malformed JSON,
upstream error events and incomplete streams is outside this patch's scope.

## Verification

`gateway_claude_sse_compat_test.go` first failed against the unmodified source:
all ten Chat format/mode cases and twelve native quota format/mode cases failed.
The fixtures contain only synthetic text and quota metadata.

Independent final checks: all 34 compatibility/boundary leaf cases passed with
race detection and shuffle; all 43 compatibility/Chat regression leaf cases
passed with shuffle. Before the bounded-Chat follow-up, 73 surrounding
functional cases and 64 non-Chat-parallel race cases also passed. The original
full race selection failed in five unchanged parallel Chat tests that call
Gin's global `SetMode`; two old tests alone reproduce that failure. Preserve
this limitation rather than calling the full race scope a pass.

Independent review found the missing Chat frame budget; both modes now reject
oversized frames, with regression coverage for rejection and per-frame reset.
These are local source/httptest results. No deployment or Cherry GUI acceptance
is implied; sources, commands and evidence are retained in the task report.
