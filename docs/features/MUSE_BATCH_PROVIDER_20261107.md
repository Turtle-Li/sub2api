# Muse image batch provider

The `muse_images` transport reuses the existing sequential batch manifest, durable
attempt claim, queue, private result store, cancellation fence, and reserve/capture/
release settlement. Accounts remain OpenAI API-key accounts. Discovery requires an
explicit `muse-image` → `muse-image` mapping and a positive configured image price.
There are no default prices, account migrations, new schedulers, or production
configuration changes. Configure the existing account base URL to the Muse service
root or its `/v1` URL and supply its existing API key through the usual account form.

Only exact `muse-image` is accepted. Each item produces one image, accepts at most
one inline reference, and uses native output dimensions (the existing `1K` request
label). Explicit aspect ratios, higher resolution, output counts above one, remote
reference URIs, and non-PNG requested formats are rejected before reservation.
The returned PNG/JPEG/WebP content is validated and its actual MIME is recorded. There
is no provider URL fetching. Decoded images are limited to 20 MiB and 64 megapixels.

POST `/v1/images/tasks` includes a stable SHA-256 idempotency key and requests
`b64_json`. After HTTP 202 the upstream task ID is written next to the durable item
attempt. Subsequent workers only GET that ID. Queued/processing tasks and transient
poll failures remain pending; a lost submission response or missing receipt is a
terminal item failure, never a blind resubmission. A cancellation stops local
progress; the upstream has no task cancellation API and may finish its work.
The original batch expiry/retention policies remain authoritative. Output and
receipt cleanup reuse the existing lifecycle. HTTP redirects are disabled so
account credentials cannot be forwarded to an output URL or another origin.

Runtime construction injects the existing OpenAI gateway account controls. Every
POST and GET acquires the shared account concurrency slot before outbound work,
keeps it through response-body consumption, and releases it before the next queue
poll. Remote queued/processing time does not occupy a local HTTP slot. Busy POSTs
remain pending without creating an attempt marker; busy polls keep the existing
receipt. Missing gateway/concurrency/HTTP runtime disables submission availability.
Account proxy resolution runs before the attempt marker and the shared transport
revalidates it at send time. Broken, inactive or missing bound proxies fail closed;
there is no direct-client fallback. POST uses the original model-send path (and
its paid-fallback checks), GET uses the account-aware transport, and both carry
the existing no-redirect context option. This defines concurrency as active
outbound HTTP calls rather than remote Muse generation jobs.

## Pinned reference and intentional differences

Upstream: owner project `muse2api`, local source `/Users/lijiayu/auto_work/muse2api`,
revision `5542a479ec6ba036a9717e8776380ff5aacf3fe7`. Reference contract:
`app.py` (`ImageRequest`, `_queue_image`, `create_image_task`, `get_image_task`,
`_image_response`); provider source was verified identical at revision
`b84ecba0e3e09bd77175ea49151a590c2b8d2c63`. Upstream license is MIT; this integration
calls the service and does not copy its browser or generation implementation.
Any redistribution of upstream implementation must retain its MIT notice.

Adopted behavior: native persisted asynchronous tasks, stable idempotency key,
first-reference-only limit, and b64 output. Intentional restrictions: exact model
mapping, no silently ignored request controls, no arbitrary URL retrieval, bounded
content validation, and fail-closed ambiguous submissions. Contract tests live in
`backend/internal/service/batch_image_provider_muse_test.go`; original sequential
provider and settlement regression tests remain applicable.
