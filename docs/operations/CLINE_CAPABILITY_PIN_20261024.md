# Cline Pass capability-assisted pinning (2026-10-24)

## Verified scope

At source baseline `c4d096ebea9dbbcd0a495ebe56f815181e6f2484`, the owner
requested testing `providerOptions.gateway.only` together with
`has: ["vision", "implicit-caching"]`. Eighteen bounded real requests used the
existing official Cline account 221 and its protected runtime injection, without
changing account configuration or persisting credentials. Each model received
six requests: valid/invalid `only` over SSE, valid/invalid `only + has` over SSE,
and valid/invalid `only + has` over JSON. The invalid provider was
`not-a-real-provider`; successful answers contained `OK`.

| Pass model | `only: deepseek` | Invalid `only`, no `has` | `only: deepseek` plus `has` | Invalid `only` plus `has` |
| --- | --- | --- | --- | --- |
| `deepseek-v4.1-flash-1017` | `openai-compatible-private` | Answers from the private provider | `deepseek`, JSON and SSE | Rejected in JSON and SSE |
| `deepseek-v4.1-flash` | `openai-compatible-private` | Answers from the private provider | Still `openai-compatible-private` | Still answers from the private provider |
| `deepseek-v4-pro` | `deepseek` | Rejected | Rejected: `no_capable_providers_available` | Rejected |

For Flash-1017, the successful route names
`deepseek/deepseek-v4.1-flash-1017`; the private contributor attempt has zero
provider attempts before the DeepSeek attempt succeeds. Its planning metadata
explicitly records both the provider restriction and the required capabilities.
This supports the observed routing change but does not establish Cline's
internal implementation. The response model can remain a `vmc/*` alias; the
request continues to use the original `cline-pass/*` subscription SKU.

All rejected streaming requests had HTTP 200, an SSE error event, no answer,
and `[DONE]`. The rejected non-streaming requests had HTTP 500 wrapping the
gateway's 400 error. Credit cost was not independently measured; the screenshot's
`creditsUsed: 0` is not asserted for this account.

## Analysis before repair

Bug: Cline pin verification can report success without provider evidence.

现象: A 200 response containing an error is treated as unresolved routing and
can be reported as `ok=true`; a probe also accepts 200 error bodies.

复现条件: `TestClineUpstreamService_TestRejectsEmbeddedErrors`,
`TestClineUpstreamService_ProbeRejectsEmbeddedError`, and
`TestClineUpstreamService_TestRequiresProviderEvidence` reproduce the failure
at the baseline. The new capability tests also fail because no `has` is sent.

调用链: Admin test/probe/validate -> `chatCompletion` -> status-only success
branch -> routing parser -> `ClinePinAdherenceOK(unresolved)` returns true.
`clineUpstreamErrorMessage` handles JSON errors but not SSE error frames.

影响范围: Cline management probes and verification, plus the shared outbound
pin injector. No schema, billing, credential, deployment or scheduling change.

疑似根因: Confirmed directly in source and failing tests: success is decided
before checking embedded errors, unresolved adherence counts as success, and
the existing planner restriction omits the capability filter needed for this
specific Flash-1017 virtual model. Other models demonstrably have different
contracts; they must not receive the same default filter.

推荐修改方案: Supplement only configured Flash-1017 routing restrictions with
the two verified capabilities at the existing final-send injection seam. Reuse
the injector for channel harvesting and validation. Check JSON/SSE errors before
status success, and require observed provider evidence for a successful pin test.

风险: Cline may change its virtual-model capabilities or routing. Never remove
`has` or retry another route silently after a failed strict pin. Re-probe and
re-test both valid and invalid providers after a change. Unconfigured accounts
and models remain byte-identical; Pro and ordinary Flash keep existing behavior.

## Reference contracts

The existing reference remains
[`cline-pass-switcher` at `02538a3137a08948a94f26f17dd4aeff8c029ed1`](https://github.com/munmunjaklin458-afk/cline-pass-switcher/tree/02538a3137a08948a94f26f17dd4aeff8c029ed1).
Before editing, `server.js` (`parseRouting`, `harvestAvailableProviders`,
`injectPrefs`, `buildAttempts`), the README's pipeline table, and `package.json`
were inspected. This version has no automated test script or test files, and
does not set `has`.

Adopted contracts: keep the two provider-write forms, original subscription SKU,
single final-send seam, account-managed metadata, and real provider readback.
Intentional difference: the verified Flash-1017 capability filter is sent even
when an old snapshot says `direct`; other models retain snapshot-based routing.
The local probe and validation now use the same injector as real traffic.
No upstream implementation was copied. The reference is MIT, copyright 2026
cline-pass-switcher contributors; future copied source must retain its notice.

The [Vercel gateway capability contract](https://github.com/vercel/ai/blob/main/content/providers/01-ai-sdk-providers/00-ai-gateway.mdx)
defines `has` as the intersection of provider-model capabilities. It is not a
prompt or a guarantee that every model supports the two chosen values. The
Cline account's real positive/negative results above govern this change's scope.

## Implementation and validation

`cline_upstream_pin.go` supplements only a configured Flash-1017 restriction
(strict, preference or exclusion allowlist) with the verified `has` values.
Additional client capability values are retained without duplicates. Empty,
off-only and sort-only configurations do not activate the filter. The existing
`sendCCUpstreamRequest` seam applies it after model mapping and protocol
conversion, preserving the subscription SKU.

Channel harvesting and validation reuse this injector. Their metadata writes
still use the two existing managed account `Extra` keys. The initial discovery
request remains unrestricted; its provider is an observation of that request,
while the placeholder request establishes whether a selectable pool exists.

Management test/probe/validate check embedded JSON/SSE errors before accepting
HTTP success. The existing SSE frame parser handles complete frames, CRLF and
`[DONE]`. Unresolved pin adherence no longer counts as a successful test, and
an empty non-2xx response retains its HTTP failure. No retry, fallback removal,
new dependency, schema, billing or live configuration change was introduced.

Developer evidence:

- Existing focused Cline tests passed at the baseline. New capability and
  embedded-error tests failed before the production changes, then passed.
- Focused unit tests passed after implementation, including stale pipeline
  metadata, preservation of client capabilities, opt-in/no-op boundaries,
  actual final-send injection after model mapping, harvesting, validation,
  JSON/SSE errors and missing provider evidence.
- Full service unit suite: `go test -tags unit ./internal/service -count=1`,
  passed in 245.336 seconds. `go build ./...` passed.
- Focused Cline tests also passed with `-race -shuffle=on -count=1` in
  3.621 seconds. `go vet -tags unit ./internal/service` and `git diff --check`
  passed; the changed Go files are formatted with `gofmt`.
- The CI-pinned golangci-lint v2.14.0 command
  `run --timeout=5m ./internal/service/...` reported one existing finding:
  `model_protocol_catalog.go:236:40`, gosec `G704` (SSRF taint analysis).
  Running the same command against the unchanged baseline
  `c4d096ebea9dbbcd0a495ebe56f815181e6f2484` reproduced the same single finding.
  This change does not modify that file or suppress the warning. Full lint
  therefore remains failing on the baseline issue.
- A temporary Go build overlay exercised the modified `ClineUpstreamService`
  against the real Cline API with the existing runtime key held only in memory.
  Four scenarios passed in 14.42 seconds: valid pin (`deepseek`, `adopted`),
  invalid pin rejected, probe (`pinnable=true`, 16 normalized candidates), and
  validation (`deepseek=ok`, invalid provider=`bad`). Its repository was an
  in-memory test implementation; no production metadata or pins were written.
  This added six real requests to the 18-request comparison above.

Local, non-secret reproduction artifacts are under the workspace's
`review-artifacts/cline-capability-pin-20261024/`: `live_probe.py`,
`live_gateway_test.go`, and `live_overlay.json`. These are local verification
tools, not production commands or credential records. The script refuses
redirects and emits only routing/status evidence.

The owner subsequently authorized resolving the baseline warning, committing to
the owned repository's `main`, and releasing after checks pass. The G704 analysis
and scoped false-positive disposition are in
`docs/bugs/backend/BUG-20261024-model-catalog-g704.md`; full backend lint now
reports zero issues. Release evidence is recorded in
`docs/operations/CLINE_CAPABILITY_RELEASE_20261025.md`.

The existing strict Flash-1017 pin needs no configuration edit once this code is
deployed. Cline's future routing remains an external dependency; use the same
positive/negative pair to revalidate it.
