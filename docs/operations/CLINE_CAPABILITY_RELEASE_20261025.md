# Cline capability pin release, 2026-10-25

The owner authorized fixing the existing lint warning, committing/merging the
Cline capability pin change to `Turtle-Li/sub2api` main, and publishing after
verification. The implementation and 24 live Cline requests are documented in
`CLINE_CAPABILITY_PIN_20261024.md`; the G704 false-positive analysis is in
`../bugs/backend/BUG-20261024-model-catalog-g704.md`.

## Scope and preflight

- Source baseline: `c4d096ebea9dbbcd0a495ebe56f815181e6f2484`.
- Only configured restrictions on `cline-pass/deepseek-v4.1-flash-1017`
  automatically add the two verified gateway capabilities. Pro, ordinary Flash
  and unconfigured models retain their existing request behavior.
- Management pin tests reject embedded JSON/SSE errors and require provider
  evidence. No dependency, migration, billing or live account/group change.
- The G704 warning was reproduced on the baseline and traced to the analyzer's
  missing custom sanitizer support. Its point-specific security explanation
  preserves all existing URL/redirect/resolved-IP policy. Ten policy cases
  prove rejected destinations never receive a request or account credentials.
- Root-owned release configuration confirms owned fork/main, GitHub source
  `https://github.com/Turtle-Li/sub2api`, background mode `activate` and the
  authenticated release request probe enabled.
- Before the initial release, AWS blue was healthy, restart count zero, no OOM, revision
  `b7743bf17501919e74d83546bfff4eba47dfa6a2`, image ID
  `sha256:f6cf2b249ca6bd7ebdb3dd3921bca6f4eb9dbded8d1b66eea44a338a92e8afc5`.
  Caddy pointed only to `sub2api-blue:8080`; green was stopped.
- Use the existing explicitly dispatched GitHub production workflow, its shared
  concurrency group and host maintenance lock. No source build or raw container
  lifecycle commands run on production. The current verified blue image is the
  compatible application rollback; retain usage/financial data and account pins.

## Verification and publication

Full backend lint v2.14.0 passed with zero issues. The focused Cline/catalog and
HTTP upstream tests passed with race detection and shuffled order (service
3.304s, repository 11.587s). `go test -tags unit ./... -count=1`,
`go build ./...` and `git diff --check` passed. The main session reviewed the
complete implementation/test diff and verified model scope, no-op boundaries,
final-send placement, error handling and credential-safe reproduction scripts;
this is a self-review, not an independent reviewer verdict.

## Initial deployment and acceptance finding

- Warning disposition: `80ee1a754`; capability pin source:
  `7b3c389db2551ecee23ba6f225795c2e638d6378`, pushed to owned main.
- [CI 40110363761](https://github.com/Turtle-Li/sub2api/actions/runs/40110363761)
  passed all six jobs, including backend unit/integration, frontend/embed, full
  lint and release helper checks for that exact source.
- [Production workflow 40111474861](https://github.com/Turtle-Li/sub2api/actions/runs/40111474861)
  succeeded at 2026-10-24 16:35:58 UTC. Healthy green serves the exact source;
  restart count is zero, no OOM, and Caddy points only to green.
- Image: `sub2api:auto-20261024-093515-7b3c389d`; loaded image ID:
  `sha256:a36e84b9f533da42c5f144910083741894bd66bdd6c7e41d3d78f819991278b5`.
  Archive SHA256:
  `56a5626297d454c4b3cb9840a19f14c00748659078d47a25c4483ac1128c98d3`,
  87,141,611 bytes. Release logs:
  `/var/log/sub2api-release/gha-20261024-093515-7b3c389d-3542483`.
  Application 5xx/fatal and Caddy 5xx audit counts were all zero.
- Real management tests adopted DeepSeek with canonical slug
  `deepseek/deepseek-v4.1-flash-1017`; an invalid provider returned an error
  without an answer. Temporary tests preserved all stored pins. Before this
  deployment, both controls returned the private provider, demonstrating the
  behavior change on the same account and SKU.
- The ordinary public Chat Completions request also returned DeepSeek routing
  metadata, but its usage row had NULL provider attribution. This is an
  existing nested-metadata parsing gap, reproduced separately before repair:
  [BUG-20261025-cline-provider-attribution](../bugs/backend/BUG-20261025-cline-provider-attribution.md).
  This finding kept acceptance open until the forward fix below was deployed
  and the public response and usage attribution agreed.

The main session also reviewed the two-path observer repair. Independent agent
review could not run because the available role routing was incompatible; no
independent reviewer verdict is claimed.

## Provider attribution forward fix

Source `819dce3c016d3dc116473c2b43295a7d4d7d1a8a` adds only the two missing
message-level routing lookups, preserving existing top-level/direct precedence
and unknown provider behavior. The regression first failed on the original
observer, then passed along with the shared observer, raw forwarding and Cline
suites under race detection and shuffled execution (6.119s). Full backend lint
again returned zero issues; the build and diff checks passed.

[CI 40113452002](https://github.com/Turtle-Li/sub2api/actions/runs/40113452002)
passed all six jobs for this exact main commit, including full backend unit and
integration tests. Before dispatch, the remote main SHA was checked again and
matched. A fresh production inspection confirmed healthy green at `7b3c389d`,
zero restarts/no OOM and Caddy pointing only to green; this is the compatible
application rollback for the forward fix.

## Final deployment and acceptance

[Production workflow 40114632600](https://github.com/Turtle-Li/sub2api/actions/runs/40114632600)
succeeded at 2026-10-24 17:16:03 UTC (October 25 01:16:03 CST) for source
`819dce3c016d3dc116473c2b43295a7d4d7d1a8a`.

- Active container: `sub2api-blue`, healthy, zero restarts, no OOM, exact source
  label verified. Caddy routes only to `sub2api-blue:8080`; green at `7b3c389d`
  is stopped and retained as the compatible application rollback.
- Image: `sub2api:auto-20261024-101449-819dce3c`; loaded image ID:
  `sha256:0ba614ba6da445de8e17e10fdb97132fa64579adbf100b752b1ee060ea1db972`.
  Compressed archive SHA256:
  `19d9b4b6f99b57e267b9c34ed1c03ef07a11b57f9b06f4dccd1c25ecb171f0c3`,
  87,133,867 bytes. Archive and source identity checks passed before activation.
- Release logs:
  `/var/log/sub2api-release/gha-20261024-101449-819dce3c-3581996`.
  Application 5xx/fatal and Caddy 5xx audit counts were zero; background mode
  is `activate`. The canonical authenticated release probes passed.
- The Caddy release recorded the same requested/final SHA256:
  `8469457ee548ed85c3cb88f0a61359d32ee2902a6ef3919ce87acf2752857257`,
  with active slot `blue`.
- Management positive control: DeepSeek, `adopted`, canonical slug
  `deepseek/deepseek-v4.1-flash-1017`, 1220 ms. Negative control:
  `not-a-real-provider` rejected with gateway 400, no answer, 279 ms. All stored
  model pins were identical before and after the temporary tests.
- The ordinary public Chat Completions call returned HTTP 200 and DeepSeek
  routing metadata. Its matched new usage row `926760` records account `221`,
  requested alias `deepseek-v4.1-flash-1017`, upstream SKU
  `cline-pass/deepseek-v4.1-flash-1017`, and provider `deepseek`. This closes
  both the actual forwarding and usage-attribution acceptance checks.
- API and www `/health` both returned HTTP 200 with `status: ok`; the www
  homepage returned HTTP 200. The first aggregate smoke exited at the www
  health step because the default Python User-Agent received Cloudflare 1010
  (403). The same URLs with a browser User-Agent returned 200. The helper's
  health request now uses that verified agent, and the separate health run
  passed. No application or edge policy change was needed for this probe issue.

Existing NULL provider rows remain unknown; no historical identity was inferred
from configured pins. Cline routing is external behavior, so future checks
should retain the real positive/negative controls. Final operational-record
updates contain documentation only; the deployed and CI-verified code source
remains `819dce3c016d3dc116473c2b43295a7d4d7d1a8a`.
