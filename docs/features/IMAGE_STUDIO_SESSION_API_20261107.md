# Image Studio session API

The Studio uses the existing panel JWT in `Authorization: Bearer <session token>`.
The host adapter automatically selects an eligible owned API key by ID for the
chosen provider/model and sends `X-API-Key-ID: <positive integer>`. The user-facing
workspace has no group/key picker; the wire-level ownership boundary is unchanged.
No API key credential is returned by this directory, copied into an authorization header,
or sent through an internal HTTP request. JWT middleware remains responsible for expiry,
revocation, active user and session binding; selected-key admission subsequently reuses
the same user/key/group/IP/subscription/balance checks as the public gateway.

## Directory

`GET /api/v1/image-studio/keys?page=1&page_size=20` requires JWT only.
It returns the existing panel response envelope with `data.items`, `data.total`,
`data.page`, `data.page_size`, and `data.pages` (see shared pagination response).
Each item contains only `id`, `name`, `status`, nullable `group_id`, `group_name`,
nullable RFC3339 `expires_at`, `expired`, and `quota_exhausted`.
Only the logged-in user's keys are queried. Directory metadata is informational;
permission and available balance are rechecked on each operation. Do not interpret
an active key as proof of batch capability; load its model directory.

## Batch operations

All paths below are relative to `/api/v1/image-studio` and require JWT plus the selected key ID.
Request and response bodies are exactly those of the existing `/v1/images/batches` API:

| Method | Path | Existing handler |
| --- | --- | --- |
| GET | `/batches/models` | Models (capability and model allowlist filtered) |
| POST | `/batches` | Submit (validation, idempotency, security audit, service/billing) |
| GET | `/batches` | List |
| GET | `/batches/:id` | Get |
| GET | `/batches/:id/items` | Items |
| GET | `/batches/:id/items/:custom_id/content` | ItemContent |
| GET | `/batches/:id/result-files` | ResultFiles |
| GET | `/batches/:id/download` | Download |
| POST | `/batches/:id/cancel` | Cancel |

Use `Idempotency-Key` for submit retries. The body includes `model`, `items`
(each with `custom_id`, `prompt`, optional `output_count` and `reference_images`),
and the existing optional `task_name`, `parent_batch_id`, `provider`,
`shared_reference_images`, `response_mime_type`, `aspect_ratio`, `image_size`, `metadata`.
See `service.BatchImageSubmitRequest` for the authoritative schema.
List filters remain `status`, `task_name`, `downloaded`, `from`, `to`, `limit`, `cursor`.
Batch responses use the existing raw batch JSON, rather than the panel envelope.
Image and archive retrieval must use authenticated fetch (including the selected ID),
then a browser blob URL; do not place credentials in URLs.

Missing login returns 401, missing/malformed selection 400, and another user's or
missing key returns the same 404. Disabled key/user, unavailable or inaccessible group,
IP ACL, quota, expiry and model errors preserve shared admission behavior.
No client-provided user ID is trusted. Job queries remain scoped to both user and key.
All Studio batch routes retain the original batch billing admission, including GET;
this does not add the separate realtime-task read exemption. The raw URL is never rewritten.

## Scope and verification

This is a thin server façade; no schema, provider, queue, wallet or settlement changes.
Existing security auditing is retained by registering the same BatchImageHandler instance.
The original `/v1` routes are unchanged. Read-by-ID now loads AllowedGroups so exclusive
key groups receive the same permission data as credential-based authentication.

Developer evidence: `.omo/evidence/image-studio/session-api/README.md`.
Tests use local repositories/fakes, with no external provider calls. Route tests exercise
owned reads and reach the original Submit service's batch group gate; existing service
unit tests separately exercise successful provider enqueue and single reserve behavior.
This is not a claim of real-provider or browser acceptance.

The current frontend model directory/history adapter and its scoped browser
acceptance are documented separately in `IMAGE_STUDIO_REDESIGN_20261108.md`.
