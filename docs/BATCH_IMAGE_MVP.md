# Batch Image MVP

Sub2API Batch Image MVP provides asynchronous Gemini and OpenAI/Image batch generation through a unified API surface backed by Redis workers, PostgreSQL state, private object storage, and provider-specific execution backends.

Supported providers:

- `gemini_api`
- `vertex`
- `openai_images`

API users do not see Gemini file names, Vertex job names, internal OpenAI item markers, GCS paths, API keys, service-account
material, or permanent COS credentials. Private-COS delivery exposes short-lived exact-object
read capabilities only through the authenticated `result-files` endpoint. Those URLs are
temporary bearer capabilities and are never included in task list/status/item responses.

## API Routes

```text
POST   /v1/images/batches
GET    /v1/images/batches/{id}
GET    /v1/images/batches/{id}/items
GET    /v1/images/batches/{id}/items/{custom_id}/content
GET    /v1/images/batches/{id}/result-files
GET    /v1/images/batches/{id}/download
POST   /v1/images/batches/{id}/cancel
DELETE /v1/images/batches/{id}/outputs
```

Submit request:

```json
{
  "model": "gemini-2.5-flash-image",
  "provider": "gemini_api",
  "shared_reference_images": [
    {
      "shared_reference_id": "sha256:example-product-front",
      "mime_type": "image/png",
      "data": "<base64 image bytes sent once>"
    }
  ],
  "items": [
    {
      "custom_id": "cover_001",
      "prompt": "A clean product hero image...",
      "output_count": 1,
      "reference_images": [
        {
          "id": "product-front",
          "type": "product_truth",
          "shared_reference_id": "sha256:example-product-front"
        },
        {
          "id": "style",
          "type": "style",
          "mime_type": "image/jpeg",
          "file_uri": "gs://internal-managed-bucket/batch-image/refs/style.jpg"
        }
      ]
    }
  ],
  "image_size": "1K",
  "response_mime_type": "image/png"
}
```

`shared_reference_images` is an optional compact transport pool. An item references a
pooled payload with `shared_reference_id` while retaining its own ordered `id` and `type`.
Sub2API resolves the pool before validation, request hashing, idempotency checks, and
provider JSONL generation. It is intended for product-truth images reused by every output,
but item order and role labels remain independent. Every pool entry must be referenced.
The client therefore sends an inline shared payload to Sub2API only once. Every provider
still receives every reference required by each independent item: resolved inline bytes
are serialized into each applicable request, while a reusable internal `gs://` `file_uri`
repeats only the URI on Gemini/Vertex. OpenAI/Image items accept inline bytes only; `gs://`
references are rejected rather than forwarded as unusable or accidentally public URLs.
The field does not weaken per-item model limits: resolved shared and inline references are
counted together. The normalized request clears the transport pool and reference IDs, so
an expanded legacy request and its compact equivalent have the same semantic request hash.

`reference_images` is optional per item and should contain only references specific to that
output, such as its assigned scene. Inline `data` is a base64 string decoded by the backend;
`file_uri` is reserved for internal Google Cloud Storage references and must be a `gs://`
URI. Each reference image must use one of `image/png`, `image/jpeg`, or `image/webp`.
When `type` is a recognized closed role, every provider adapter places a
server-owned role guide immediately before that image part. This preserves per-image
authority after provider serialization (for example, `MODEL_REFERENCE` may define the
adult person but every worn or held product is an untrusted placeholder that must be
replaced from `PRODUCT_TRUTH`). An empty `type` retains the legacy untyped behavior;
an unknown non-empty `type` receives a fixed no-authority guide, and caller-controlled
role text is never echoed into the upstream prompt.
Current model limits are:

- `gemini-2.5-flash-image`: up to 3 reference images per item.
- `gemini-3.1-flash-image`, `gemini-3.1-flash-lite-image`, and `gemini-3-pro-image`: up to 14 reference images per item.
- Supported OpenAI Image batch models (`gpt-image-2` and the configured Image 2.5
  flare/sunburst revisions): up to 16 inline reference images per item.
- Per batch job: up to 1000 reference image attachments total after `output_count` expansion across all items. This is an internal Sub2API guardrail for request size and cost control, not the generated-image cap and not a Pro Image per-item capability. Generated-output caps are resolution-aware: 50 images at `1K`, 15 at `2K`, and 10 at `4K`.
- Per batch job: up to 128 MB decoded inline reference image data total. For large Gemini/Vertex batches, prefer server-managed `gs://` `file_uri` references or split the request into multiple jobs. OpenAI/Image batches must split when the inline limits are reached.

`output_count` is optional per item and defaults to `1`. It means "repeat this prompt and reference image set N times" rather than relying on one upstream request to return multiple images. The backend expands each repeat into a separate provider item with suffixed custom ids such as `cover_001_01`, `cover_001_02`. Gemini/Vertex serialize those items as JSONL; `openai_images` executes each expanded item as a separate `n=1` Images request. Current limits are:

The OpenAI-compatible `/v1/images/generations` `n` field likewise repeats one
prompt and one reference set; it cannot express ten different prompt/reference
assignments. Use ten batch `items` when every output needs its own prompt or
reference set. Each batch item retains its own prompt and ordered reference set,
and results are reconciled back to the expanded `custom_id` rather than by
provider row or completion order.

- Per prompt item: up to 4 output images.
- Per batch job after expansion: up to 50 expected outputs at `1K`, 15 at `2K`, and 10 at `4K`. These are hard generated-output caps; clients and Codex skills must split larger workloads before submission.
- Legacy provider-streamed jobs can use server ZIP subject to the item and byte caps. New
  COS-delivered jobs return raw JSONL result-file capabilities; browsers and Codex decode images
  and assemble ZIP files locally, so bulk media never traverses the Sub2API host.

Public batch response:

```json
{
  "id": "imgbatch_0123456789abcdef0123456789abcdef",
  "object": "image.batch",
  "status": "queued",
  "model": "gemini-2.5-flash-image",
  "image_size": "1K",
  "provider": "gemini_api",
  "item_count": 1,
  "success_count": 0,
  "fail_count": 0,
  "estimated_cost": 0.25,
  "actual_cost": null,
  "created_at": 1783123200,
  "submitted_at": 1783123201,
  "settled_at": null
}
```

Public items response:

```json
{
  "object": "list",
  "data": [
    {
      "custom_id": "cover_001",
      "status": "succeeded",
      "mime_type": "image/png",
      "file_extension": "png",
      "image_count": 1,
      "error": null
    }
  ],
  "has_more": false
}
```

For a COS-archived Vertex job, persisted items initially use `result_available`. Their exact
per-item success/error metadata lives inside the private JSONL and is overlaid by the browser or
Codex after local parsing. Aggregate job counts and settlement remain authoritative from Vertex
`completionStats`; the Sub2 host does not download JSONL merely to populate the item table.

## Lifecycle

Internal lifecycle:

```text
created -> uploading -> submitted -> running -> indexing -> settling -> completed
```

Terminal and cleanup statuses:

```text
failed
cancelled
completed -> output_deleted
```

Public status mapping:

```text
created/uploading -> submitting
submitted         -> queued
running           -> running
indexing          -> processing_results
settling          -> settling
completed         -> completed
failed            -> failed
cancelled         -> cancelled
output_deleted    -> output_deleted
```

`completed -> output_deleted` happens after manual output deletion or TTL cleanup.

Cancellation is provider-state aware. For Gemini/Vertex jobs, the public cancel path acquires
the same per-job lock as the worker, reloads the durable row, and checks the provider before
requesting cancellation. For `openai_images`, it first persists a durable cancel marker without
waiting for that lock. A currently running Images request may finish and be billed, but the worker
checks the marker before its upstream-attempt claim and cannot start another item afterward. If
the lock is busy, cancel returns the current job after the marker is durable; reconciliation remains
the worker's responsibility. A provider terminal result is never overwritten: `succeeded`, `failed`,
`expired`, and `cancelled` are repaired into the normal worker queue with `EnsureEnqueued`, and a
successful output reference is persisted before repair. If `Cancel` races with provider
completion, one follow-up status check applies the same reconciliation. The worker remains the
only component that indexes/delivers results, persists terminal state, and settles or releases the
balance hold; cancellation reconciliation never calls `Submit`. Except for the durable
`openai_images` marker case above, missing locks, unsupported queue repair, Redis errors, or
inconclusive provider evidence fail closed with the generic public cancel error and do not expose
provider identifiers or raw responses.

For Vertex jobs with private-COS delivery enabled, `indexing` includes a durable delivery gate:

```text
list small GCS object metadata
  -> exact GCS signed GET + exact COS signed PUT capabilities
  -> Cloudflare Workflow streams each raw JSONL shard unchanged
  -> Sub2 validates shard metadata with exact-object COS HEAD
  -> Sub2 reconciles aggregate counts with Vertex completionStats
  -> settling
```

The Worker does not parse JSONL or decode Base64. One Workflow step uses one GCS GET and one COS
PUT for a result shard; Workflow instances provide durable queueing and retries. Sub2 never sends
a Google OAuth token or COS permanent credential to the Worker. Image bytes and Base64 do not
traverse the Sub2 host.

## Redis

Redis is used for wakeups, retries, worker coordination, per-job locks, and download limiting. PostgreSQL remains the source of truth.

`batch_image.queue_enabled` defaults to `false`. When it is set to `true`, app startup starts `batch_image.worker_concurrency` independent `BatchImageWorker` consumers plus one delayed queue mover, one stale active recovery loop, and one provider-submitted queue reconciliation loop. The worker count defaults to `1` and is validated in the range `1..16`. Each consumer reserves jobs from the Redis ready queue and keeps the existing per-job Redis lock and heartbeat while Provider I/O is in flight. Lock refresh runs strictly before the lease deadline. If refresh reports a lost lease, the worker cancels the processor context and must not acknowledge or requeue the reservation; the new lock holder or durable recovery loop owns subsequent progress. Worker acknowledgement/requeue and lock-owned queue repair verify the current lock token in the same Redis Lua operation that changes queue membership, so a paused stale owner cannot mutate a replacement owner's state after its lease expires.

Size the worker count against both upstream account concurrency and the host budget. On the current 2 vCPU production shape, start at `2`; raising it beyond available Gemini/Vertex/OpenAI account concurrency only increases queue and database pressure without adding throughput. One `openai_images` worker advance starts at most one item, and account-slot admission still caps concurrent Images calls across jobs.

Submission limits apply after `output_count` expansion: `1K` accepts at most 50
output images, `2K` accepts at most 15, and `4K` accepts at most 10. The generic
`max_output_images_per_job` remains an absolute ceiling, so lowering it also
lowers the high-resolution tiers. Requests above a tier limit are rejected
before account selection, balance hold, provider upload, or provider job creation.

`2K` and `4K` jobs share a separate whole-job finalization gate. Production
keeps this concurrency at `1`, matching the Office Mini upscale worker. When the
gate is occupied, another completed high-resolution job is returned to the
delayed queue instead of waiting inside a batch worker. This preserves the
second release-mode worker for provider polling, settlement, and ordinary `1K`
indexing. The gate is process-local because production has one active background
owner; horizontal background workers require a distributed finalization lease.

The production Compose file also enables the process-local synchronous image gate at `16` requests with `wait` overflow and `100` waiting entries. This is a host backpressure limit for image response bodies, not a Provider quota: Gemini account slots and image2 account slots are still selected by the Redis/DB account scheduler.

Redis structures:

- Ready queue: `batch_image.queue_ready_key`
- Delayed queue: `batch_image.queue_delayed_key`
- Active set: `batch_image.queue_active_key`
- Inflight keys: `batch_image.inflight_key_prefix`
- Per-job lock keys: `batch_image.lock_key_prefix`
- Queue idempotency keys: `batch_image.idempotency_key_prefix`
- Download limiter keys managed by the download limiter

Workers reserve normal work from Redis. A separate active-generation reconciliation loop runs at `batch_image.recovery_interval_seconds`, with `batch_image.recover_limit` as its page size (maximum 1000; larger configuration is rejected). It scans only durable provider-submitted jobs in `submitted`, `running`, `indexing`, or `settling` status. Each pass fixes the current maximum eligible database ID and pages only through that snapshot; new submissions wait for the next pass, so a continuously growing full page cannot prevent an earlier transient Redis repair failure from being revisited. Before repairing Redis membership it acquires the same per-job lock used by workers and revalidates durable eligibility, so a stale scan cannot requeue a job after a worker has persisted a terminal state and acknowledged it. Recovery never submits a provider job. This is a bounded reliability exception, not general database-polling work dispatch. Standby generations do not scan or write queue state.

## Billing

MVP billing rules:

- Submit may estimate cost.
- Settlement runs after result indexing.
- Only successful images are charged.
- Failed items are not charged.
- Reference images are sent to the selected provider as input and can create upstream input-token, transfer, or temporary-storage cost. They are counted once per expanded output request when `output_count > 1`, but the public MVP billing model does not add a separate reference-image surcharge. User-facing estimated, held, and settled amounts are still based on the successful output image count and configured requested-tier batch price.
- Settlement request id is `batch_image_settlement:{batch_id}`.
- Settlement is idempotent; re-running settlement must not double charge.
- Settlement billing failures are retried with a bounded retry limit. After the retry limit is reached, the job is failed and the remaining hold is released through the idempotent release path.

Exact production pricing is resolved through model pricing configuration and is not defined here.

## Cleanup

Defaults:

- Input retention after terminal status: 24 hours.
- Output retention after terminal status: 72 hours.
- Maximum output retention: 7 days.
- Cleanup interval: 30 minutes.
- Cleanup batch size: 100.

Manual output deletion:

```text
DELETE /v1/images/batches/{id}/outputs
```

After output cleanup, downloads return `410 Gone` with `BATCH_IMAGE_OUTPUT_DELETED`.

Cleanup never accepts user-supplied provider paths. Provider cleanup must use server-generated refs and prefix-safe deletion.

For COS-delivered Vertex jobs, output cleanup removes every bounded deterministic COS raw-shard key before
deleting the managed GCS output. A cleanup failure keeps the output state retryable and does not
falsely mark it deleted.

For `openai_images`, every cleanup target is a bounded deterministic private-COS key. Cleanup therefore
does not require the upstream account credential and remains possible after an administrator soft-deletes
the account. If an account disappears while one of its jobs is still active, the processor moves that job
to `failed`, releases the remaining hold through the idempotent release path, and leaves its deterministic
objects eligible for the ordinary retention worker. Transient account-database errors remain retryable and
must not be treated as account deletion.

For the managed Vertex/GCS batch bucket, disable Cloud Storage soft delete or configure lifecycle carefully to avoid hidden retained storage cost.

## Provider Notes

`gemini_api`:

- Uses Gemini Batch API with JSONL file mode.
- Supports Gemini `apikey` upstream accounts with a configured API key.
- Result file refs are internal.
- API keys are never returned.
- The provider can be selected and submitted through Sub2API when an administrator configures a Gemini API-key upstream account. In the 2026-07-07 PR validation, this path was verified as selectable/callable, but successful image generation was not continued because the test API key had no prepayment.

`vertex`:

- Uses Vertex `BatchPredictionJob` with managed GCS JSONL.
- Supports Gemini `service_account` upstream accounts with valid service account JSON.
- GCS bucket and prefix are server-managed.
- Vertex job name and GCS paths are internal.
- `1K` keeps the provider result path unchanged. When the private upscale
  adapter is active, both Gemini API and Vertex accept `2K`/`4K` batch jobs,
  ask Gemini for the requested aspect ratio at source `1K`, and use the shared
  native `2x`/`4x` post-processor. Billing and holds retain the requested tier.
  See `docs/operations/IMAGE_25_UPSCALE_20260928.md` for the failure, storage,
  credential, and rollback contract.
- Upscaled batch images are private COS objects served through authenticated
  item and ZIP downloads. They do not use the Vertex raw-result capability
  endpoint. Each expanded `custom_id` must return exactly one image; a provider
  line with zero or multiple images fails before upscaling or COS persistence.
  Retries overwrite one deterministic object key, while manual/TTL cleanup
  sweeps the bounded JPG/PNG/WebP candidates for every durable custom ID.
  Failed and cancelled high-resolution jobs are cleanup-eligible after the
  stale-write fence; completed jobs retain the configured output-retention
  window.
- Optional delivery mode streams completed JSONL shards unchanged through a dedicated Cloudflare
  Workflow into private Tencent COS before settlement. Only source listing, Vertex status, and
  exact-object COS `HEAD`/signing control traffic touches Sub2.

`openai_images`:

- Supports `gpt-image-2`, Image 2.5 flare/sunburst, and their configured dated
  revisions on OpenAI `oauth`, `setup_token`, and compatible `apikey` accounts.
  Other GPT Image models fail request validation rather than being guessed compatible.
  Model routing is strict: GPT Image models cannot select a
  Gemini provider, and Gemini image models cannot select `openai_images`.
- Source-1K aspect ratios are limited to `1:1`, `2:3`, `3:2`, `3:4`, `4:3`,
  `4:5`, and `5:4`. `9:16`, `16:9`, and `21:9` are rejected because the upstream
  minimum-pixel requirement and Sub2API's longest-edge-at-most-1024 source contract
  cannot both preserve those exact ratios.
- API-key edits send references as repeated `image[]` multipart file parts. OAuth
  and setup-token transports use ordered inline data URLs. The public shared-reference
  pool still uploads common bytes to Sub2API once; each independent upstream item
  receives the references it actually uses in its original order.
- OpenAI does not receive one native multi-item batch request. Sub2API first
  stores a private durable manifest, then the queue worker executes exactly one
  `n=1`, source-`1K` Images request for each expanded `custom_id`. A ten-item
  public batch therefore means up to ten separately admitted upstream Images
  calls, while the client still sees one `imgbatch_*`, one hold, one aggregate
  usage row, and one idempotent settlement transaction.
- Deterministic per-item attempt and result objects prevent a worker retry from
  replaying an accepted non-idempotent Images call. If a process ends after an
  attempt starts but before its result is durable, that item fails as an
  unknown provider outcome instead of automatically charging the upstream a
  second time. Other items continue.
- The attempt claim is an atomic COS create guarded by
  `x-cos-forbid-overwrite: true`; only `409/FileAlreadyExists` means another
  worker already owns it. This guarantee requires a non-versioned COS bucket.
  Application startup and every claim read bucket versioning and fail closed
  for `Enabled`, `Suspended`, or an unreadable state. The COS identity therefore needs the
  minimum permission to read bucket versioning in addition to its exact-prefix
  object permissions. OpenAI batch enablement must remain off until a real
  create-twice readiness probe confirms that the second write is rejected,
  followed by exact-key cleanup.
- Attempt and result objects are both first-writer-wins atomic creates. A stale
  worker that finishes after lease handoff cannot overwrite the new owner's
  durable success, failure, or unknown-outcome decision.
- `Get` is status-only. Only the queue worker's explicit `Advance` operation may
  start another item, so status checks and cancellation cannot accidentally
  generate a new image. Account concurrency admission happens before the
  durable attempt marker and before the upstream request.
- Each upstream item has a 20-minute execution deadline. A timeout records only
  that item as failed and later items continue; it does not discard earlier
  durable successes. Items in one batch advance sequentially, so the strict
  ten-item source-generation upper bound is approximately 200 minutes plus
  bounded queue/storage overhead if every upstream request consumes its full
  deadline. This is a failure bound, not an expected duration or SLA.
- Requested `2K`/`4K` remains on the durable job and its pricing snapshot; each
  upstream item is source `1K`, then the common actual-dimension check and Mini
  post-processor produce the requested tier. Successful items are billed at
  that requested tier and failed items are not billed.

Other Gemini account/login types are not selected unless they expose equivalent
API-key or service-account credentials through the same provider flow. OpenAI
account types outside the list above are likewise not selected.

## Provider Enablement

OpenAI/Image groups require an eligible OpenAI account, an explicit model
mapping/whitelist, configured batch pricing for the requested resolution tier,
private batch object storage, and the group-level image and batch-image gates.
The object store holds manifests, attempt markers, per-item results, and the
combined provider result; without it `openai_images` submission fails closed.

### Official Google Enablement

Operators must enable Gemini/Vertex capability in Google's official console before turning on Sub2API batch image for any group. Sub2API feature flags and group switches do not create Google-side access by themselves.

Recommended production path:

- Use a Google Cloud project with billing enabled.
- Enable the relevant Gemini API / Vertex AI APIs for the project.
- Use a service account or Application Default Credentials for the Sub2API runtime.
- Create one fixed Cloud Storage bucket for batch image input and output, then grant the runtime and Vertex service agent the minimum required bucket permissions.
- Configure Sub2API with the project id, location, managed bucket, provider account, model whitelist, and pricing.
- Enable `BATCH_IMAGE_ENABLED` globally, enable image generation on the intended Gemini or OpenAI group, then enable `allow_batch_image_generation` for that group. The submitted model family must match the group platform.

API-key path:

- Google API keys are suitable for Gemini API development and supported Gemini methods.
- The Sub2API `x-goog-api-key` compatibility header still expects a Sub2API key, not a plain Google key.
- Plain Google API keys should not be documented as the default production credential for Vertex service-account batch jobs.
- If an administrator configures a Gemini API-key upstream account, validate it with one low-cost batch image after the Google account has the required billing/prepayment state. If it has no prepayment, record only that the provider is selectable/callable and that failed submit releases hold.

Official references:

- Gemini API key guide: https://ai.google.dev/gemini-api/docs/api-key
- Gemini API Batch API: https://ai.google.dev/gemini-api/docs/batch-api
- Gemini API image generation and batch image notes: https://ai.google.dev/gemini-api/docs/image-generation
- Vertex/Gemini batch inference: https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/capabilities/batch-inference
- Vertex batch predictions API: https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/batch-prediction-api

## Config

These keys exist in `backend/internal/config/config.go`:

```yaml
batch_image:
  enabled: false
  max_items_per_job_default: 50
  max_items_per_job_trial: 50
  max_output_images_per_job: 50
  max_output_images_per_job_1k: 50
  max_output_images_per_job_2k: 15
  max_output_images_per_job_4k: 10
  max_output_images_per_item: 4
  max_prompt_chars_per_item: 24000
  max_reference_images_per_job: 1000
  max_reference_inline_bytes_per_job: 134217728
  default_response_mime_type: "image/png"
  default_image_size: "1K"

  max_download_items_zip: 200
  max_download_bytes_per_request: 536870912
  max_download_duration_seconds: 600
  max_download_concurrency_per_user: 1

  input_retention_after_terminal_hours: 24
  output_retention_after_terminal_hours: 72
  output_retention_max_days: 7
  cleanup_interval_minutes: 30
  cleanup_batch_size: 100

  queue_enabled: false
  queue_ready_key: "batch_image:queue:ready"
  queue_delayed_key: "batch_image:queue:delayed"
  queue_active_key: "batch_image:queue:active"
  inflight_key_prefix: "batch_image:queue:inflight:"
  lock_key_prefix: "batch_image:queue:lock:"
  idempotency_key_prefix: "batch_image:queue:idem:"
  inflight_ttl_seconds: 604800
  job_lock_ttl_seconds: 300
  default_requeue_delay_seconds: 30
  error_retry_delay_seconds: 60
  lock_conflict_delay_seconds: 5
  stale_active_after_seconds: 600
  provider_submit_timeout_seconds: 600
  delayed_mover_interval_seconds: 5
  recovery_interval_seconds: 300
  delayed_move_limit: 100
  recover_limit: 100
  worker_concurrency: 1
  high_resolution_finalize_concurrency: 1
  high_resolution_finalize_requeue_seconds: 15

  vertex_enabled: false
  vertex_project_id: ""
  vertex_location: "global"
  vertex_managed_gcs_bucket: ""
  vertex_managed_gcs_prefix: "batch-image/{env}/{batch_id}"
  vertex_input_retention_hours: 24
  vertex_output_retention_hours: 72
  vertex_batch_prediction_base_url: ""
  vertex_gcs_base_url: ""

  delivery_enabled: false
  delivery_worker_url: "https://turtle-batch-image-pump.example.workers.dev"
  delivery_shared_secret: "<at least 32 random characters>"
  delivery_source_url_ttl_seconds: 3600
  delivery_upload_url_ttl_seconds: 3600
  delivery_download_ttl_seconds: 900
  delivery_poll_seconds: 10
  delivery_cos_endpoint: "https://cos.ap-shanghai.myqcloud.com"
  delivery_cos_region: "ap-shanghai"
  delivery_cos_bucket: "image-1309919944"
  delivery_cos_access_key_vault_ref: "vault://<approved-cos-item>#access_key_id"
  delivery_cos_secret_access_key_vault_ref: "vault://<approved-cos-item>#secret_access_key"
  delivery_cos_vault_agent_socket: "/run/sub2api-upscale-vault/public.sock"
  delivery_cos_prefix: "sub2-batch-image/prod/"
  delivery_cos_force_path_style: false
```

Feature flags default to disabled. Raw `delivery_cos_access_key_id` and
`delivery_cos_secret_access_key` values are rejected at startup. Both exact
references must point to the same Vault KV item; the colocated networkless
agent returns only those two allowed fields over its read-only Unix socket.

Once a batch row is durable, provider submission uses the server-owned
`provider_submit_timeout_seconds` deadline rather than the HTTP client's
connection lifetime. Clients that time out must retry with the same
idempotency key; they receive `BATCH_IMAGE_SUBMIT_PENDING` until the provider
job reference is durable, without starting a second upstream submission.

The CAM identity should be restricted to this bucket and prefix. Keep the bucket private. The
Worker stores only `delivery_shared_secret`; it receives expiring exact-object URLs rather than
the Google service-account key or COS CAM key.

Production uses this object-only CAM scope; it deliberately omits `ListBucket`, bucket ACL,
bucket policy, and public-read permissions:

```json
{
  "version": "2.0",
  "statement": [
    {
      "effect": "allow",
      "action": [
        "name/cos:PutObject",
        "name/cos:GetObject",
        "name/cos:DeleteObject"
      ],
      "resource": [
        "qcs::cos:ap-shanghai:uid/1309919944:image-1309919944/sub2-batch-image/prod/*"
      ]
    }
  ]
}
```

## Operations Checklist

- Enable `batch_image.enabled`.
- Configure Redis.
- Enable `batch_image.queue_enabled` when workers should consume queue jobs.
- Configure provider accounts.
- Configure the Vertex managed GCS bucket if using Vertex.
- Ensure bucket permissions are correct.
- Disable or manage GCS soft delete.
- If delivery is enabled, deploy the dedicated Worker/Workflow, configure the same HMAC secret
  at both ends, keep COS private, and grant the CAM key only the required object operations under
  `delivery_cos_prefix`.
- Configure COS CORS for the exact production frontend origins, `GET`/`HEAD`, and only required
  response headers. Do not use browser credentials; the signed query is the temporary authority.
- Configure cleanup worker settings.
- Configure max items per job.
- Configure download concurrency.
- Confirm billing pricing.
- Run smoke tests before enabling.

## Security Checklist

- No provider refs in public responses.
- No GCS URI exposure.
- No signed URL in list/status/item JSON, logs, events, recovery files, referrers, or error text.
  The authenticated `result-files` response necessarily contains short-lived COS signatures and
  must use `private, no-store` plus `Referrer-Policy: no-referrer`.
- No service account exposure.
- No API key exposure.
- No permanent COS credential exposure.
- No image bytes/base64 in PostgreSQL.
- No base64 in logs.
- Owner-scoped status, item, download, cancel, and delete routes.
- Cross-host clients must not forward the Sub2 `Authorization` header to COS.
- Clients must fall back to server ZIP only for the explicit
  `BATCH_IMAGE_RESULT_ARCHIVE_UNAVAILABLE` legacy response, never after COS network/CORS/signature
  or archive-integrity errors.
- The server must identify a COS archive marker before requiring delivery
  configuration. A completed legacy job without that marker returns
  `BATCH_IMAGE_RESULT_ARCHIVE_UNAVAILABLE` even when delivery is disabled, so
  clients may use the authenticated server ZIP path. A job with a valid archive
  marker remains fail closed with `BATCH_IMAGE_DELIVERY_NOT_CONFIGURED` when the
  private delivery store or configuration is unavailable.
- Output deletion is owner-scoped.
- Cleanup paths are server-generated only.

## Test Commands

Core smoke and compile commands:

```bash
go test -tags=unit ./internal/service -run 'BatchImage' -count=1
go test -tags=unit ./internal/config ./internal/service ./internal/repository -count=1
go test ./internal/config ./internal/service ./internal/repository ./internal/handler ./internal/server/routes -run '^$'
go test ./... -run '^$'
```

These commands should not require Docker, testcontainers, Redis, GCP, Gemini, Vertex, or GCS.

## PR Hygiene Checklist

- Do not accidentally commit `rfcs/batch-image-issue-draft.md` unless maintainers explicitly want it.
- Keep migrations ordered: `159_batch_image_foundation.sql`, then `160_batch_image_provider_refs.sql`, then later migrations.
- Include generated Ent code if generated code is committed in this repository.
- Keep generated server and wire files updated.
- Keep feature flags disabled by default unless maintainers ask otherwise.
- Do not commit real secrets, API keys, service account JSON, or local machine paths.
- Keep fixtures tiny and fake; no real cloud refs or credentials.
- Do not add new public routes, providers, dashboards, queues, or billing behavior in this stabilization PR.
