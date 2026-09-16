# OpenAI request timezone rewrite experiment

Owner request: make the timezone already present in Codex/OpenAI request
context match the selected account's fixed proxy exit, then verify the final
wire payload with a simulated upstream. This is an experiment for comparing
model behavior. It does not assert that OpenAI routes models or applies account
policy from this value.

## Contract

- Sub2API does not add a timezone HTTP header. It only replaces the value of an
  existing `<timezone>...</timezone>` tag inside an existing
  `<environment_context>...</environment_context>` block.
- Rewriting runs after an OpenAI account has been selected, so every failover
  attempt uses that attempt's account and proxy. It covers Responses HTTP,
  Chat Completions compatibility, Messages compatibility, and Responses
  WebSocket first and later turns.
- Only `instructions`, top-level `system`, and `developer`/`system` role text
  are eligible. User-role content is never rewritten, even when it contains
  identical XML. Missing tags are not inserted.
- The effective value is `accounts.extra.openai_request_timezone` when it is a
  valid IANA timezone, otherwise the selected account proxy's
  `detected_timezone`. The values `off`, `disabled`, and `none` disable the
  rewrite for that account. Invalid overrides fall back to valid proxy
  metadata.
- Values must be loadable IANA location names such as `Asia/Tokyo` or
  `America/Los_Angeles`; `UTC` is also accepted. Fixed-offset labels such as
  `UTC+8` are rejected.

## Proxy detection and cache consistency

`ipwho.is` (`timezone.id`), `ip-api.com` (`timezone`), and Cloudflare trace
(`tz`) can supply the IANA value. Successful geo-capable probes persist it on
the proxy with `timezone_detected_at` and copy it into the existing latency
snapshot. IP-only fallback probes leave the prior valid value intact.

Probe persistence includes the exact protocol, host, port, username, and
password identity that was tested. A result is discarded if that identity
changed while the request was in flight. Editing any of those transport fields
clears the old timezone and starts a fresh asynchronous probe. A changed
timezone invalidates scheduler snapshots for all accounts bound to the proxy,
including fixed-egress OAuth accounts.

New proxies are already probed after creation. On application startup, one
leader instance also runs a one-shot backfill for active proxies whose stored
timezone is missing or invalid. The backfill uses at most three concurrent
probes and a 20-second per-proxy deadline, does not block HTTP startup, and is
canceled during application shutdown. A failed or IP-only probe leaves the
field null; an administrator can retry it with the normal connection test or
quality check. Requests remain byte-for-byte unchanged until a valid value is
stored, unless an account override is configured.

## Request-path cost

The rewrite is local, linear work. It performs no network or database access,
does not take a process-wide lock, and uses a read-only timezone-validation
cache. Requests without a timezone marker take a validation-only fast path and
do not allocate. A changed request allocates one replacement body; borrowed
`gjson` results avoid a second full-size copy, and the original bytes remain
unchanged for account failover.

Apple M1 Pro benchmarks (Go benchmark, three 2-second samples) measured roughly
18 microseconds and 4.5 KiB for a changed 4 KiB body, 0.50–0.56 milliseconds
and 131 KiB for a changed 128 KiB body, and 3.96–4.11 milliseconds and 1.05 MiB
for a changed 1 MiB body. A 128 KiB request without a timezone marker measured
0.11–0.12 milliseconds with zero allocations. The 128 KiB parallel benchmark
measured 0.16–0.19 milliseconds per operation across eight workers, with the
same one-body allocation and no observed contention-related allocation growth.
It is retained in the service test so later changes can expose contention or
allocation regressions.
Cost therefore scales with request bytes; the normal
Codex text-request path is small relative to upstream latency, while sustained
large inline-media traffic should still be watched through ordinary process
CPU and allocation metrics.

The proxy discovery backfill is outside the request path. It runs once at
startup only for active proxies with missing or invalid metadata, with three
workers and a 20-second per-proxy deadline.

The final source passes the complete service package, focused race checks,
repository and migration tests, all-package compilation, frontend typecheck,
ESLint, locale tests, and the production frontend build. The retained
benchmarks exercise changed and unchanged bodies plus an eight-worker path.

## Verification and rollout

The service tests send representative requests through the real forwarding
entry points into a recording upstream. They assert that a Japan proxy changes
the developer tag to `Asia/Tokyo`, an account override changes it to
`America/Los_Angeles`, and the same XML inside user text stays
`Asia/Shanghai`. WebSocket tests apply the same assertion to the first frame
and the composed later-turn transformer. Repository integration tests apply the
migration on PostgreSQL, persist a detected timezone, enqueue bound-account
snapshot invalidation, clear metadata after a proxy transport edit, and reject
a late result from the old endpoint.

After deployment, verify that the startup log reports the timezone backfill
counts and inspect `detected_timezone` in the admin proxy response. Run the
existing proxy test or quality check only for entries that remain blank before
comparing model behavior. Compare otherwise identical prompts across a small
set of accounts and record response quality separately from latency, model
name, service tier, and upstream request ID.

For a per-account rollback, set `openai_request_timezone` to `off`. A binary
rollback is compatible with the nullable columns and does not require removing
them. Request bodies and prompt contents are never written to timezone logs.
