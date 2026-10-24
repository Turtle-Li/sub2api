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
- The AWS active blue container is healthy, restart count zero, no OOM, revision
  `b7743bf17501919e74d83546bfff4eba47dfa6a2`, image ID
  `sha256:f6cf2b249ca6bd7ebdb3dd3921bca6f4eb9dbded8d1b66eea44a338a92e8afc5`.
  Caddy points only to `sub2api-blue:8080`; green is stopped.
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

Exact commit, hosted CI, release workflow and production smoke results are
recorded below as they complete.
