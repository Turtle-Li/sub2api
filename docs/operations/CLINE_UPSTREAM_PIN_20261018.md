# Cline Pass upstream pinning (2026-10-18)

## Release record

- Fork `main` advanced to `b3ffa338ef24697dd11c059086968a4875bebfb7`
  (`feat(cline): pin Cline Pass subscription upstreams` + `feat(usage): record
  the actual upstream provider per request`) on top of the v0.2.17 release
  commit `1abeac808`.
- Explicit workflow dispatch
  `.github/workflows/sub2api-production-deploy.yml` on `main` with
  `deployment_target=aws-candidate`; run `39146716027` completed **success** in
  9m49s and built that exact commit.
- Host evidence `/var/log/sub2api-release/gha-20261018-095729-b3ffa338-61201`:
  candidate `sub2api-green` created from
  `sub2api:auto-20261018-095729-b3ffa338`, healthy on attempt 3, authenticated
  real-request probe `models_status=200 responses_status=200`, Caddy switched
  `sub2api-blue:8080 -> sub2api-green:8080`, old blue stopped (drain monitor
  observed zero in-flight requests).
- Post-release checks: `sub2api-green` up and healthy, Caddy admin and startup
  views select only `sub2api-green:8080`, public
  `https://api.turtleligpt.com/health` returns `200`, the stopped
  `sub2api-blue` container (`sub2api:auto-20261017-094012-03d5ba69`) remains the
  rollback target, and the new container logged **zero** error-level lines while
  serving live traffic.
- Migration `262_usage_log_upstream_provider.sql` applied at container startup;
  the migration runner fails startup on a failed DDL, and the container started
  and served normally.

Owner request: Cline Pass (`cline-pass/*`) subscription models are routed by
Cline's own gateway, and the upstream provider behind a model can change without
notice (the reported case is DeepSeek). A community project,
[`cline-pass-switcher`](https://github.com/munmunjaklin458-afk/cline-pass-switcher),
documents a way to enumerate, validate and **pin** that upstream. This change
ports the mechanism into Sub2API instead of running a second proxy.

## Mechanism that was ported (and what was not)

The switcher's measured finding is that a Cline Pass model travels one of two
routing pipelines, and pinning is written differently for each:

| Pipeline | Backend | Response fingerprint | Pinning field |
| --- | --- | --- | --- |
| `direct` | OpenRouter | top-level `provider` + real `model` | top-level `provider.only / order / sort` |
| `planner` | Vercel AI Gateway | `provider_metadata.gateway.routing` | `providerOptions.gateway.only / order / sort` |

A top-level `provider` block is silently dropped on the planner pipeline, which
is why "changing the provider" does not work against the official API. Pipeline
ownership is decided by Cline and can change over time, so the pipeline type is
a *probed snapshot*, not a constant.

Ported:

- upstream enumeration via a zero-token "fake provider" probe
  (`only: ["__probe__"]`), which makes the gateway fail at the routing layer and
  list the real providers — read from the planner error text
  (`Available providers are: ...`) or from the direct pipeline's embedded
  `error.metadata.available_providers`;
- pipeline detection and last-actual-provider read-back from the response;
- per-model pin configuration (strict / preferred + fallback, exclusions, sort);
- per-provider validation with `ok / limited / bad / auth / unknown`
  classification;
- one-shot pin verification ("test") that reports whether the gateway honoured
  the pin.

Not ported (Sub2API already owns these): the downstream proxy key, the account
pool / round-robin scheduling, request history, and OpenAI response unwrapping.

## Where it lives

| Layer | File |
| --- | --- |
| Pin config + request-body injection (pure) | `backend/internal/service/cline_upstream_pin.go` |
| Probe / validate / test service | `backend/internal/service/cline_upstream_service.go` |
| Data-plane injection point | `backend/internal/service/openai_gateway_cc_pipeline.go` (`sendCCUpstreamRequest`) |
| Managed-`Extra` protection | `backend/internal/service/admin_account.go` |
| Admin API | `backend/internal/handler/admin/account_cline_upstream.go`, `backend/internal/server/routes/admin.go` |
| DI | `backend/internal/service/wire.go`, `backend/internal/handler/wire.go`, regenerated `backend/cmd/server/wire_gen.go` |
| Admin page | `frontend/src/views/admin/ClineUpstreamView.vue` |
| Admin API client | `frontend/src/api/admin/clineUpstream.ts` |
| Account-list entry point | `frontend/src/components/admin/account/AccountActionMenu.vue`, `frontend/src/views/admin/AccountsView.vue` |
| i18n | `frontend/src/i18n/locales/{en,zh}/admin/clineUpstream.ts` (+ `accounts.ts`, `common.ts` nav key) |
| Tests | `backend/internal/service/cline_upstream_pin_test.go`, `frontend/src/views/admin/__tests__/ClineUpstreamView.spec.ts` |

## Behaviour contract

- **Storage**: two per-account `Extra` keys, JSONB, **no migration**:
  `cline_upstream_pins` (`{model: {mode, upstreams, exclude, sort}}`) and
  `cline_upstream_meta` (`{model: {pipeline, pinnable, upstreams, statuses,
  last_provider, canonical_slug, probed_at, validated_at, last_error}}`).
  Both are added to the managed-key delete + preserve lists in
  `admin_account.go`, so an ordinary account create/edit/bulk update can neither
  set them nor wipe them; only the dedicated endpoints write them.
- **Injection point**: `sendCCUpstreamRequest` is the single choke point for
  every Chat Completions upstream send (raw CC, Responses→CC fallback,
  Messages→CC fallback). Injecting there — after protocol conversion and after
  `ensureDeepSeekChatReasoningPlaceholders` — is what makes the pin survive the
  typed Responses→CC rebuild that drops client-supplied unknown fields.
- **No-op guarantee**: `ApplyClineUpstreamPin` returns its input slice unchanged
  for non-Cline accounts, for models without a pin, and when injection fails; it
  never blocks or rewrites an unrelated field. Only `provider.*` and
  `providerOptions.gateway.*` keys are written, via `sjson`, so the rest of the
  body (including client-sent `provider` sub-keys) is preserved.
- **Modes**:
  - `off` — no pin. Exclusions and sorting still apply.
  - `strict` — `only: [first selected upstream]`.
  - `preferred` — `order: [selected upstreams...]`; the gateway performs the
    fallback. Sub2API does not run its own retry loop, so `preferred` is the
    mode that mirrors the switcher's multi-upstream failover.
- **Exclusions** are never sent as `exclude`/`ignore` (the gateway ignores
  them); when the probed provider list is known they are converted into an
  `only` allow-list (`known − excluded`). If the list is unknown the UI warns
  that exclusions cannot be enforced until the model is probed.
- **Sort mapping**: Vercel accepts `cost|ttft|tps` as-is; OpenRouter uses
  `price|latency|throughput`.
- **Probing is admin-initiated and costs a little quota** (each probe, validate
  or test sends one minimal real request). Probing requires an API-key Cline
  account whose base URL is the official `api.cline.bot`; manual pinning still
  works on custom relays.

## Picking a channel, and what strict pinning costs

The probed list for a model is the set of upstreams Cline's gateway may use
(e.g. `cline-pass/deepseek-v4-pro` → alibaba, azure, baseten, deepinfra,
deepseek, novita). To keep traffic on the model vendor's own channel, pick the
channel that matches the model family — for DeepSeek models that is `deepseek`,
for Qwen it is `alibaba`, and so on — and confirm it is actually in that model's
probed list first.

Recommended order of operations:

1. **Probe** the model to learn its pipeline and channel list.
2. **Validate** the model. This pins each channel once with a minimal request and
   marks it `可用 / 限流 / 不可钉 / 密钥异常`. Always do this before pinning:
   the status is a shared-pool state that changes over time.
3. **Pin.** `strict` with one channel means "only this channel, nothing else" —
   which is the point when you do not want third-party channels.

What `strict` costs: if the pinned channel is rate limited (`429`) or cannot be
pinned for that model (model-id mapping failure → `invalid_request_error`), the
error is passed straight through to the client, because strict mode has no
fallback. That is inherent, not a bug: "only the vendor channel" and "never
fail" cannot both hold. Mitigations, in order of preference:

- re-validate later — shared-pool rate limiting is transient;
- temporarily switch the model back to `off` and let the planner choose;
- use `preferred` with the vendor channel first, accepting that the gateway may
  then fall back to another channel.

`preferred` only writes `order` (a preference). It constrains the gateway to an
allow-list only when `exclude` is non-empty, in which case
`only = probed − excluded` is injected as well.

## Models with no selectable upstream ("not pinnable")

Some `cline-pass/*` models have **no provider pool at all** — typically
single-backend or contributor models, whose canonical slug looks like
`private/<model>-contributor`. Two things happen for them, and both are
expected, not failures:

- the placeholder-provider request is **not** rejected: the gateway ignores
  `providerOptions.gateway.only` (and the top-level `provider.only` form) and
  answers normally, because there is nothing to filter;
- therefore there is no list to harvest. The probe records the observed
  upstream instead, marks the model `pinnable=false`, and the page shows a
  `不可钉` badge plus the reason in the expandable detail row
  (`渠道清单来源: gateway ignored the placeholder provider restriction; single
  upstream observed: <provider>`).

For these models pinning is a no-op: leave them on `不钉住`. What you *can* read
off the row is which upstream they always use, which is the useful answer for
"is it on the vendor channel?".

### `pinnable=false` is an inference, not proof — and you can override it

The "single upstream" verdict is derived from the gateway **ignoring** the
placeholder probe. That is evidence, not proof: a gateway that silently drops an
*unknown* provider name would look exactly the same while still honouring a
*real* one. So the page never blocks the experiment:

- the channel checkboxes are always usable, and the dialog has a **force a
  channel name** field for a name the probe never listed (`deepseek` on a model
  whose only listed upstream is `openai-compatible-private`);
- `测试` reports the adherence verdict for exactly that forced configuration, so
  "does the gateway honour `deepseek` here?" is answered directly, without
  saving anything;
- only after a test reports `adopted` is it worth saving the pin. Saving is what
  puts the field on live traffic — the injection never consults `pinnable`.

If the test reports `not-adopted` (served by something else) the pin really is
ignored for that model, and no configuration can change it.

### Private provider names

The observed provider for such a model is not one of the selectable pool names
(`deepseek`, `baseten`, …) but a private route name such as
`openai-compatible-private`. That is the gateway's own label for a
**non-pool, private provider** configured on Cline's side. It cannot be
redirected by `provider.only` / `providerOptions.gateway.only`, because there is
no pool to filter — the model is hard-wired to that route.

To establish what such a route actually is, the probe snapshot keeps two pieces
of raw evidence, both shown in the expandable detail row:

- **响应模型** (`response_model`): the model the upstream response declares —
  this is the real upstream model id, so it shows whether the private route
  serves the official model;
- **网关路由元数据** (`routing_raw`): the raw `provider_metadata.gateway.routing`
  object, including `canonicalSlug`, `finalProvider` and `planningReasoning`.

If a model only exposes a private route, the only lever left is **which model id
is called**. Use the toolbar's manual probe field to probe any id (it does not
have to be in the Cline list yet); a model that exposes a real pool will show
the usual channel chips and become pinnable.

The probe tries the detected pipeline form first and then the other form, so a
misdetected pipeline no longer silently loses the list. The distinction it
preserves:

| Harvest result | Meaning | `pinnable` |
| --- | --- | --- |
| Gateway rejected the placeholder and listed providers | Selectable pool | `true` |
| Gateway ignored the restriction and answered | Single upstream; the observed provider is recorded | `false` |
| Other error | Raw error kept in the harvest note | `false` |

## A 200 is not evidence the pin was adopted

The gateway silently drops a pin it cannot honour (an unknown `only` value, or a
model with no provider pool) and then serves the request from wherever it likes.
So a successful response says nothing about whether the pin worked.

The only honest test is the channel the response reports, compared against what
was pinned. Every verdict carries this comparison:

| Adherence | Meaning |
| --- | --- |
| `adopted` | Served by the pinned channel |
| `fallback` | Served by another channel in the selected order (`preferred` working as intended) |
| `not-adopted` | Served by a channel that was **not** selected — the pin was ignored |
| `violated` | Served by an **excluded** channel — the exclusion was ignored |
| `unresolved` | No pin target, or the response named no channel |

This is why `校验` (validate) marks a channel usable **only** when the response
names that channel. The earlier rule — "any 2xx counts as available" — reported
every channel as available on a model whose pin is ignored, and those false
verdicts then fed the exclusion allow-list. `not-adopted` is counted as `bad` in
the summary while the exact verdict stays on the row. `测试` (test) leads with
the adherence verdict, so "did my pin actually take effect?" is answered
directly rather than being left to the reader.

The two pipelines also spell one channel differently (`zai` on the Vercel
gateway vs `z-ai` on OpenRouter). The injection translates the configured name
into the spelling of the target pipeline, because the direct pipeline passes
`only` through to OpenRouter, where an unknown provider is **refused** rather
than ignored — the wrong spelling fails a request the right one would serve.

## One-click vendor pinning

`一键钉住官方渠道` (Pin vendor channels) strict-pins every model whose probed
channel list actually contains its vendor's channel, and lists everything else
as skipped:

- model family → channel alias (`deepseek` → `deepseek`, `qwen` → `alibaba`,
  `glm` → `zai`/`alibaba`, `kimi` → `moonshot`, `minimax` → `minimax`,
  `mimo` → `xiaomi`), then the canonical slug's vendor prefix, then the model id
  family;
- a model is skipped when it has not been probed, when it is not pinnable
  (single upstream), or when its list has no vendor channel;
- **the preview is mandatory**: the dialog lists exactly which model gets which
  channel and why the others are skipped, before anything is written;
- models that are not recommended keep their existing configuration untouched,
  so the action never clears a hand-made pin.

It is a convenience over the same API — nothing about it is a separate code
path, and it cannot pin a model that has no selectable pool.

## Admin API

All under the existing admin auth + audit + compliance middleware:

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/admin/accounts/:id/cline-upstreams` |
| `PUT` | `/api/v1/admin/accounts/:id/cline-upstreams` |
| `POST` | `/api/v1/admin/accounts/:id/cline-upstreams/probe` |
| `POST` | `/api/v1/admin/accounts/:id/cline-upstreams/validate` |
| `POST` | `/api/v1/admin/accounts/:id/cline-upstreams/test` |

The page is at `/admin/cline-upstreams` (sidebar entry "Cline Upstreams"), and
the Cline account row's "more" menu links to it with `?account=<id>`.

## Actual upstream in the admin request log

The pin only tells the gateway what to prefer; the authoritative answer is what
the response declares. That value is now recorded per request and shown in the
admin request log (`/admin/usage` → 使用记录 → "实际上游渠道" column), so the
pin can be verified against real traffic instead of only via the page's test
button.

Capture path (follows the existing upstream-response-model observer pattern):

- `upstream_response_model.go` gains `ObserveUpstreamProvider` /
  `Provider()` / `observedUpstreamProvider(c)`. It reads
  `provider_metadata.gateway.routing.finalProvider` (planner) and a top-level
  `provider` display name (direct, slugified), also accepting a `data.*`
  envelope. The **last** non-empty declaration wins, because a streaming
  planner response only carries the routing metadata on its final chunk. A
  `bytes.Contains(payload, "provider")` guard keeps the extra gjson lookups off
  the hot path for every other platform.
- The observer is fed by the shared CC choke point, so streaming and
  non-streaming are covered by one carrier. The Responses→CC and
  Anthropic→CC fallbacks now bootstrap the observer themselves (they previously
  recorded no upstream response metadata at all).
- The value travels on `OpenAIForwardResult.UpstreamProvider` (gin contexts are
  unreachable from `RecordUsage`, which takes a plain `context.Context`) and is
  persisted as `usage_logs.upstream_provider` (migration `262`, nullable
  `VARCHAR(64)`, no backfill).

Nothing is recorded for platforms whose responses do not declare a provider —
the column renders `—`.

## Why this is not a `.s2plugin`

The plugin system is a signed out-of-process gRPC **transport replacement** with
exactly one capability: `openai.oauth.outbound_transport.v1`
(`plugin_manifest.go` rejects any other capability, and the manifest JSON schema
pins `platform=openai`, `account_type=oauth`). Concretely, as a plugin with no
core change:

| Need | Possible? |
| --- | --- |
| Mutate the outbound Chat Completions body for a Cline account | **No** — `ShouldRouteOpenAIOAuth` only routes `platform=openai` + `account_type=oauth`; Cline requests never reach the plugin |
| Add an admin REST endpoint | **No** — there is no plugin route-registration mechanism |
| Add an admin UI page | **No** — the manifest has only `ui.entrypoint`, rendered as a `sandbox="allow-scripts"` iframe with CSP `connect-src 'none'` inside the built-in Plugins view |
| Probe Cline with the account's key | **No** — `ResolveOutboundIdentity` returns nil for anything but OpenAI OAuth |
| Store per-account config | Only for in-scope (openai/oauth) accounts |

Making (a) viable would require a new capability wired in four core places
(`plugin_manifest.go`, `pluginCapabilityAccountScopeGrants`,
`hasEnabledOpenAIBinding`, `ShouldRouteOpenAIOAuth`) **and** reimplementing the
whole Cline upstream transport inside the plugin, because the plugin *is* the
HTTP client (there is no "core sends, plugin edits the body" hook). (b) and (c)
would remain impossible. `docs/PLUGIN_DEVELOPMENT.md` §11 says explicitly not to
declare a capability the host has not implemented.

## Removing the feature later

Owner decision (2026-10-19): keep both halves, and deliberately **no** config or
admin kill switch — when the feature is no longer wanted it will be removed from
source directly, using the checklist below.

Everything is additive, so removal is deletion plus a handful of small hunks.
The two halves below are independent.

**A. Cline upstream pinning** (the panel and the injection)

Delete:

- `backend/internal/service/cline_upstream_pin.go`
- `backend/internal/service/cline_upstream_service.go`
- `backend/internal/service/cline_upstream_pin_test.go`
- `backend/internal/handler/admin/account_cline_upstream.go`
- `frontend/src/views/admin/ClineUpstreamView.vue`
- `frontend/src/views/admin/__tests__/ClineUpstreamView.spec.ts`
- `frontend/src/api/admin/clineUpstream.ts`
- `frontend/src/i18n/locales/{en,zh}/admin/clineUpstream.ts`

Revert the small hunks (each is 1–8 lines, all in one place per file):

| File | Hunk |
| --- | --- |
| `backend/internal/service/openai_gateway_cc_pipeline.go` | the `body = ApplyClineUpstreamPin(account, body)` line |
| `backend/internal/service/admin_account.go` | two delete + two preserve entries for the two `Extra` keys |
| `backend/internal/handler/admin/account_handler.go` | `clineUpstream` field + `SetClineUpstreamService` |
| `backend/internal/handler/wire.go` | the `clineUpstream` parameter + setter call |
| `backend/internal/service/wire.go` | `NewClineUpstreamService,` in the provider set |
| `backend/internal/server/routes/admin.go` | the five `/:id/cline-upstreams` routes |
| `backend/cmd/server/wire_gen.go` | regenerate with `wire` (do not hand-edit) |
| `frontend/src/router/index.ts` | the `/admin/cline-upstreams` route record |
| `frontend/src/components/layout/AppSidebar.vue` | the nav item + `UpstreamPinIcon` |
| `frontend/src/components/admin/account/AccountActionMenu.vue` | the `isCline` computed + menu button + emit |
| `frontend/src/views/admin/AccountsView.vue` | `useRouter`, `handleClineUpstream`, the `@cline-upstream` binding |
| `frontend/src/api/admin/index.ts` | the import, `adminAPI` entry and named export |
| `frontend/src/i18n/locales/{en,zh}/admin/index.ts` | the namespace import + spread |
| `frontend/src/i18n/locales/{en,zh}/common.ts` | `nav.clineUpstream` |
| `frontend/src/i18n/locales/{en,zh}/admin/accounts.ts` | `admin.accounts.clineUpstream` |

Nothing else references the feature; `ApplyClineUpstreamPin` is a no-op for any
account without a pin, so the injection line can also be left in place safely.

**B. Actual-upstream in the request log**

Populated only when the upstream response declares a provider, which in practice
means Cline Pass (planner `finalProvider` / direct `provider`) — but not *only*
Cline: any account whose base URL points at an OpenRouter-compatible relay that
echoes a top-level `provider` will fill this column in too. So it is worth
keeping while any such account exists, and it can be removed together with A
when neither does.

To drop it:

| File | Hunk |
| --- | --- |
| `backend/internal/service/upstream_response_model.go` | `ObserveUpstreamProvider`, `Provider()`, `observedUpstreamProvider`, the provider field, `upstreamProviderMaxLength`, the `bytes` import, and the call inside `ObserveOpenAI` |
| `backend/internal/service/upstream_response_model_test.go` | the six `TestUpstreamProvider*` tests |
| `backend/internal/service/openai_gateway_service.go` | `OpenAIForwardResult.UpstreamProvider` |
| `backend/internal/service/openai_gateway_usage.go` | the `UpstreamProvider:` line in the usage-log literal |
| `backend/internal/service/usage_log.go` | `UsageLog.UpstreamProvider` |
| `backend/internal/service/openai_gateway_chat_completions_raw.go` | 2 × `UpstreamProvider: observedUpstreamProvider(c),` |
| `backend/internal/service/openai_gateway_responses_chat_fallback.go` | 4 × the same line **+ the observer bootstrap** (see below) |
| `backend/internal/service/openai_gateway_messages_chat_fallback.go` | 3 × the same line **+ the observer bootstrap** (see below) |
| `backend/internal/repository/usage_log_repo_insert.go` | arg type, the 8 column lists, `$64`, `upstreamProvider` local + arg |
| `backend/internal/repository/usage_log_repo_query.go` | `usageLogSelectColumns`, the scan var, scan target and assignment |
| `backend/internal/repository/usage_log_repo_request_type_test.go` | the 4 scan stubs, 2 `sqlmock.AnyArg()` rows, 2 column-substring assertions, the shifted absolute indices |
| `backend/internal/repository/usage_log_session_id_unit_test.go` | the shifted `usageLogInsertArgTypes` / `prepared.args` indices |
| `backend/internal/handler/dto/types.go` + `mappers.go` | `AdminUsageLog.UpstreamProvider` and its mapping |
| `frontend/src/types/index.ts` | `AdminUsageLog.upstream_provider` |
| `frontend/src/views/admin/UsageView.vue` | the column entry, the export header and the export row value |
| `frontend/src/components/admin/usage/UsageTable.vue` | `#cell-upstream_provider` |
| `frontend/src/components/admin/usage/__tests__/UsageTable.spec.ts` | the stub slot + the provider test |
| `frontend/src/views/admin/__tests__/UsageView.spec.ts` | the i18n mock key, the fixture field, the two export assertions |
| `frontend/src/i18n/locales/{en,zh}/dashboard.ts` | `usage.upstreamProvider` |

Two things in there are **independent fixes worth keeping even if the column
goes**: the observer bootstrap added to the Responses→CC and Anthropic→CC
fallbacks (they previously recorded no upstream response model or service tier
at all), and migration 262 itself. `usage_logs.upstream_provider` becomes a
nullable column with no readers; leave it, or drop it with a **new** migration —
never edit 262 after it has been applied (the runner checksums applied files and
refuses to start on a change).

**C. Also remove** `AGENTS.md` → the "Cline Pass upstream pinning" section, and
the `docs/operations/CLINE_UPSTREAM_PIN_20261018.md` file itself.

Suggested order: delete A + B code and their tests, regenerate `wire`, run
`go build ./... && go test -tags unit ./internal/... ` plus
`pnpm run check:i18n && pnpm run typecheck && pnpm run test:run`, then decide
separately whether to drop the column.

`usage_logs.upstream_provider` is a nullable column with no readers once the
code is gone; leave it (harmless) or drop it with a new migration — never edit
migration 262 after it has been applied.

## Verification performed (local, no deployment)

```text
go build ./...                                             exit 0
go vet -tags unit ./internal/service/ ./internal/repository/ ./internal/handler/...   exit 0
go test -tags unit ./internal/service/                     ok (full suite)
go test -tags unit ./internal/server/... ./internal/handler/... ./internal/repository/ ./migrations/ ./internal/pkg/usagestats/...   ok
go run github.com/google/wire/cmd/wire (idempotent; wire_gen.go diff = 2 insertions/1 deletion)
frontend: vue-tsc --noEmit                                 exit 0
frontend: vitest (i18n + ClineUpstreamView + AccountActionMenu + AccountsView + UsageView + UsageTable + api)  ok
frontend: vite build                                       exit 0 (output under the git-ignored backend/internal/web/dist)
```

Unit tests cover: strict/planner, preferred/direct, unknown pipeline injecting
both forms, exclusion→allow-list conversion, excluded-upstream removal, sort
mapping, `off` ignoring selections, preservation of unrelated `provider` keys,
no-op without config and for non-Cline accounts, routing parse for both
pipelines, provider-harvest parsing, error classification, meta merge, pin
normalization on save, an end-to-end assertion that
`forwardAsRawChatCompletions` puts `providerOptions.gateway.only/sort` on the
wire for a pinned Cline account, and — for the request log — planner/direct
provider extraction, last-declaration-wins, `data.*` envelope, object-shaped
`provider` rejection, context carry and length bounding, plus the usage-table
cell and export column.

## Migration and rollback

Migration `262_usage_log_upstream_provider.sql` is a single additive
`ADD COLUMN IF NOT EXISTS ... VARCHAR(64)` with a comment; it does not rewrite
rows and is safe on a live table. Old binaries ignore the extra column, so a
rollback does not need a reverse migration (the column simply stays NULL for
new rows written by the old binary).

The feature itself is additive and opt-in per account+model:

- With no `cline_upstream_pins` entry the gateway body is byte-identical to
  before, so reverting the deployment restores previous behaviour with no data
  change.
- The `Extra` keys are inert data; they can be cleared per account with
  `PUT /cline-upstreams` with `{"pins": {}}`, or left in place across a
  rollback.
- No background job, no new port or Caddy rule.

## Known limits

- Pinning is per account+model, not per API key or group. A group that mixes
  several Cline accounts uses each account's own pin.
- `strict` pins a single upstream; a hard failure does not try the second
  selection. Use `preferred` when a fallback is wanted.
- The provider catalogue snapshot is refreshed only by probing, and provider
  availability (`limited`) is a shared-pool state that changes over time;
  re-validate before treating a channel as permanently unusable.
- Observed actual provider is recorded on probe/test, not on every live request,
  so the data plane keeps zero extra parsing cost.
