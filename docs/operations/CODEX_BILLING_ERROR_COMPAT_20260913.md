# Codex billing error compatibility

## Problem and local contract

Sub2 rejects a request before forwarding when a subscription window is
exhausted (`USAGE_LIMIT_EXCEEDED`) or a pay-as-you-go wallet has insufficient
balance (`INSUFFICIENT_BALANCE`). The localized Sub2 message must remain in the
response body, and recognized Codex-engine clients must show an actionable
Sub2 prompt instead of
`exceeded retry limit, last status: 429 Too Many Requests`.

For a strictly recognized official Codex client, or an exact
production-observed compatible client listed below, on a Responses route, both
business rejections use HTTP 400 with a UTF-8 `text/plain` body containing the
existing Chinese message verbatim. Subscription exhaustion must use the message
produced by `SubscriptionUsageLimitMessage`, including the current reset-card
count when that lookup succeeds. For example:

```text
订阅每周额度已用完。你当前还有 2 次可用重置次数，请前往「订阅」页面使用后再试。
```

The response also carries `X-Sub2-Error-Code` with `USAGE_LIMIT_EXCEEDED` or
`INSUFFICIENT_BALANCE`. The ops error logger restores its business
classification from this header because the client-facing body is plain text.

Non-Codex clients keep the existing Sub2 response status and top-level
`code`/`message` shape. RPM, concurrency, upstream capacity, API-key quota, and
other 429 paths are outside this adapter.

### Production-observed compatible clients

On 2026-09-14 at 16:30 CST, production rejected an exhausted subscription on
`/responses` with the correct Chinese message and one available reset, but the
request identified itself with the User-Agent prefix `claudian/`. Claudian uses
the Codex retry behavior that discards an ordinary 429 body, so the user still
saw the generic retry-limit error even though the server body was correct.

The billing-only matcher therefore requires the full observed Codex-style
identity shape: a leading `claudian/` product token with a valid three-part
version, a final `(claudian; version)` trailer, and either no `originator` or the
exact value `claudian`. It does not add Claudian to the official Codex identity
set and does not change OAuth, passthrough, client allowlist, or any non-billing
behavior. Embedded tokens such as `Mozilla/5.0 claudian/0.153.4`, lookalikes
such as `claudian_evil/`, malformed versions, missing/mismatched trailers, and
conflicting originators remain excluded.

Codex streaming requests must not commit an SSE heartbeat before the
post-queue billing check. Compact keepalive starts only after that check. The
Responses WebSocket path checks subscription or wallet funding before ingress
admission and once more immediately before `coderws.Accept`; after upgrade it
checks only platform quota, API-key windows, and RPM. This keeps terminal
subscription and wallet errors on the HTTP 400 path even when funding changes
between API-key middleware and the WebSocket handshake.

## Fixed upstream reference

- Repository: <https://github.com/openai/codex>
- Revision: `25af12f7e61572b0bc18ddb1008be543b91519b0`
- Tag: `rust-v0.145.0`
- License: Apache-2.0
- Relevant source:
  - `codex-rs/codex-api/src/api_bridge.rs`
  - `codex-rs/codex-api/src/rate_limits.rs`
  - `codex-rs/protocol/src/error.rs`
- Relevant upstream test:
  - `codex-rs/codex-api/src/api_bridge_tests.rs`, test
    `map_api_error_keeps_unknown_400_errors_generic`
  - `codex-rs/core/tests/suite/client.rs`, test
    `usage_limit_error_emits_rate_limit_event`

At this revision, `api_bridge.rs` maps an HTTP 429 to the Codex
`UsageLimitReached` path only when `error.type` is `usage_limit_reached`.
Otherwise it creates `RetryLimitReachedError`, whose display contains only the
status and request ID. `rate_limits.rs` reads `x-codex-promo-message`, and
`UsageLimitReachedError` uses that value in its displayed guidance. Header
values are parsed with `HeaderValue::to_str`, so that promo path cannot carry
the required Chinese text. By contrast, an otherwise unknown HTTP 400 becomes
a non-retryable `InvalidRequest` containing the response body verbatim.

Sub2 therefore uses the narrow plain-text 400 adapter only for recognized
official Codex Responses requests and the two local billing states. This is an
intentional HTTP classification difference made to preserve the requested
Chinese product copy and stop futile retries. No upstream code is copied. A
local runtime probe with Codex Desktop `0.154.0-alpha.6.2` on 2026-09-13
displayed both Chinese messages verbatim, including a two-card subscription
count.

## Regression scope

Keep coverage for the pre-forward billing writers and protocol boundaries:

- API-key authentication middleware
- Anthropic-backed Responses handler
- OpenAI Responses handler
- OpenAI Responses WebSocket before protocol upgrade
- streamed Responses queue and compact keepalive before billing recheck

Each must cover subscription exhaustion and insufficient balance, preserve the
non-Codex contract, and assert the Codex status, UTF-8 content type, exact
Chinese body, and `X-Sub2-Error-Code` header. Client recognition must use the
strict official matcher or a documented exact billing-only client prefix or
originator. Embedded tokens in a browser User-Agent must not activate the
adapter. Ops coverage must ensure the header restores the original billing
classification.
