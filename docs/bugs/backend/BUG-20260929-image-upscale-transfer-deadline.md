# BUG-20260929-image-upscale-transfer-deadline

Status: RESOLVED; independently reviewed, deployed, and verified in production.
Baseline: `cfc18e67f91a40dc15ede0ba0135a39579b47245`. Fixed release:
`26e16557a23e78836c1435caf78550dc335edfc0`.

## Failure and evidence

The first post-release OpenAI-compatible asynchronous `n=2`, `2K` probe failed
after about 71 seconds with `SUBMIT_TRANSPORT_FAILED`. It returned no image and
was not billed. At the failure time the active application, upscale Vault
sidecar, Mini gateway, and ComfyUI were healthy. The application resolved the
Mini hostname to its Tailnet peer and a direct Tailscale ping used the peer path.
The Mini access log contained no matching POST and no new job or Comfy input, so
the request had not completed arrival and did not leave an orphan upscale job.

A bounded invalid-auth transfer probe created no job but isolated the link:
256 KiB reached the gateway and returned 401 in 7.6 seconds, while a 2.5 MiB
body did not complete within 60 seconds. Production source PNGs are commonly
about 2.0-2.4 MiB. The configured 30-second timeout was installed as
`http.Client.Timeout`, which covers connection, complete request upload,
response headers, and complete response-body read. It cancelled a valid
multipart upload before the gateway could parse it or issue a job ID. The
separate 900-second per-job deadline could not govern work that had not yet been
submitted.

## Repair and boundaries

Clone Go's default HTTP transport and apply the configured 30-second value as
`ResponseHeaderTimeout`. Leave `http.Client.Timeout` unset. Source upload and
result download are then bounded by the existing per-job context and the
earlier parent operation/request deadline, while a server that accepts the body
but fails to return headers is still bounded.

Do not retry submit transport errors or HTTP 5xx responses. The Mini submit API
has no idempotency key, so retrying an ambiguously accepted POST could create a
duplicate job and waste compute. Explicit 429 remains the only submit retry.
This repair does not change queue capacity, worker concurrency, all-or-nothing
response semantics, storage, usage recording, or billing.

## Verification

Unit regressions assert the configured/default response-header timeout, absence
of a client-wide timeout, a synthetic upload lasting longer than the configured
header timeout, cancellation by the job context, and timeout while waiting for
response headers. The complete Image Upscale service test suite passed, followed
by independent Level 1 QA and code review of the frozen source candidate.

GitHub Actions workflow `36598546824` deployed the exact fixed release through
the guarded production blue-green path on 2026-09-30. The active
`sub2api-blue` container reported the exact OCI revision, healthy state, zero
restarts, and no OOM. Public health, the networkless upscale Vault sidecar, the
Tailnet Mini endpoint, and ComfyUI were healthy.

The post-deployment OpenAI-compatible asynchronous task
`imgtask_cf1a14c37a9b4a3fa959bd4f67f01943` completed an `n=2`, `2K` request in
172 seconds of client-observed time. It returned two distinct `2048x3072` RGB
PNGs through task-scoped storage. Both images were independently inspected and
contained one person, one pose, and one continuous frame, with no collage or
multi-panel composition. The exact completion window contained one usage row
and one billing-dedup row: `image_count=2`, requested billing tier `2K`, base
cost `0.402 CNY`, probe multiplier `0.01`, and committed charge `0.00402 CNY`.
Independent Level 2 artifact and production QA passed without issuing another
generation request.

Knowledge candidate: yes; the distinction between response-header and total
client timeout is a durable runtime transport contract for large media uploads.
