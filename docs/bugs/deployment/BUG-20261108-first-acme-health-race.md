# First ACME issuance races the Caddy release health check

Bug: BUG-20261108-first-acme-health-race — ANALYSIS_READY

现象: workflow 42417275856 deployed application revision d65140f7a, then
rolled back its Caddy configuration with `tlsv1 alert internal error`.

复现条件: remove the imported API certificate directive while Caddy has no
managed certificate for that hostname. The release receiver probes HTTPS
immediately after `caddy reload` returns.

调用链: `sub2api-caddy-config-release.sh` reloads Caddy, verifies its three
configuration views, then performs one public-health curl. Caddy starts
asynchronous ACME issuance; the health failure triggers forced rollback.

影响范围: initial automatic-certificate activation through the digest-bound
Caddy receiver. Ordinary managed renewals are not this configuration path.
The application revision switched successfully and remained healthy.

疑似根因: direct evidence, high confidence: Caddy logged `obtaining
certificate` at 21:55:10.005 UTC, began its HTTP challenge at 21:55:10.920,
then the release rollback canceled that challenge at 21:55:11.094. No managed
API certificate was persisted. Configuration-view checks passed far enough to
reach the single HTTPS probe. This evidence does not establish an ACME/DNS
failure; the observed issuance attempt was canceled locally.

推荐修改方案: allow bounded, strictly verified HTTPS health retries after
the unchanged configuration and body-limit checks. Retain rollback on
exhaustion. Add an operator-only acknowledgement for an already rolled-back
transaction: require its exact commit/input digest, protected metadata and
backups, the recorded old host/startup/Admin configuration and active image
revision, and successful HTTPS health before archiving its fence. Do not
change Caddy or application state during acknowledgement.

风险: retries extend maintenance-lock occupancy, bounded below two minutes
per health phase. A broken certificate still fails closed. Acknowledgement
must reject unrelated, ambiguous, modified or incompletely rolled-back
transactions. Existing normal releases must continue refusing pending fences.

Validation: delayed TLS readiness succeeds without rollback; exhausted health
readiness rolls back; acknowledgement preserves evidence and performs no
reload; mismatched provenance, backup or live configuration retains the fence.

Source baseline: d65140f7a43c76788003d319b484f1fa5723043e,
`deploy/sub2api-caddy-config-release.sh`, its existing hermetic test harness,
and filtered production Caddy/release logs from 2026-11-07 21:55 UTC.
