# Image Studio session API

The default-off [wallet path](#session-only-wallet-generation) supplements the
legacy key-backed path below. Old jobs keep their original owner/key boundary.

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
nullable RFC3339 `expires_at`, `expired`, `quota_exhausted`, `can_generate`, and
`has_history`. The dedicated directory query selects no credential, usage IP or
live concurrency. Generation eligibility comes from the active image group's
batch setting; history checks both job user and key ownership, excluding hidden
jobs. The client discovers only generation keys and reads only history keys,
then adds a key to history after its first successful submission. Historical
keys remain visible even if their group no longer permits generation.
Only the logged-in user's keys are queried. Directory metadata is informational;
permission and available balance are rechecked on each operation. Do not interpret
an active key as proof of batch capability; load its model directory.

## Batch operations

All paths below are relative to `/api/v1/image-studio` and require JWT plus the selected key ID.
Request and response bodies are exactly those of the existing `/v1/images/batches` API:

| Method | Path | Existing handler |
| --- | --- | --- |
| GET | `/batches/models` | Models (capability and model allowlist filtered) |
| GET | `/batches/gallery` | Gallery (joined, owner/key-bound item metadata page) |
| POST | `/batches` | Submit (validation, idempotency, security audit, service/billing) |
| GET | `/batches` | List |
| GET | `/batches/:id` | Get |
| GET | `/batches/:id/items` | Items |
| GET | `/batches/:id/items/:custom_id/content` | ItemContent |
| GET | `/batches/:id/items/:custom_id/thumbnail` | ItemThumbnail (first image, JPEG preview) |
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

## Session-only wallet generation

`GET /api/v1/image-studio/keys` exposes `X-Image-Studio-Wallet: on|off` for
discovery. On means the logged-in user is admitted to the configured Studio
pricing group. The legacy directory is used for previous works. For new work
in wallet mode the client sends its panel JWT and `Idempotency-Key`, not
`X-API-Key-ID`, to `POST /api/v1/image-studio/wallet/batches`. Models are
available at `GET /wallet/batches/models` (relative to `/api/v1/image-studio`).
Wallet history, item reads and cancellation use the same `/wallet` prefix and
remain subject to ownership even when wallet submission is disabled. Original
key-based and `/v1` routes remain unchanged. Old images can be referenced from
the wallet path, but a new job cannot claim the old key's edit root.

Full administrators set a single `group_id` and list of `account_ids` via
`/api/v1/admin/image-studio/wallet-group`. `PUT` takes `{config, expected}`, each
with those fields, and saves them together with compare-and-set. The off value
is `{ "group_id": 0, "account_ids": [] }`; no setting also means off. A group
must be active, standard OpenAI and image-enabled. Existing exclusive and
restricted-user group rules apply. These account IDs are **Studio-only**:
loading them does not write `account_groups`, enable external batch access, or
involve unselected accounts, a global pool or paid reserve. Provider/model
compatibility, the group's model allowlist and shared account admission remain
in force. The selected group's exact per-model CNY-authored image price cards
must cover the requested tier. Missing prices never inherit the group default.
Apply the normal effective group and account rates once, with no extra FX or
second batch discount on an already authored wallet-unit tariff.

Migration 277 adds `studio_group_id` to the existing jobs. The reserved owner
slot `api_key_id=0` is scoped by the panel user and is only a namespace in the
existing job/idempotency/billing ledger, not an `api_keys` row. A paid item uses
the same reserve/capture/release path as legacy batches. Wallet settlement is
visible in the Studio task and ledger; it does not introduce a synthetic row
in the API-key-backed `usage_logs`. New successful outputs use the normal
private COS result path and 360-hour deadline. Pause new jobs before any
rollback decision and use only verified slot-compatible recovery images while
any wallet task may still have work or a reservation. Keep schema and history.

The current frontend model directory/history adapter and its scoped browser
acceptance are documented separately in `IMAGE_STUDIO_REDESIGN_20261108.md`.

## Gallery and price extension (2026-11-08)

`GET /batches/gallery` returns raw `{data:[{job,item}],next_cursor?}`. Defaults
to 24 items, maximum 50; query parameters are `limit`, `cursor`, `status`
(`all|pending|completed|failed`), literal substring `q` (max240 bytes), and
optional `root` batch ID (max200 bytes, root plus direct children). The cursor
is a positive `job-row-id:item-row-id` descending boundary. It conveys no
authorization: every query still binds the session owner and selected key.
Changing filters starts a new page. This is a live status listing rather than
a frozen snapshot. No object reads occur in the metadata query. Existing
`/batches` and `/items` responses remain compatible. Repeated `custom_id`
parameters on `/items` (max100) let polling update only loaded items from very
large batches; ownership still uses the existing ListItems service.

Models now include optional `prices: {"1K":{"min":number,"max":number},...}`
for supported sizes. These reuse submission pricing, currency conversion,
user/group/image/account multipliers and provider-specific batch discounts.
An account range is not a price guarantee; settlement remains authoritative.
The UI deduplicates and routes using the same first eligible key, preserving its
quotes. Missing prices display as unconfigured; an explicit zero stays zero.

The thumbnail endpoint uses the existing completed-job/content ownership and
retention gates. It emits a white-composited JPEG, quality80, long edge at most
480px, without upscaling; it never marks an explicit original download.
Response caching is private for 300s, varying on Authorization and X-API-Key-ID.
Server cache: 32MiB/256 entries, coalesced reads, owner and output rechecked
on each actual request. Source decoding is bounded by32MiB/40MP and two
concurrent decodes. The browser cache is scoped to the mounted user's workspace,
64 blobs/16MiB. New successful items persist private cloud previews while their
result bytes are available; 2K/4K previews use final pixels. A process-cold read
opens the stored JPEG, without a provider account or original result read.
Only historical/missing previews trigger a shared-lease batch backfill, reusing
all already stored covers and scanning normal JSONL once for missing items.
An ETag supports authenticated 304 revalidation. Migration270 adds concurrent
metadata paging indexes; migration271 adds nullable deletion/retry fences to
existing jobs. No second generation task table or billing operation is added.
Storage prerequisites, bounded retry and deletion contracts are documented in
`IMAGE_STUDIO_PERSISTENT_THUMBNAILS_20261108.md`.
Current evidence and limitations: `IMAGE_STUDIO_GALLERY_OPTIMIZATION_20261108.md`.
