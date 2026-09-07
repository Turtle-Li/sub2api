# Sub2API v0.2.2 update and model allowlist compatibility

## Scope and fixed references

User request: merge the new upstream release and check group model allowlists
so existing clients remain usable. Merge baseline is fork main
`0fca492ac` and upstream release `v0.2.2`, peeled revision
`5485f368b29d05adb95a00f71801c7c23d8f48af` from
https://github.com/Wei-Shaw/sub2api. The release tag still contains VERSION
`0.2.1`; the fork build version is explicitly set to `0.2.2`.

Upstream contracts were inspected in migration 235, Ent group schema,
`internal/service/group_model_allowlist.go`, auth cache, gateway middleware,
Codex model listing, batch image handler, and their tests. Existing LGPL-3.0
license and notices are retained. Native Responses image account admission
continues to follow `RESPONSES_IMAGE_ACCOUNT_ROUTING_20260907.md`.

## Acceptance and delivery dependencies

- AC1: merge v0.2.2 without removing the fork's fixed-egress, billing, probe
  metadata, image completion, and native image account admission behavior.
- AC2: preserve all existing group allowlist enabled flags and stored models;
  disabled lists must not reject models used by current clients.
- AC3: old and new application generations can read/write the same group
  configuration during blue-green release and rollback.
- AC4: focused and broad backend tests, frontend typecheck/build/tests, actual
  PostgreSQL migration and repository projection tests pass before publication.

T1 root pins revisions and audits live state. T2 root resolves gateway/cache/
image conflicts while bounded workers resolve disjoint admin and WS-test
files. T3 follows T2: root integrates and verifies; independent read-only QA
checks the exact frozen result. T4 follows T3: merge tested source into fork
main; build the exact main revision through the explicit GitHub workflow.
Application release uses the installed lock-owning receiver, verified image
identity and node state, candidate real-request gate, health and rollback.
Do not switch DNS or transfer cross-node background ownership for this update.

## Read-only production audit

On 2026-09-07, the shared production database had 13 undeleted active groups
(4, 6–14, 16–18), all with `models_list_config.enabled=false`; none needs to
be enabled or widened for this update. The new physical column did not exist.
Last-24-hour usage included GPT-5.6 sol/terra/luna in groups 4/6/12 whose old
saved lists omit those models, and Claude Opus 5 in group 14 whose saved list
omits it. Therefore enabling those saved lists would reject legitimate traffic.
Preserve disabled state. If an administrator later enables a group allowlist,
review its public model aliases against actual usage and intended access first.
An enabled list is an admission policy, not just a model-picker preference.

## Bug analysis: V022-ROLLING-GROUP-COLUMN

Observed in isolated PostgreSQL 18: applying upstream migration 235 to an
old-format groups table makes `SELECT models_list_config FROM groups` fail
with `column does not exist`. Old production binaries issue this SQL through
Ent. A new blue-green generation runs migrations before old connections drain;
both origins also share the database. This is a deterministic schema failure,
independent of provider availability or whether allowlists are enabled.

The chosen compatibility boundary is the Ent storage key:
`field.JSON("model_allowlist", ...).StorageKey("models_list_config")`.
Migration 235 updates the column comment and preserves physical storage and
all JSON data. New API/domain names and upstream enforcement remain
`model_allowlist`. No duplicate column, trigger, or data rewrite is needed.
Generated Ent code must be regenerated when this mapping changes.

This is an intentional fork difference from the fixed upstream migration.
Keep it while old binaries are available for rollback. A physical rename is
only permissible in a separately planned migration after every legacy reader
and writer has been retired; the project maintainer owns that decision.
Do not replace the fork migration with upstream SQL during routine merges.
Auth snapshot version 25 invalidates both incompatible local/upstream v24
layouts so a legacy display-list payload cannot silently bypass admission.

## Validation commands

```sh
cd backend
go test ./internal/service ./internal/handler ./internal/handler/admin ./internal/repository ./internal/pkg/claude ./migrations
go test -tags=integration ./internal/repository -run 'Test(GroupModelAllowlistSharesLegacyStorageForRollingUpgrade|GetByKeyForAuthCarriesGroupModelAllowlist|MigrationsRunner_)' -count=1
cd ../frontend
pnpm typecheck
pnpm test:run
pnpm build
cd ..
bash deploy/tests/group-model-allowlist-migration-test.sh
```

The migration regression uses an isolated PostgreSQL container without a
network or host port. Repository integration tests verify new Ent creation,
legacy SQL reading/writing, new Ent reading/updating, and authentication
projection. Native Responses HTTP/WS image tests and model allowlist tests
must both remain present and pass. Test results and immutable publication
identity are recorded when verification completes.

## Operational boundaries

Current application topology is verified live before release, not inferred
from older candidate-only documentation. Azure uses SSH alias
`sub2api-candidate` (user `lijiayu`, `/opt/sub2api`); the old rollback origin
uses `sub2api-new`; shared data services use `sub2api-db`. Exact identities
remain device-local and in the private Registry. No secret is stored here.
Each node must retain its current background role: active on the serving
Azure origin, standby on the old origin. Release the latter only with
`SUB2API_RELEASE_BACKGROUND_MODE=preserve-standby` through its installed
receiver. Do not use the workflow's default old-origin activation path.
Shared provider/account capacity errors are not evidence for DNS rollback.

## Additional merge seams

- The new standard OpenAI model discovery path resolves the logical account's
  proxy with `ResolveAccountProxyURL`, just like existing Codex discovery. A
  configured but unavailable proxy must fail before any upstream request.
- The fork's bounded Anthropic account-window precedence is retained. When an
  explicit shared window is exhausted, an additional credits-required signal
  does not install a longer model entitlement cooldown beyond account recovery.
  This applies equally to bare and detailed signals. The upstream overlap test
  retains its shared-window/reset assertions and expects no extra model write;
  standalone model entitlement and explicit Fable 7d_oi tests remain intact.
- Stream terminal failure diagnostics use one `stream_failed` event, preserving
  upstream request identity and the existing capacity/usage side effects.
  The newly overlapping diagnostic call is coalesced; a regression asserts
  exactly one event after native and passthrough semantic output.
- The simple-mode test expects the sanitized optional long-context override to
  be absent, retaining the fork service's established default behavior.
- New frontend locale completeness checks exposed four missing local bandwidth
  alert translations. Both locales now provide them. The upstream Codex manifest
  test uses the current auth store and renamed allowlist API fixture.
