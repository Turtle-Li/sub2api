# Image Studio: recover paid results without replay

2026-11-21 — local implementation; not a production release or a replay of
historical tasks.

## Incident and scope

On production `f89d19620`, Image 2.6 Sunburst 2K job
`imgbatch_cdce67503ce289c3713525add1126561` received a 2048 × 3072 Office Mini
PNG (5,147,102 bytes; result GET 200). Indexing ended after about 133.4 seconds
with `IMAGE_UPSCALE_STORE_FAILED`, the 0.20 hold was returned, and the gallery
received no result. This localizes the failure to writing the final object,
after both generation and upscale succeeded. The duration is consistent with
the old single COS write budget; there is no evidence assigning a root cause
to COS itself versus the network or our client. The Shanghai image bucket's
console showed access-log delivery off and CLS unopened. No exact cloud-side
PUT record was obtained and no cloud setting changed.

## Recovery contract

- Migration 289 adds immutable job/item/stage result checkpoints and durable
  retry metadata. Local sequential image providers write a successful result
  line before the combined archive, and 2K/4K processing stages validated final
  bytes before publishing. They use a private write-ahead copy in
  `${DATA_DIR:-./data}/batch-image-delivery-spool-v1` (`/app/data` in the
  production container). Files are synced, atomically published, and imported
  under the job row fence into PostgreSQL. A database outage leaves the copy
  recoverable on the shared active/standby data volume; if only the volume is
  unavailable, a healthy database can still stage the result. A committed
  checkpoint wins over later attempts, and an obsolete file cannot reattach
  to a completed, deleted or wrong-stage job.
- Retry the existing result, not the chargeable generation. A cancelled
  sequential job stops new work but still delivers its already staged results.
  For a finished 2K/4K output, the next worker reuses the staged bytes and the
  same fenced COS key. Mini-first and the configured Aliyun fallback remain
  the upscale policy. Only provably pre-submit upscale faults are reattempted
  by the indexer; it does not present a raw 1K image as delivered 2K/4K.
- Retry delays start at 5, 15 and 45 seconds, then grow to a 20m15s ceiling.
  Redis is scheduling, not the recovery record; the existing scanner can
  re-enqueue an active job missing from Redis. The delayed run can then read
  from PostgreSQL or re-import the private spool. Pending delivery is not an
  expiry candidate and has no automatic abandon deadline.
- A task with indexed output remains in `settling` when capture or state
  finalization cannot complete. The same wallet/key hold, capture request ID,
  pricing snapshot and ledger idempotency are reused. Do not debit or release
  a second time. Confirmed empty/failed tasks retain their existing refund
  semantics. On successful settlement the checkpoints and retry metadata
  are deleted transactionally and the configured 360-hour access deadline is
  measured from settlement. Originals remain in private Tencent COS; neither
  the spool nor PostgreSQL is a gallery-serving store. The app deadline is
  separate from bucket/service safety lifecycles.
- Gemini Vertex 1K uses its existing GCS → Cloudflare → COS transport. The
  companion `Turtle-Li/turtle-batch-image-pump` reference checked at
  `5e8fc045905795debb448b7673858e0847fe4b57`, `src/index.js`, `src/core.js`,
  and `test/core.test.js`, accepts a run ID scoped to the same job ID and
  exact archive keys. Re-POST of the same run ID returns the existing workflow.
  Ordinary control or COS verification errors keep polling the same run. A
  matched `errored` receipt permits up to two new **transport** runs with fresh
  URLs for the already generated GCS output. An ambiguous response or an
  operator-terminated workflow is not authority to rotate the run. No new
  Vertex generation or companion deployment is needed. The GCS source must
  still exist to recopy; the app cannot recreate an expired source.

## Alerts and operations

Migration 290 adds a reusable operational outbox. Call
`OperationalAlerts.Notify(ctx, OperationalAlert{Component, Stage, SubjectID, Code})`
with stable internal identifiers. It reuses the enabled Feishu bot and existing
memory-only webhook injection; nothing is fetched or posted inline by the caller.
The active generation claims messages, deduplicates by the four fields and
retries unacknowledged deliveries independently. A lost provider acknowledgement
can result in a repeated Feishu message; the outbox ID stays the same. Never put
prompts, pixels, recipients, credentials, object URLs or arbitrary error bodies
in these fields. The image pipeline reports staging, archiving, verification,
upscale, indexing and settlement errors; a recovered spool copy also reports
when PostgreSQL was previously unable to admit it.

For a known incident use a read-only query on `batch_image_delivery_retries`
to see `stage`, `code`, `failure_count`, `retry_at`; join the job status by its
exact batch ID. Inspect counts, not content, in `batch_image_result_checkpoints`
and inspect alert attempts/acknowledgement in `operational_alert_outbox`.
Repair the named dependency and let the next scheduled attempt finish the
original task. There is no customer action or manual retry button needed for
storage/DB failures. Escalate persistent pricing/count conflicts or an exhausted
Vertex transfer without modifying the request fingerprint or reusing an active
workflow. Any further Vertex transport rotation requires operator confirmation
of the exact terminal receipt and surviving GCS output under the normal job
fence. Never reset job status to `submitted`, remove attempt markers, replay the
generation API, or reopen a historically refunded job.

The 1 GiB PostgreSQL checkpoint threshold is **soft admission backpressure**
before a local job's first paid item, not a hard capacity or expiration policy.
An in-flight job can exceed it. Watch pending bytes, age, free application volume
space and PostgreSQL storage/backup growth; pause admission and investigate if
pending bytes reach the threshold or a known terminal fault does not clear.
Unfinished `.writing-*` files are not publishable; remove orphaned temporary
files only after verifying no writer and no active task can use them. Ready files
are not ordinary trash. This design cannot recover bytes lost before either
durable write, or when both temporary stores are lost; the upstream-attempt
fence still prevents replay of an uncertain generation.

## Release gate and verification

For the opt-in single-image Shanghai lane, follow the companion Mini source,
private ingress, retry and ACK drain boundaries in
`deploy/shanghai-image-delivery/README.md`. A Shanghai control-only transfer
smoke does not validate the end-to-end application path or wallet settlement.

The schema change is additive; never restore an old database to roll back.
Use the documented shared active-generation fence and shared app data volume.
Verify migrations 289/290, the volume's writable private subdirectory, normal
360-hour output configuration, and Feishu runtime readiness on the same build.
Retain a recovery build that recognizes pending checkpoints. Do not return an
older worker to active while checkpoint/spool records or protected settlement
jobs remain. A new canary or real bot message needs explicit release/QA scope.

Local coverage uses fake provider/Mini/COS/Feishu responses plus disposable
PostgreSQL/Redis: repeated write failure, unacknowledged COS success, process
rebuild and import after a database disconnect, unchanged bytes/key, no provider
or Mini replay after checkpoint, cancellation/cleanup fences, one ledger capture
after recovery, Vertex transport-only replay, and outbox claim/deduplication.
The real PostgreSQL wallet test also records a successful capture followed by
six failed finalization writes; after restart-style reconstruction it checks
the original debit, cleared reservation, and exact 360-hour deadline.
