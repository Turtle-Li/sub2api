# Attachment Gateway Phase 1

This package is an opt-in Responses attachment experiment. The application
default is `gateway.attachment_gateway.attachment_optimizer_enabled: false`.

When enabled it:

- visits only Responses `input_image.image_url` and legacy `image_url` fields;
- accepts inline PNG, JPEG and WebP base64 data URLs;
- leaves images below 512 KiB unchanged;
- encodes ordinary images as WebP q85, likely code/UI screenshots as q90,
  and images with transparency as lossless WebP;
- never resizes the raster;
- caches by SHA-256 of decoded source bytes under `data/attachment_cache`;
- deduplicates concurrent cache misses with in-process singleflight;
- bounds request-side base64/decode work and worker-side raster/WebP work with
  separate slots, so a non-cancellable encoder still holds capacity after its
  caller times out;
- applies per-image, per-request decoded-byte, pixel-count and image-count
  limits;
- expires cache pairs and evicts the oldest pairs above the configured byte
  budget without touching unknown files;
- fails open per image and emits only byte/count/duration metrics.

The request-level budget is a second, separately gated Phase 1 capability:

- `request_budget_enabled: false` keeps aggregate inspection completely off;
- `aggregate_small_image_enabled: false` can lower the per-image threshold from
  512 KiB to 128 KiB only when supported images exceed the configured aggregate
  byte/count trigger;
- candidate attachment count/bytes and candidate forward-body bytes are
  measured after optimization;
- `request_budget_enforce: false` logs `budget_would_reject` but never rejects;
- enforcement is possible only in `rewrite` mode and returns 413 before any
  upstream account/failover attempt;
- PDF, Office, audio and video remain unmodified, but recognized inline fields
  count toward the aggregate budget.

The optimizer work limits remain fail-open and are not upload quotas. In
particular, `max_images_per_request` still means "stop optimizing more images";
it never silently changes into a rejection rule.

Application integration adds three rollout barriers in addition to the leaf
feature switch:

- `attachment_optimizer_dry_run: true` measures and populates cache but sends
  the original payload;
- `allow_unscoped: false` requires an explicit API-key or group allowlist;
- `optimize_timeout_ms: 5000` returns the original payload when the request
  time budget expires.

For production canaries, `rollout_control_file` can point at a tiny file whose
entire content is exactly `off`, `dry_run`, or `rewrite`. The handler reads it
only for an explicitly in-scope key/group. Missing, oversized, or invalid
content fails closed to `off`, allowing an immediate mode change without
recycling a container or interrupting long-lived Responses streams. An empty
config value preserves the static `attachment_optimizer_dry_run` behavior.

The feature remains a synchronous experiment. Dry-run requests can still pay
the optimization CPU/latency cost, and an encoder already executing cannot be
preempted; concurrency bounds keep that work finite while the request fails
open at its deadline.

Phase 1 does not process files, PDF, Office, audio, video or `file_id`.
HTTP Responses and the first WebSocket ingress turn can use the experiment.
Subsequent WebSocket turns are deliberately unchanged pending a transform-hook
design.

An additional, separately disabled URL experiment can externalize the
post-compression inline image bytes:

- `url_rewrite_enabled: false` is the leaf switch;
- it runs only for an explicitly scoped request while rollout mode is
  `rewrite`; `dry_run` never writes to object storage;
- it reuses the existing S3-compatible `image_storage` credentials without
  enabling async image tasks when `image_storage.enabled` remains false;
- deterministic SHA-256 object keys plus R2/S3 `HeadObject` avoid re-uploading
  the same optimized bytes after a process restart;
- an in-memory URL cache reuses still-valid presigned URLs within the configured
  window, and singleflight prevents concurrent upload stampedes;
- storage errors, timeouts, non-HTTPS URLs and unsupported images fail open to
  the compressed data URL;
- URLs, object keys, hashes, credentials and image contents are never logged.

For private R2 buckets, leave `image_storage.public_base_url` empty. The
existing S3 adapter then emits a short-lived presigned GET URL. Configure the
bucket lifecycle separately so objects expire after the desired retention
period.

This package runs after the reverse proxy has accepted the request. It can
reduce Sub2-to-upstream bytes but cannot fix a Caddy 413 that rejects the
original client body before the handler runs.

Run focused verification:

```bash
cd backend
go test ./internal/config ./internal/handler
go test -race ./internal/service/attachment_gateway \
  -run '^(TestConcurrentRequestsSingleflightOneEncode|TestTimedOutBackgroundEncodeStillHoldsConcurrencySlot|TestCacheCleanup.*)$'
go test -tags nodynamic ./...
go test ./internal/service/attachment_gateway \
  -run '^$' -bench '^BenchmarkPhase1Scenarios$' -benchtime=1x -benchmem
```
