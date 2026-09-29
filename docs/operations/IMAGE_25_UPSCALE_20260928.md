# Image 2K/4K Resolution Adapter Contract

Date: 2026-09-28

## Scope

This contract applies to every supported image model when the requested output
tier is literal `2K` or `4K`. Eligibility never depends on a model-name
allowlist. The adapter decodes each returned image and invokes Office Mini only
when its actual dimensions do not satisfy the requested tier. `1K` and explicit
pixel-dimension values keep their existing provider path.

- `2K`: skip Mini when the longest edge is at least 2048; otherwise use `2x`.
- `4K`: skip Mini when the longest edge is at least 3840; use `2x` for a
  returned image whose longest edge is at least 1920, otherwise use `4x`.
- The post-processing path is selected only by literal `2K` and `4K` tier
  values. Pixel-dimension values such as `2048x1152` keep the existing provider
  path unchanged; they are not rewritten into unsupported Gemini pixel sizes.
- Provider request rewriting is a separate compatibility concern. Known Gemini
  2.5 providers receive `imageSize=1K`; unknown or future models retain the
  requested native tier and still pass through the same actual-dimension check.
- Codex OAuth non-streaming requests use the native Images transport only for
  `n=1`. Non-streaming multi-image requests use the Responses transport, which
  requests parallel image tool calls, fills any shortfall before returning,
  and preserves all-or-nothing response semantics. Streaming keeps its existing
  transport contract. This rule does not affect Mini eligibility.
- Batch JSONL preserves an explicit supported aspect ratio and either the native
  requested tier or the provider-compatible `1K` source tier.
- Billing and balance holds retain the originally requested `2K`/`4K` tier.
- The synchronous Images API and asynchronous image-task wrapper use the same
  adapter. Batch indexing uses that same process singleton and limiter.
- OpenAI-platform asynchronous 2K/4K results are stored once under their
  `imgtask_*` ID inside the gateway before usage recording. Task finalization
  only commits the already compact URL response; it never downloads and
  uploads those results a second time. A required storage failure returns no
  successful image result and does not enter usage billing.

The fixed service contract is the Office Mini gateway source deployed on
2026-09-28 from `deploy/office-mini-upscale-api/upscale_api/app.py` in the
retained `infra-upscale-task` work artifact. The active service accepts
`POST /v1/upscale`, `GET /v1/jobs/{id}`, and
`GET /v1/jobs/{id}/result`; all three calls require the same Bearer token.
Supported scales are 2 and 4 and the selected preset is `faithful`.

## Failure and concurrency behavior

One process-level limiter is shared by synchronous and batch calls. Production
uses one active request and at most eight local waiters, matching the remote
gateway's single worker and bounded queue. A full local queue returns a typed
429/backpressure error. Each Office Mini submit/poll/result job has its own 900
second deadline, including its queue wait and source loading. A multi-image
operation receives the sum of its per-image job budgets with a 25-minute cap
at the production 900-second setting, so one image cannot consume the complete
budget of a later image. The operation window never becomes shorter than an
explicitly configured per-job timeout. The
parent request or worker context can still impose an earlier deadline; the
OpenAI-compatible asynchronous task wrapper currently has a 30-minute total
execution deadline. Provider generation, Mini processing, final storage, and
task commit all share that parent lifetime, so the earlier parent deadline wins
and the difference between 25 and 30 minutes is not a guaranteed storage SLA.
For Responses shortfall filling, the shared window starts with the first Mini
job and is also inherited by later provider fill attempts, so those attempts
cannot reset or outlive the remaining high-resolution operation budget.
The scheduler releases and reacquires the Mini slot between images, so a
multi-image operation does not reserve the remote worker for its entire lifetime.

Batch admission is resolution-aware after `output_count` expansion: `1K` is
limited to 50 output images, `2K` to 15, and `4K` to 10. A nonblocking
high-resolution job admission gate is capped below queue-worker concurrency
whenever more than one worker exists, preserving at least one worker for 1K
jobs, provider polling, and settlement. Excess completed high-resolution jobs
are requeued without opening provider output. Once admitted, images enter the
shared per-image scheduler: interactive single-image work may receive at most
two consecutive grants while batch work is waiting; the next Mini slot then
goes to the oldest batch item. An `n>1` synchronous Images response is
classified as batch work, so one large request cannot continuously monopolize
Mini and admitted batch work cannot starve.

Synchronous `n>1` requests remain atomic and return only after every output has
finished post-processing. Separate synchronous requests return independently as
their own images finish. The asynchronous batch API returns a job ID after
submission, but the job becomes terminal only after all provider output has been
indexed and post-processed. Item results are still committed after the complete
provider output scan; progressive per-item visibility is not part of this phase.

Submit retries are limited to explicit HTTP 429 responses because the remote
API has no idempotency key and a transport/5xx retry could duplicate an accepted
job. Poll and result GET requests may retry 429/5xx within the configured bound.
`IMAGE_UPSCALE_REQUEST_TIMEOUT_SECONDS` limits only the wait for response
headers after a request body has been written. It is not a total HTTP-client
deadline: a multi-megabyte source upload and result-body download remain bounded
by the per-job context and its parent operation/request deadline. The client
therefore uses the standard transport with `ResponseHeaderTimeout` and no
client-wide `Timeout`. This distinction is required because a valid source can
take longer than 30 seconds to traverse the production Tailnet link before the
Mini gateway can parse and acknowledge the multipart submission.
401/403, invalid MIME, corrupt bytes, and dimension mismatches are permanent.
The shared limiter admits work before inline base64 decoding or URL download.
Each provider `1K` source is limited to 16 MiB, 2048 pixels on either axis, and
2,097,152 total pixels before submission. The adapter decodes the result and
requires its dimensions to equal the source dimensions multiplied by the
requested scale.

A synchronous request is atomic: if any returned image cannot be upscaled, the
request fails and Sub2 does not charge it. Batch processing is item-isolated:
one failed item is stored as `IMAGE_UPSCALE_*`, successful items remain
downloadable, and settlement charges successful images only.
The OpenAI-compatible asynchronous `n>1` wrapper preserves the same atomic
Images result contract: if its parent deadline expires before all images and
required storage complete, the task fails without customer billing and does not
publish a partial result. Workloads that require completed items to survive a
later item timeout must use the batch API's separate `custom_id` items.
The synchronous client response contains at most the requested `n` images, and
every expanded batch `custom_id` result must contain exactly one image. For the
Responses shortfall loop, each attempt may return fewer than the remaining
count and trigger another attempt. If a non-streaming upstream attempt returns
more than remain, Sub2 retains only the required prefix before Mini, object
storage, response assembly, and billing. Structured logs record each event;
successful usage rows retain discarded-image and event counts in
`image_size_breakdown` for durable frequency analysis.

## Synchronous delivery and object storage fallback

OpenAI Images-compatible, Gemini native, and Antigravity non-streaming 2K/4K
responses first retain every final upscaled image in memory. When the dynamic
`ImageStorage` setting is enabled, Sub2 then attempts to offload the completed
images through the existing S3-compatible abstraction; the configured backend
may be R2, COS, OSS, S3, or MinIO. Only when every image upload succeeds is the
client response atomically rewritten to object-storage URLs (`data[].url` for
OpenAI-compatible responses and `fileData`/`file_data` for Gemini responses).

Object storage is a bandwidth and memory-pressure optimization, not a success
dependency. If storage is disabled, unreachable, times out, or any image upload
fails, the request must still succeed with all final upscaled images returned
inline (`b64_json` or a data URL for OpenAI-compatible responses, and
`inlineData`/`inline_data` for Gemini). A multi-image response must never mix
stored URLs and inline images. The fallback warning records only the protocol
path, image count, and error type; it must not log credentials, object URLs, or
image data.

If an earlier image upload succeeds and a later upload fails, the client still
receives the complete inline response. The already uploaded object can remain
orphaned because synchronous object keys are unique and there is no response
metadata commit from which to drive cleanup. Operators should monitor this
bounded leak risk; add explicit synchronous-object TTL or cleanup indexing if
fallback frequency becomes material. Office Mini does not receive permanent
object-storage credentials in this phase: bytes still return to Sub2, which
performs the optional upload. Direct Mini-to-storage upload requires a future
short-lived presigned PUT design.

## Batch persistence and objects

Migration `261_batch_image_image_size.sql` persists the requested output tier;
legacy rows default to `1K`. Known Gemini 2.5 providers receive `1K` for 2K/4K
jobs; future providers retain their native requested tier. During result
indexing, each successfully upscaled image is written to the
existing private batch COS bucket under:

`<delivery_cos_prefix>/upscaled/<batch_id>/<sha256(custom_id)>/00.<ext>`

Only a fixed marker is stored in `provider_source_object`; custom IDs never
enter object paths directly. Item and ZIP downloads stream exact private
objects through the existing owner authorization and download limiter. Output
cleanup derives the three fixed `00.jpg`, `00.png`, and `00.webp` candidates
for every durable custom ID and deletes them without ListBucket access. Index
retries overwrite deterministic keys and never delete them out of band, which
prevents a stale worker from deleting a winning worker's object after a lease
expires. Objects written before an item metadata commit therefore remain
discoverable and are removed by manual or TTL output cleanup. Cleanup uses a
bounded retry and does not mark output deleted until the COS sweep succeeds.
Failed or cancelled high-resolution jobs receive a bounded output
cleanup deadline after a 150-second fence and are eligible for the same worker
sweep; this covers a COS write followed by an indexing metadata-commit failure.
Every COS write also holds a PostgreSQL `FOR UPDATE` permit on the job row from
the final `indexing` status check until `Put` returns. Cancel/failure/completion
transitions serialize on that same row, so a Redis-lease-expired worker cannot
begin or resume a write after terminal cleanup. The 150-second delay remains
defense-in-depth beyond the 120-second write timeout. Manual deletion observes
the same delay. Completed jobs retain the configured output-retention window.
Each COS write is capped at 120 seconds. Authenticated item and ZIP reads use
a 10-minute per-object context that is cancelled when the stream closes, so a
stalled object-store connection cannot pin a worker or download indefinitely.

## Credential and deployment boundary

The raw bearer token belongs only to Vault item
`13f74ced-5297-4789-a595-df01e346ba29`, field `api_key`. Repository and runtime
configuration store only the exact non-secret label:

`vault://secret/data/infrastructure/office-mini-upscale-api-public-key-20260928#api_key`

`sub2api-upscale-vault` is a networkless, read-only, memory-only agent with an
exact allowlist. The application sees only
`/run/sub2api-upscale-vault/public.sock`. A restart clears the value and becomes
unhealthy until an operator reloads the field through the hash-pinned
`infra-vault run-item-env-file` flow. Never add an `IMAGE_UPSCALE_API_KEY`
environment variable, Docker secret value, config key, log field, or command
argument.

Production is pinned to `https://hcmac-mini.tailfc4ed7.ts.net`. The blue-green
release and runtime guard accept only the dedicated
`sub2api_image_upscale_vault` read-only mount and reject a raw bearer
environment. Prepare and verify the sidecar with
`deploy/sub2api-image-upscale-vault-container.sh`; inject the value separately
through a hash-pinned `infra-vault run-item-env-file` request whose sole command
is `deploy/sub2api-image-upscale-vault-inject.sh`. The request maps only Vault
field `api_key` to `IMAGE_UPSCALE_API_KEY`; the consumer removes that variable
from SSH's environment and sends the value through stdin to the fixed remote
agent load command. It then runs the sidecar's `ready` check. Do not reuse the
request for another command or image revision.

The canonical generic Compose topology declares the stable local
`sub2api_image_upscale_vault` volume so a disabled fresh install still starts.
It is empty and contains no credential. The sidecar helper must initialize it
before activation. Production blue-green releases do not rely on Compose to
create it: their preflight requires the helper-prepared volume and a healthy
sidecar before mounting it read-only into the application.

Install the fixed release variables initially disabled by piping the exact
12-line block from `deploy/.env.example` to the root-owned
`/opt/sub2api/scripts/sub2api-image-upscale-config.sh`. After the sidecar is
ready, pipe the same block with only `IMAGE_UPSCALE_ENABLED=true` changed. The
config helper accepts only the documented values, requires a root-owned mode
`0600` release environment, runs under the shared maintenance lock, and allows
only an exact false/true transition after initial installation. It does not
print or replace surrounding configuration. The sidecar helper never accepts a
raw secret and never replaces an existing sidecar outside the maintenance
lifecycle.

## Resolution eligibility and production routing

Upscale eligibility is model-independent. A literal `2K` or `4K` request is
accepted for every image model. After generation, Sub2 decodes every returned
image and compares its real pixel dimensions with the requested tier. Images
that already satisfy the tier pass through unchanged; undersized images enter
the shared Mini scheduler. Adding a future model such as `image-3` must not
require an upscale allowlist change.

The production hostname `hcmac-mini.tailfc4ed7.ts.net` depends on Tailnet split
DNS. The production host must keep Tailscale DNS acceptance enabled and both the
host and application container must resolve that hostname to the Tailnet peer,
not a public DNS answer. This setting does not enable an exit node or a system
proxy. After host network or Tailscale changes, verify resolution and the Mini
`/health` endpoint from inside the active application container before treating
2K/4K image generation as available.

On 2026-09-29, production initially timed out during Mini submission because
the host had Tailscale DNS acceptance disabled and the hostname resolved through
public DNS. Enabling split DNS restored direct Tailnet routing. Exact commit
`c1202ddcadbea710211c7420581ec72191a649a8` then passed a real concurrent probe
with `gpt-image-2.5-sunburst`: one synchronous `n=2`, `2K` request returned two
stored-URL PNGs in 66.976 seconds, while a synchronous `n=1`, `2K` request
started ten seconds later and returned independently in 37.624 seconds. All
three files decoded as `2048x3072`. The multi-image response carried root-level
`size: "2048x3072"`; no response contained inline base64 after successful
storage offload. These timings are an observed probe, not an SLA.

Exact commit `67ac5eec3e83e60a278a2580e3bce268e5b2aa2b` then passed the
production asynchronous stress probe on 2026-09-29. One and only one
`n=10`, `2K` submission returned task
`imgtask_595d95e0c7b14a9ba61d9c4e8342fc97` in 0.324 seconds and completed
after 707 seconds. It produced ten unique `2048x3072` PNGs. Every result was
a task-scoped object URL, with no `imgsync_*` URL and no inline base64. The
application stayed healthy with zero restarts and no OOM; observed peaks were
143.28% multi-core CPU and 37.63% memory. The task window contained exactly one
related usage row and one billing-dedup row. The usage row recorded
`billing_mode=image`, `image_count=10`, `image_size=2K`, base cost `2.01 CNY`,
probe multiplier `0.01`, and committed charge `0.0201 CNY`. Its diagnostic
dimension breakdown classified the 3072-pixel long edge as 4K, but
`BillableImageSize` and the displayed billing tier remained the requested 2K.
These timings and resource peaks are observed evidence, not an SLA. The probe's
prompt also asked for “ten clearly varied poses” while sending `n=10`; the
model therefore rendered a ten-panel collage inside each of the ten distinct
PNG files despite the bridge's no-collage instruction. That run remains valid
for transport, scheduling, upscale, storage, and billing evidence, but its
visual-semantics check is failed and must not be cited as proof that `n=10`
produces ten single-frame compositions. Future probes must ask for exactly one
subject and one continuous frame per returned image and explicitly prohibit a
grid, collage, contact sheet, split panel, storyboard, inset, or multiple poses.

On 2026-09-29, the first post-release `n=2`, `2K` probe for exact commit
`cfc18e67f91a40dc15ede0ba0135a39579b47245` failed without billing as
`SUBMIT_TRANSPORT_FAILED`. The active application and Mini gateway were healthy,
DNS resolved to the Tailnet peer, and the Mini logs showed no corresponding
POST or queued job. A no-job diagnostic transfer then measured about 34 KiB/s:
256 KiB reached the gateway in 7.6 seconds, while a 2.5 MiB body exceeded 60
seconds. The service's former 30-second `http.Client.Timeout` therefore expired
during upload, before the Mini gateway could return a job ID; the 900-second job
lifecycle never started. The transport repair and regression scope are recorded
in `docs/bugs/backend/BUG-20260929-image-upscale-transfer-deadline.md`.

## Validation and rollback

Release gates require unit/race tests, migration tests, shell syntax and runtime
guard tests, a real 2K and 4K smoke, and owner-visible dimension evidence.
Production activation follows the exact-commit GitHub blue-green workflow.

Rollback is configuration-compatible: pipe the exact fixed block with
`IMAGE_UPSCALE_ENABLED=false` to the installed config helper, then run the
normal guarded release. Retain the sidecar and its volume; a rollback does not
need to expose or reload the bearer. Existing 1K jobs remain readable. Completed
2K/4K object results remain readable and cleanable by this release; do not roll
back to a binary that predates migration 261 while such jobs still need item or
ZIP download.
