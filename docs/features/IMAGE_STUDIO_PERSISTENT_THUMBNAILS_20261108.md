# Image Studio persistent thumbnails

结论：复用已有生图任务表和作品条目表，无须另建一套生图任务。新结果在处理
图片时预存云端缩略图；历史缺失图按批次补齐，后续冷启动直接读已存缩略图，
浏览器可缓存及校验。已在本地实现，尚未提交、推送或部署。

## Decision and acceptance

The owner requested eager cloud thumbnails and browser caching on 2026-11-08.
Reuse `batch_image_jobs` and `batch_image_items` as the generation authority;
do not create a second generation task table or derive history from usage logs.
Store a versioned 480px JPEG cover per item in the existing private delivery
object store. Job/custom ID determine its exact key; completed results are immutable.
The database retains lifecycle and backfill retry fences, not image bytes.

The fixed reference is VSO `4edef85ab3716fd43989b4a49561a148ba500d93`:
`backend/src/main/java/com/vso/controlplane/service/ImageVariantStorageService.java`
and its matching test under `backend/src/test/java/`. Its eager-store/no-original-read
and historical-backfill-once tests were inspected, together with
`ImagePreviewService` and `GalleryCardAssetService`. Adopt those behaviors without
copying Java code. Retain host session/key ownership, retention, batch settlement,
private authenticated delivery, and explicit original downloads. The Vertex
archive delivery path retains its separate archive-client contract.

Alternatives: process memory alone repeats original reads after restart; a new
generation task table duplicates lifecycle; a new independent storage provider
duplicates credentials and cleanup. The existing private COS direct-I/O interface
already supports seekable byte uploads and exact-object reads/deletes.

## Task DAG (PLAN_READY)

One local Developer owns all writes in this worktree. Read-only architecture
inspection precedes implementation. No production operations or submission.

| ID | Depends | Allowed writes / result | Acceptance and evidence | Rollback / invalidation |
| --- | --- | --- | --- | --- |
| P1 | — | Thumbnail service/repository and additive migration | Persisted covers survive a fresh service instance; exact object keys; lifecycle write fence and shared batch backfill lease | Old binary ignores additive columns; retain objects until compatible cleanup |
| P2 | P1 | Indexer/upscale hooks and runtime wiring | Encode while source bytes are already available; preview failure never fails generation or changes billing; high-resolution cover uses final bytes | Disable hook; original output/billing stays intact |
| P3 | P1 | Historical reader and cleanup | One scan covers missing successful items of a batch; no repeated per-card scan; bounded decoding/time/retry; disable writes before output cleanup | Stop backfill; preserve generation jobs and originals |
| P4 | P1–P3 | Thumbnail HTTP handler/client | Private browser cache plus ETag/304; auth before cache response; no original fallback in browser | Retain original explicit download endpoint |
| P5 | P2–P4 | Tests and project reference docs | Cold/restart source-read counts, failure, authorization, deletion and migration tests; existing image suite regression | Any failing lifecycle/privacy test blocks completion |

All steps use source contracts in the session API, gallery optimization and
Image 2.5 upscale documents. A change to provider submission, settlement,
credentials or production topology invalidates this scope and requires separate
analysis. Crashed historical reads retry after the shared lease expires;
successful covers are reused, and failed backfills have a durable cooldown.
Browser freshness remains five minutes, matching the existing privacy boundary.

## Validation

P1–P5 implemented locally. Evidence is under
`.omo/evidence/persistent-thumbnails/` in this worktree.

| Evidence | Observed result |
| --- | --- |
| `backend.jsonl` | Six packages pass: service, repository, handler, routes, migrations and server; 168 top-level tests / 355 cases including subtests |
| Eager indexing regression | One normal indexing source scan (38,417 fixture bytes); first list on a fresh service instance adds **0 original opens / 0 original bytes**, without a provider/account resolver |
| Historical regression | First missing item scans the batch once (38,417 fixture bytes); another item on a fresh instance adds **0 original opens / 0 original bytes** |
| Lifecycle and failure tests | Shared backfill lease prevents a competing read; failed storage preserves successful generation counts; cooldown blocks repeated scans; expiry/deletion rejects warm cache hits and late writes |
| Final-resolution test | The 2K cover exactly matches a thumbnail rendered from the stored final image, with one provider read for indexing |
| HTTP journey | Real Gin handler and download service return JPEG, private cache headers and ETag; authenticated matching/weak ETag returns 304 with no payload; no login, wrong key and deleted output cannot receive 304/data |
| Initial `frontend-tests.log` / `eslint.log` / `frontend-build.log` | Original54-test snapshot passes; release revalidation now passes62 studio cases plus lint/type checking/build, recorded in `docs/operations/IMAGE_STUDIO_GALLERY_RELEASE_20261108.md` |
| `race.log` | Thumbnail service/repository/HTTP cases and COS missing-object classification pass under Go's race detector |

The first new cold-read tests failed against the memory-only implementation
(`BATCH_IMAGE_DOWNLOAD_FAILED`), then passed with persisted objects. This is
developer verification with synthetic data and a local SDK HTTP fixture, not a
production bucket/network timing claim. Wire was regenerated. Its startup test
had a pre-existing missing `channelMonitorV3GroupProber` argument; the fixture
now supplies the corresponding nil, and cleanup test compilation passes.

## Storage, lifecycle and failure contract

- Metadata paging reads only the existing job/item tables. No request-log scan,
  new generation queue or second billing ledger is introduced.
- Keys are `<delivery-prefix>/thumbnails/v1/<batch-id>/<hashed-custom-id>/00.jpg`.
  A version fixes 480px long edge, quality80 JPEG and white alpha composition.
  Immutable completed output plus exact job/custom identity defines the source;
  HTTP ETag is the SHA-256 of the actual derivative bytes.
- The existing private COS runtime must be configured with exact-object
  Get/Put/Delete capabilities under its delivery prefix. No browser credentials,
  public object URLs, new account secrets or activation settings are introduced.
  Missing storage fails explicitly; the browser never falls back to originals.
- Eager capture runs while bytes are available. Its per-indexing-pass budget is
  10 seconds total, at most five seconds per attempt, reserving the parent's last
  30 seconds for authoritative work. Provider/Mini wait time does not consume
  that budget. Missing derivatives can be backfilled later; capture errors do
  not enter billing or fail a successful result.
- Historical source reads have a 90-second deadline, two process-wide backfills,
  the existing per-user download permit, and a renewable shared batch lease
  (existing Redis fence; existing DB advisory fallback only without Redis).
  `thumbnail_retry_at` limits failed/missing-source rescans for two minutes.
  Shared backfill continues under its own deadline when a reader leaves. A
  failure cooldown returns non-retryable409; a peer-held lease returns503.
  Download contention does not consume the cooldown. Finished objects survive
  request cancellation, restart and LRU eviction.
- Source bytes are capped at32MiB (normal) or128MiB (upscaled results), with the
  same40MP pixel check and two concurrent decoders. One bad upscale does not
  skip other covers; legacy unmarked items still use their original JSONL. Stored
  covers are capped at 1MiB. Memory/browser LRU limits remain unchanged.
- `thumbnail_disabled_at` is written before output cleanup. Each Put holds the
  job row lock while checking indexing/completed state, user deletion, output
  deletion, expiry and this fence. Cleanup waits for active writes and then
  deletes the exact versioned keys, including failed/pending item identities.
  Future reads check owner/key, retention and the fence before any cache hit.
- HTTP caching is private for 300 seconds, varying on session Authorization and
  selected API-key ID. Stale cache revalidation still authenticates before 304.
  The mounted frontend keeps its existing bounded blob LRU; 503 preparation
  responses receive bounded, cancellable GET retries, never generation replay.
  Preview requests have an independent serial queue, so they never queue ahead
  of explicit originals. Each card aborts its request when leaving the viewport;
  workspace cancellation also aborts active reads and retry waits.
- Vertex archive-only `result_available` items retain their documented external
  archive client behavior. Expired/deleted/unreadable sources are not regenerated.

Migration271 adds only two nullable job columns; no existing task/image/financial
rows are rewritten. Keep columns and private artifacts on rollback. A rollback
runtime must retain derivative cleanup or have a separately reviewed cleanup
arrangement, rather than leaving old private artifacts past output retention.
Deployment, live storage verification and migration execution remain outside
this local change. The branch preflight reported `CANONICAL_NOT_READY` because
this existing managed checkout has no resolved `.agent-worktree.toml` trunk;
work stayed in the already attached worktree with its earlier edits preserved.

```yaml
knowledge_candidate: no
knowledge_candidate_reason: Project-specific derivative storage and ownership contract
candidate_type: not-applicable
origin_repo: Turtle-Li/sub2api
origin_path: docs/features/IMAGE_STUDIO_PERSISTENT_THUMBNAILS_20261108.md
origin_revision: 06b619a2d7b1bd611b518c217d8f72b5349c3fc9
source_change_set: image-studio-gallery-optimization
classification_suggestion: not-applicable
```
