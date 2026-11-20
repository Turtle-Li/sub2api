# Image Studio: model visibility and history availability

## Observed on production, November 19, 2026 (Pacific time)

- The active application matched source `ac835ab6f`. Schedulable OpenAI
  accounts in the batch-enabled, group-priced pay-as-you-go pool already mapped
  `gpt-image-2.6-flare` and `gpt-image-2.6-sunburst` to themselves. The Image
  Studio batch allowlist and wildcard candidate list were still capped at 2.5.
  Separately, the dedicated `生图` group had batch generation disabled; that
  setting does not block the already eligible pay-as-you-go group.
- The [official Images API model list](https://developers.openai.com/api/reference/resources/images/methods/generate)
  documents Flare and Sunburst IDs for both versions, plus dated 2.5 snapshots.
  It does not list `gpt-image-2.5` or `gpt-image-2.6`. Those bare IDs appear
  in saved gateway configuration, not as proof of official support. Account
  251 maps them to themselves. A read-only 14-day usage query found positive
  image counts for **both generation and editing** with each bare ID on that
  account; `upstream_model` was the same bare ID. This proves historical
  compatibility for that relay, not which variant it runs or that every account
  accepts the alias. Leave the existing API and Model Plaza entries unchanged.
- The current Google route is the Gemini group’s Vertex AI Batch provider with
  its service-account admission and managed GCS delivery. The separate
  `gemini_api` provider remains supported; provider identities are not merged.
- Wan and Seedream already have the tariff-aware runtime, schedulable accounts,
  exact model mappings and per-model CNY price cards. Their four groups are
  exclusive. The current owner had neither group grants nor active bound keys.
  The catalogue was correctly empty for those providers. An authorized user
  needs a key in a granted group, not a new adapter or blanket admission.
- Owner history over the preceding 30 days: **41 successful items, all cleaned
  after the 72-hour retention period**, and **3 failed items**. Cleanup events
  corroborated removal; this was not merely an expired browser URL. The three
  failures were Gemini result delivery, Wan standard result storage after
  processing, and a Seedream Pro attempt with an unknown outcome. The latter
  was not replayed. None incurred a captured charge. Historical operational
  evidence: [native results](../operations/IMAGE_STUDIO_LOAD_NATIVE_RELEASE_20261108.md)
  and [initial Gemini result delivery](../../deploy/IMAGE_STUDIO_RELEASE_20261107.md).

## Changes and boundaries

- Batch catalogue expansion, request validation and execution recognize the same
  two exact Image 2.6 variants; account mapping, group allowlist and existing
  prices still decide whether an option is offered. The workbench prefers an
  available 2.6 Sunburst (then 2.6 Flare) for an untouched selection; explicit
  picks and edit continuations survive catalogue refreshes. The private admin
  test picker also shows exact known Images variants explicitly mapped but
  omitted by a compatible relay's `/models`. This does not change shared model
  discovery, add a generic alias, or imply that the route has been tested.
  Direct gateway and pricing contracts are unchanged.
- The picker uses **Gemini** for both transports and shows exact IDs when opened.
  Same-ID options on distinct routes remain separate. Wan/Seedream tabs with no
  eligible models show a key-management action rather than selectable placeholders.
- Gallery metadata exposes the existing output deadline. **可查看** selects only
  completed, unexpired items with output; **全部** retains task metadata and
  distinguishes expired media from generation failure. Expired records do not
  fetch thumbnails or originals or allow downloads or edit continuation. Actual
  failures retain their reason and technical details. Undecodable previews use
  a placeholder. Completed Vertex `result_available` items show an explicit
  archive action. It reads the owned batch's short-lived capability only on
  demand and parses the selected result locally; the gallery never downloads
  JSONL. Existing ownership, pricing and settlement ledger remain unchanged.
  Removed objects are not retried or reconstructed.
- A rejected Gemini result-archive request keeps its HTTP stage and a
  recognized Worker rejection code, never its response text, path or signed
  URL. Only the Worker's signed application-level `GET` 404 `NOT_FOUND` is
  treated as a reason to attempt creation; an edge HTML 404 is not.

## Output retention in this candidate

Successful new jobs default to `360` hours from settlement. Direct outputs and
thumbnails use the existing private Sub2 COS storage. The Vertex route stages
results in managed GCS and archives JSONL to COS before settlement. PostgreSQL
stores the ownership, job and expiry metadata. Originals, thumbnails, ZIPs
and archive capabilities recheck the persisted deadline; signing is bounded by
the remaining lifetime. Physical cleanup still runs asynchronously. Reference
input retention is unchanged. Vertex source-output cleanup still follows the
existing coordinated job cleanup and any separate GCS bucket lifecycle.

The current active runtime has no output TTL override in its YAML or environment.
The private COS bucket deletes current objects at 30 days and incomplete uploads
at one day; its rules were inspected after the owner's sign-in and not changed.
The app should enforce the 15-day deadline, not a new bucket-wide rule. Existing
deadlines are not rewritten. Already removed objects are not recoverable through
this change.

## Receipt-based production checks and release gate

| Route | Observed result |
| --- | --- |
| Image 2.6 Sunburst | A protected production Images API probe returned HTTP 200. Usage shows account 249, group 16, exact unchanged model on `/v1/images/generations`, and one 1536×1024 PNG (CRC and full raster checked). Account 67's earlier 502 used a different relay. |
| Image 2.6 Flare | The same protected production route and account 249 returned HTTP 200 with the exact unchanged Flare ID and one validated 1312×1199 PNG. Do not retry the distinct exhausted account 254. |
| Image 2.5 Sunburst | The Studio receipt `imgbatch_b1198d71b6fd9e594f1d05bcd36c5dfa` used account 249, group 6, and settled once (0.2 internal units). Chromium decoded the thumbnail and original PNG, 1536×1024. |
| Muse | A real Studio job settled once (0.1 internal units). Chromium decoded the thumbnail and original WebP, 498×332. |
| Gemini / Vertex | `imgbatch_5b618cb4f6824555c1ab91fc98cace98` reached Vertex `JOB_STATE_SUCCEEDED`, then Sub2 failed `RESULT_DELIVERY_FAILED`. Nothing captured. The signed status lookup returned application `404 NOT_FOUND`. A fail-closed probe on the deployed Worker rejected the current GCS namespace with `403 SOURCE_PATH_NOT_ALLOWED` and accepted the old source path up to the deliberately invalid destination-key check. The job's output-cleanup events completed; no original was recovered or replayed. |
| Wan / Seedream | Existing native adapters and prices; no currently authorized bound test key for the exclusive groups. No admission changed. |

### Archive repair and release sequence

The companion `Turtle-Li/turtle-batch-image-pump` configuration uses only the
currently configured production GCS bucket and `batch-image/prod/` namespace.
Worker tests reject the old bucket, staging and sibling prefixes, and wrong
batch identities; the Wrangler dry run passes.

- Production Sub2 runtime configuration matched the new prefix; a read-only
  query found zero nonterminal jobs in the old bucket. In Cloudflare, only
  `SOURCE_PATH_PREFIX` was changed on `turtle-batch-image-pump`. Deployment
  `2e7447b7` serves 100%; `ba9dcc1d` is the previous Worker version. The
  destination, shared secret, IAM, and storage rules did not change.
- A signed probe with deliberately invalid destination and no signed object
  URLs rejected the old source with `SOURCE_PATH_NOT_ALLOWED` and accepted the
  new source as far as `DESTINATION_KEY_NOT_ALLOWED`. Neither probe started a
  transfer.
- The authenticated Studio submitted one new Gemini Vertex 1K job
  `imgbatch_2bb4baeaf463a332fedc5081497c4f9d`. It completed one image. The
  `cos_jsonl_archive_completed` receipt records one shard, 1,250,259 bytes,
  after Sub2 matched the COS HEAD to the Worker's receipt. The ledger has
  exactly one capture. This job settled on the old release (72-hour deadline);
  no historical task was replayed, reopened, or charged.

Release through the canonical exact-`main` AWS blue-green workflow with
`deploy_caddy=false`. Preflight converged on healthy `sub2api-green`, the
rollback revision `ac835ab6f`, no unfinished transaction, and no running
inactive color. After activation, submit one exact 2.6 Studio job, open its
thumbnail and original, and verify one capture and a 360-hour deadline from
settlement. Open and decode the Gemini COS archive from the new gallery.
Preserve other Caddy routes, group admission, and prior retention deadlines.

## References and verification scope

Preserve the pinned 88lin viewport/card behavior and VSO ownership/variant
contracts in [the source adoption notes](../../frontend/src/features/image-studio/UPSTREAM.md).
No new upstream source, dependency, schema, credential, or provider retry was
introduced. Local functional tests do not establish live provider admission.

The targeted config, service, repository and handler Go tests passed. The
Image Studio and archive frontend suite passed 102 tests; scoped ESLint,
production frontend build and `git diff --check` passed. Chromium with a
loopback API fixture verified the desktop/mobile picker, expired/failed items,
undecodable-cover fallback, and a decoded archive result and per-item archive
failure. The trace showed no automatic reads of expired media or unopened
archives, no credentials sent to the fixture COS origin and no generation
submission. These are candidate checks, not a production COS or 2.6 success.
