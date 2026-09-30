# OpenAI image batch final result could not be uploaded to COS

## Symptom and impact

OpenAI Images batch items executed and their individual result objects appeared
in COS, but the batch remained `running`. Every provider status check retried the
final combination step and logged `put COS delivery object failed`; the final
`output/results.jsonl` object was never created. A client therefore could not
receive or settle an otherwise completed durable batch.

## Root cause

The final result combiner passed an `io.PipeReader` directly to the
S3-compatible `PutObject` implementation. The COS SDK hashes the payload and
may replay it for retries, so this upload path requires a seekable body. Item
and manifest writes used `bytes.Reader` and were unaffected, which made the
failure specific to the final combined object.

## Repair contract

The combiner now copies the already bounded item result objects into a private
temporary file, verifies that the copied byte count still matches the sizes
observed before combination, seeks back to the beginning, and uploads that
seekable file. The file is closed and removed on every return path.

Each item result remains capped at 32 MiB. With the public high-resolution
limits, the theoretical temporary-disk bound is 480 MiB for fifteen 2K outputs
and 320 MiB for ten 4K outputs; the operation keeps only a copy buffer in memory.
The 1K fifty-image limit has a theoretical 1.6 GiB bound.

The regression test wraps the fake object store and rejects the final output
upload unless its body implements `io.ReadSeeker`.

## Verification and rollout

Run the focused OpenAI Images batch provider tests and the repository lint/test
gate before deployment. After blue-green rollout, an existing durable batch
must be able to resume and create its final output object without re-running
completed items. A fresh batch with independent prompts and references must
then complete, settle billing, and return downloadable results.

## Rollback

Revert the provider and test commit through the canonical blue-green release
workflow. Do not manually fabricate the final COS object or alter batch state
in the database; durable item objects are the authoritative recovery inputs.
