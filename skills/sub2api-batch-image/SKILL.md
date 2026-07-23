---
name: sub2api-batch-image
description: Use when the user wants Codex to submit, monitor, download, retry, or cancel Gemini/Vertex batch image generation jobs through Sub2API, including prompt lists, per-item reference images, output_count repeated generations, private-COS item downloads, and recovery records.
---

# Sub2API Batch Image

Use this skill to act as the batch-image execution agent. The user should not need to fill
the web form manually. Infer the task name, prompt list, reference-image mapping, model, API
key, and output directory from the conversation or files; ask only when a required decision
is missing.

## Required Inputs

- `SUB2API_BASE_URL` or an explicit endpoint from the user.
- A user API key that belongs to a group with batch image generation enabled.
- Prompt list and output directory.
- Optional: preferred model, task name, reference images, and output count per prompt.

Never write API keys, reference-image Base64, cloud credentials, COS signed URLs, or Google
signed URLs into chat, logs, git commits, recovery records, or public files.

## Hard Capacity Rules

- Before submitting, calculate `expected_output_count = sum(item.output_count || 1)`.
- One batch job must generate at most `200` output images. Split larger work before submission.
- Reference images are inputs, not output images. Do not treat their job-level attachment
  guardrail as an output limit or planning target.

## API Workflow

Use the public batch-image API:

```text
GET    {base}/v1/images/batches/models
POST   {base}/v1/images/batches
GET    {base}/v1/images/batches/{id}
GET    {base}/v1/images/batches/{id}/items
GET    {base}/v1/images/batches/{id}/items/{custom_id}/content?image_index=0
POST   {base}/v1/images/batches/{id}/cancel
```

Use `Authorization: Bearer <user api key>` and an `Idempotency-Key` for submission.

## Submit Request

```json
{
  "model": "gemini-2.5-flash-image",
  "task_name": "optional human-readable name",
  "image_size": "1K",
  "response_mime_type": "image/png",
  "items": [
    {
      "custom_id": "img_001",
      "prompt": "full prompt text",
      "output_count": 1,
      "reference_images": [
        {
          "id": "subject",
          "type": "reference",
          "mime_type": "image/png",
          "data": "<base64 without data:image/... prefix>"
        }
      ]
    }
  ]
}
```

`output_count` defaults to `1`. It repeats the same prompt and reference set as real provider
items; do not rely on one upstream request returning multiple images.

Limits:

- `output_count`: `1` to `4` per prompt.
- Expected output images per job: at most `200` after `sum(output_count)`.
- Flash Image models: at most `3` reference images per prompt item.
- Pro Image models: at most `14` reference images per prompt item.
- Reference attachments per job: at most `1000` after output-count expansion.
- Inline references per job: at most `128MB` decoded after expansion.
- Each inline reference: at most `10MB`.
- Reference MIME types: `image/png`, `image/jpeg`, `image/webp`.
- `file_uri` references must be internal Google Cloud Storage `gs://...` URIs.

For repeated or large reference images, prefer managed `gs://` references or split the job.

## Private COS Download Rules

New Vertex jobs are copied from Google to private Tencent COS by a Cloudflare Workflow before
Sub2 marks them complete. Image bodies do not pass through the Sub2/Japan server.

- List items first and download only `succeeded` items.
- Call the stable authenticated Sub2 item-content endpoint for every image index.
- The endpoint returns HTTP `302` to an exact-object COS GET signature valid for only a few
  minutes. Follow it in memory (`curl -L`, an HTTP client with redirects enabled, or equivalent).
- Do not print, persist, cache, or include the redirect `Location` in error messages. Anyone who
  obtains that URL can read that one object until it expires.
- If a signature expires, call the authenticated Sub2 endpoint again; never try to refresh or
  modify COS query parameters.
- Never request or store COS `SecretId`/`SecretKey`. Codex is authorized through the user's Sub2
  API key and ownership checks, not by direct bucket credentials.
- Server-side ZIP is intentionally unavailable for COS-delivered jobs because it would pull all
  media back through Japan. Download individual items and assemble a ZIP locally only if needed.
- Legacy pre-COS jobs may still use the old server-streamed download path.

Example that avoids exposing the redirect:

```bash
curl --fail --silent --show-error --location \
  -H "Authorization: Bearer ${SUB2API_API_KEY}" \
  "${SUB2API_BASE_URL}/v1/images/batches/${BATCH_ID}/items/${CUSTOM_ID}/content?image_index=0" \
  --output "${OUTPUT_FILE}"
```

Do not add verbose or trace flags to this command because they reveal signed URLs and auth
headers.

## Cost And Recovery

- Billing is based on successful output image count and configured batch unit price.
- Reference inputs still consume provider input tokens and temporary storage, repeated by
  `output_count`; warn before large reference-heavy submissions.
- Immediately after submit, write `batch-image-resume.json` in the output directory without an
  API key or signed URL.
- Record `endpoint`, task name, batch ID, model, output directory, request file, submission time,
  last status, status URL, items URL, prompt count, expected output count, and custom-id mapping.
- If the request file contains reference Base64, keep it only in the user-selected output
  directory and never commit it.
- Update the record after status checks with time, status, counts, actual cost, and failures.

## Polling And Completion

- First status check: `20-30s`.
- `queued`: every `60-120s`; after three unchanged checks, stop active polling and report it.
- `running`: about every `60s`.
- `processing_results` / `settling`: every `20-45s`.
- On completion, report task name, batch ID, success/failure counts, actual cost, and save path.
- For partial failures, list failed `custom_id`, code, source, and a short reason.
- Retry only failed items; never resubmit already successful custom IDs.
- Before cancellation, warn that already indexed successes remain billable and the remaining
  hold is released.
- Do not fetch image bytes just to inspect a list; download only for saving or explicit preview.

