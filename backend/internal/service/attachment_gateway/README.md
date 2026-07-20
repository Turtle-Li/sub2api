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

Phase 1 does not process files, PDF, Office, audio, video, remote URLs or
`file_id`; it does not create public URLs or use R2/S3. HTTP Responses and the
first WebSocket ingress turn can use the experiment. Subsequent WebSocket turns
are deliberately unchanged pending a transform-hook design.

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
