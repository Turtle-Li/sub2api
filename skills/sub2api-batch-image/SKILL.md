---
name: sub2api-batch-image
description: Submit, monitor, download, preview, retry, or cancel Gemini/Vertex batch image jobs through Sub2API. Use for prompt batches, per-item reference images, output_count repeats, private Tencent COS JSONL results, local image decoding/ZIP creation, and resumable recovery records.
---

# Sub2API Batch Image

Act as the execution agent. Infer the task name, prompts, reference mapping, model, API key,
and output directory from the conversation or files. Ask only when a required decision is
missing; do not make the user fill the web form.

Never expose API keys, reference-image Base64, Google/COS credentials, or signed URLs in chat,
logs, shell traces, git, recovery records, or public files.

## Submit

Use these authenticated endpoints:

```text
GET    {base}/v1/images/batches/models
POST   {base}/v1/images/batches
GET    {base}/v1/images/batches/{id}
GET    {base}/v1/images/batches/{id}/items?limit=500
GET    {base}/v1/images/batches/{id}/result-files
GET    {base}/v1/images/batches/{id}/download          # legacy only
POST   {base}/v1/images/batches/{id}/cancel
```

Send `Authorization: Bearer <user API key>`. Add `Idempotency-Key` when submitting.

```json
{
  "model": "gemini-2.5-flash-image",
  "task_name": "descriptive name",
  "image_size": "1K",
  "response_mime_type": "image/png",
  "items": [
    {
      "custom_id": "img_001",
      "prompt": "full prompt",
      "output_count": 1,
      "reference_images": [
        {
          "id": "subject",
          "type": "reference",
          "mime_type": "image/png",
          "data": "<base64 without a data URL prefix>"
        }
      ]
    }
  ]
}
```

Apply these limits before submission:

- Calculate `expected_output_count = sum(output_count || 1)`; split above `200`.
- Keep `output_count` between `1` and `4`.
- Allow at most `3` references per Flash Image item or `14` per Pro Image item.
- Keep expanded references at or below `1000`, decoded inline references at or below `128MB`,
  and each inline reference at or below `10MB`.
- Accept PNG, JPEG, or WebP inline references. Use only managed internal `gs://` file URIs.
- Treat references as inputs, not output-count capacity. Large repeated references consume
  provider input tokens; prefer managed `gs://` references or split the job.

## Download Private COS Results

New Vertex jobs finish as one or more raw JSONL objects in private COS. Sub2 returns short-lived
exact-object read capabilities only after API-key ownership checks. The JSONL contains image
Base64; Sub2 and the Japan server must not proxy those media bytes.

Use the bundled downloader from this skill directory:

```bash
python3 scripts/download_result_archive.py \
  --base-url "${SUB2API_BASE_URL}" \
  --batch-id "${BATCH_ID}" \
  --output-dir "/absolute/output/parent"
```

Set `SUB2API_API_KEY` in the existing process environment or approved secret store. Never put
its literal value in the command. The script:

- calls `result-files` without printing its response;
- downloads COS with no Sub2 `Authorization` header and rejects redirects;
- refreshes expired 401/403 capabilities through Sub2;
- streams JSONL, decodes and validates raster images locally;
- reconciles item IDs and success/failure counts with Vertex completion statistics;
- writes `images/`, `manifest.json`, `errors.json`, and a local ZIP under
  `<output-parent>/<batch-id>/`;
- falls back to `/download` only when Sub2 explicitly returns
  `BATCH_IMAGE_RESULT_ARCHIVE_UNAVAILABLE`, which identifies a legacy job.

Do not replace this with verbose `curl`, shell tracing, or code that prints the `result-files`
JSON. A signed URL grants read access to the whole task shard until expiry. On COS/CORS/network,
signature, or archive-integrity errors, fail closed; never silently route a new job through the
Japan server. Never request COS `SecretId` or `SecretKey`.

For a web preview, load only on explicit request. Stream the JSONL until the requested
`custom_id`, stop reading, and cache only a compressed local thumbnail. Do not fetch image bytes
merely to render the task list.

## Recovery And Polling

Immediately after submission, write `batch-image-resume.json` in the chosen output parent.
Exclude API keys and signed URLs. Record the endpoint, task name, batch ID, model, request file,
output directory, submission time, status/items/result-files/legacy-download endpoint paths,
prompt count, expected output count, and custom-ID-to-prompt mapping. If a request file contains
reference Base64, keep it only in the output directory and never commit it.

Poll without pressure:

- first check after `20-30s`;
- `queued`: every `60-120s`; stop active polling after three unchanged checks;
- `running`: about every `60s`;
- `processing_results` or `settling`: every `20-45s`.

Update the recovery record after each check with time, state, aggregate counts, actual cost, and
failure summary. On completion, download locally and report the task name, ID, counts, cost, and
save path. Parse the JSONL before listing per-item failures or retrying. Retry failed and missing
items only; never resubmit successful IDs.

Before cancellation, warn that already indexed successes remain billable and the remaining hold
is released. Billing uses successful output count; reference inputs can still add upstream input
token and temporary-storage cost.
