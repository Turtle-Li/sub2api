# Payment fulfillment reliability — 2026-09-10

Task ID: PAY-FULFILL-20260910
Type: reliability verification and scoped hardening; route concrete defects through Bug Analysis before repair.
Priority: P1 before ordinary customer purchasing.
Source baseline: fork/main and production revision `7f8b274b7d5f4b67d86b701061a30f256ddffff0`.
Worktree: branch `codex/payment-fulfillment-reliability`, isolated from existing dirty payment-release and main checkouts. The branch helper rejects remote refs, so its explicit base is the existing `codex/upstream-v024` branch at the same verified revision. Branch preflight flagged the old canonical/main baseline; no old checkout is reused for code edits.

## Acceptance

- AC1: A trusted paid order grants its configured balance or subscription exactly once, including associated plan benefits. Browser return never authorizes fulfillment.
- AC2: Concurrent delivery of the same event/order cannot duplicate balance, subscription days, gifts or commission. Different paid orders for the same user/subscription must accumulate correctly.
- AC3: Failure before commit rolls back; process loss after business commit cannot duplicate benefits when processing resumes.
- AC4: Retryable fulfillment failures recover automatically after dependency recovery/restart. Pending paid work must be durable and discoverable; exhausted/manual-review work must remain visible with an explicit recovery path.
- AC5: Acknowledgement occurs only after successful processing or durable ownership of the remaining work. Cross-service delivery and product fulfillment have distinct states.
- AC6: Local PostgreSQL concurrent and failure-injection tests prove financial/entitlement invariants; report worker concurrency separately from any measured throughput. No invented production capacity claims.
- AC7: Ordinary customer purchasing stays disabled; no extra real charge/refund is required for fault injection.

## Architecture review scope

Compare the existing PostgreSQL outbox/inbox and local fulfillment recovery against requirements before adding infrastructure. An external MQ does not remove product database transaction or deduplication obligations. Preserve payment-service funds authority and product entitlement authority.

Logic index: absent in the two relevant repositories; loaded blocks: none; index health: fallback-search. Current source, contracts and focused tests are authoritative.

## Work DAG and ownership

1. Central outbox exploration (read-only) and Sub2 fulfillment exploration (read-only) run independently; root owns integration/architecture decision and acceptance scope.
2. Root records evidence-backed defects, design and exact allowed write paths; one Sub2 implementation writer in this worktree. Central changes, if required, remain a separate independent change set.
3. Targeted red/green regression and PostgreSQL fault/concurrency tests; no production fault injection.
4. Independent QA then code review on frozen files and hashes; failures return to the implementation owner.
5. Update integration/recovery operations documentation and report actual covered cases, residual boundaries and deployment status. Any authorized release follows the existing two-node maintenance-lock/blue-green procedure, preserving Azure background ownership and existing in-memory credential agents.

Rollback: retain financial/inbox/audit history. Prefer backward-compatible code changes; any new persistent structure requires an additive migration and explicit mixed-version analysis. No delete/reset of money or idempotency records.

Initial evidence: prior 1fen WeChat and 2fen Alipay live tests cover balance recharge and full refund, not subscription fault/concurrency acceptance. Existing service baseline tests pass; actual PostgreSQL inbox/refund baseline is running. No implementation changes yet.

## Recovery design and operating boundary

The payment_orders table is the durable fulfillment work queue. Every minute the existing leader/background-gated expiry job runs reconciliation, expiration and fulfillment recovery with independent30-second deadlines (100-second outer deadline,180-second leader lease). Recovery selects paid_at-bearing PAID/FAILED orders unchanged for at least60seconds and RECHARGING leases older than5minutes, oldest first,100 per pass and at most4 concurrent executions. Refund terminal/in-progress statuses and authoritative refund manual-review fences are excluded before LIMIT; fence-read failures stop processing. Completion continues through existing balance redemption/subscription transaction+audit idempotency and timestamp-CAS lease ownership.

No RabbitMQ/Kafka is introduced. Central PostgreSQL outbox persists verified money events with the funds transaction and retries transport; Sub2 independently recovers durable entitlement work even after webhook handling stops/restarts. Transport DEAD still needs an operator after its configured retry budget; permanent business/data problems and refund review remain visible and require correction before safe recovery. Normal retries do not fabricate operator audit events.

Current limits are bounds, not a benchmark or production-QPS promise. Cache invalidation is a separate boundary: durable API-key auth invalidation exists, but failed billing Redis invalidation can retain an old balance/subscription cache for about5minutes; subscription L1 expires about9–11seconds. This may delay visibility/access after a successful database grant. This patch does not claim durable email delivery or eliminate every cache-staleness case.

Developer findings repaired: confirm/alreadyProcessed DB errors cannot be acknowledged as successful; acquired lease must retain the caller's exact timestamp even if another worker takes over before reread; manual-review rows cannot occupy the entire recovery candidate window. Fault injection uses disposable PostgreSQL and synthetic orders, never production charges/refunds. Final source hashes and independent QA/review/release evidence remain pending below.

## Prior snapshot and QA outcome (superseded by review repair)

- `backend/internal/service/payment_fulfillment.go`: `83ef29ceb01cb5493d6b7f343b9275eab586ca1a899390a25d48f2bbf9ac72d2`
- `backend/internal/service/payment_fulfillment_recovery.go`: `31dd78caf45490656e6eb106fb38d9bd11cc8882cdd9c89003c1ccef1e5e1d2d`
- `backend/internal/service/payment_order_expiry_service.go`: `7dd2cbe4293e596e0ab96b3ba9eb5b0f371835170da9d82676062e6a9be054c6`
- `backend/internal/service/payment_fulfillment_recovery_test.go`: `d4ebf88d9543b320f6107390f4f8f57492e32a6c6a64be060ec3f25033149332`
- `backend/internal/repository/payment_fulfillment_recovery_integration_test.go`: `509dd348e9f2d3fd763c00eb57b2f1aed69a4f20d7b21997d09417deddf210a8`

Developer final scoped unit-race PASS10.915s and actual PostgreSQL recovery-race PASS8.610s. Independent QA_PASS: PostgreSQL18/Redis recovery matrix10.463s (before the two-line post-capacity cancellation guard), final cancellation-race3.290s, recovery/lease/DB-error/expiry units1.900s, existing entitlement/concurrency/notification regressions1.566s, webhook ACK/semantic regressions1.147s and2.373s. Final5file hashes matched. Cancellation regression blocks the executor read after selection, cancels it, verifies PAID remains and no lease/redeem/balance grant; unretried work stays durable. No P0/P1 QA defects. Review/deployment status will follow.

Production preflight confirms only2 paid orders, both REFUNDED, ordinary payment_enabled=false; no existing paid backlog will be granted by this release. Central000017 outbox-history cap repair is independently QA/reviewed and deployed, all6services healthy and all4 real pilot events still delivered exactly once; that migration is in the separate payment-service repository.

2026-09-10 pre-release backup gate: /opt/sub2api-db-backups/sub2api-db-backup-20260910-021017.tar.gz,254674575bytes,SHA256af2b4f69687dfb71dbbb8b569c91fe0c204cf92d2507362e181cb7b11ad34476. Canonical systemd backup service exited0/success. Installed isolated restore-smoke passed outer/inner checksums, PostgreSQL restore+pg_amcheck and Redis loading; schema_count292/schema_hashfec4bbfeaabcbddd00adb365400ddca4. Production was not restored. payment_enabled=false; both existing paid orders REFUNDED.

## Independent review refinement — refund fence and lease race

REVIEW_FAIL P1 on the prior QA-passed snapshot: a signed uncorrelated/contradictory refund writer can lock the order and commit a durable manual-review fence after recovery's nonlocking recheck but before its lease update. The later claim has no fence predicate, so recovery can start after the fence exists. This is a confirmed interleaving, not a failed test assertion or hypothetical load claim. No Sub2 source was published/deployed at this checkpoint.

Approved bounded repair: use an explicit recovery lease-acquisition policy; in a short Ent transaction lock the same payment_orders row used by refund writers, re-read paid/state and refund fence, acquire the existing timestamp-CAS lease and commit before entitlement execution. Preserve normal public balance/subscription callback methods and common doBalance/doSub idempotency. Do not hold the order transaction across network/entitlement work. The lease transaction is the ordering point: a committed review fence must prevent a subsequent automatic claim. No hidden context flag or copied service mutex. Add a deterministic real-PostgreSQL interleaving test, cancel/rollback checks, then new source hashes and fresh QA/review before publication.

## Final refund-fence repair snapshot

The recovery-specific acquirer now locks and reloads the order, validates paid eligibility and the authoritative refund fence, and acquires the timestamp CAS lease in one short transaction. It commits before shared balance/subscription execution. Public ordinary callback/admin executors retain their existing acquirer policy. A committed fence before claim prevents automatic work; this is not cancellation of a claim already committed before a later refund event.

Developer evidence: scoped unit race PASS 10.353s; all four real PostgreSQL recovery groups PASS 9.059s; final boundary + cancellation rollback race PASS 7.052s. The identical boundary case against temporarily restored historical nonlocking acquisition failed its no-recovery invariant (expected 0, actual 1). Fixed code was restored before final green/freeze.

- `backend/internal/service/payment_fulfillment.go`: `391c812d577a545ea2c3066149af763c4af7b72859aa179bffb74516a251d071`
- `backend/internal/service/payment_fulfillment_recovery.go`: `9393e135dc1fa85a27ca649b2a02618c709585da7c75b4548c81da6550a009c6`
- `backend/internal/service/payment_order_expiry_service.go`: `7dd2cbe4293e596e0ab96b3ba9eb5b0f371835170da9d82676062e6a9be054c6`
- `backend/internal/service/payment_fulfillment_recovery_test.go`: `d4ebf88d9543b320f6107390f4f8f57492e32a6c6a64be060ec3f25033149332`
- `backend/internal/repository/payment_fulfillment_recovery_integration_test.go`: `03f2ae71953217b57d5e834c3dcdc406828a0983fa79ac43ea217d1db53fa616`

Fresh independent QA and review apply to this final snapshot; prior PASS is not a release gate for these changed files.

Final independent QA_PASS on the exact five hashes: actual four-group PostgreSQL/Redis race matrix 15.052s, focused service recovery/ordinary callback/entitlement race 9.756s, unknown-order ACK handler regression 1.334s, unified event semantics 1.299s. Both hash guards and whitespace check passed. QA independently reran fixed boundary/rollback tests; legacy red is Developer evidence. Full HTTP+DB outage injection was not repeated; service errors, ACK sentinel and handler branches cover that boundary composition. Independent code review pending.

Final independent REVIEW_PASS: all five frozen hashes match, original P1 closed; no new actionable P0/P1/P2. The reviewer confirmed shared row-lock/fence/CAS transaction, commit before entitlement side effects, ordinary callback/admin policy compatibility and deterministic PostgreSQL boundary/rollback coverage. Source is cleared for the authorized release; deployment results are recorded separately after execution.

## Production rollout completed — 2026-09-09 18:59 UTC

Reviewed runtime source `c82e5df699575a7b4f2faa76253b7d96208ab2a7` is deployed on both nodes. Build-only GitHub run34390838357 succeeded; metadata revision/version0.2.4/source/linux-amd64/run-id and actual archive bytes50822256/SHA2567abab85798e8946b346c21d36b1fc5eb2acc8378ccca02385514527f04185eb7 all matched before upload. Both receivers loaded image `sha256:19b7c12a530b2f4475048e7d115bc5904f79d2bd4c49d566fcb05f0acec789ba`.

- Azure `sub2api-candidate`: `sub2api-green`, accepting/background=active, healthy; tag `sub2api:auto-20260910-025504-c82e5df6`. Release log `/var/log/sub2api-release/gha-20260910-025504-c82e5df6-3520201`. Previous blue drained and stopped.
- Old origin `sub2api-new`: `sub2api-green`, accepting/background=standby, healthy; tag `sub2api:auto-20260910-025637-c82e5df6`. Release log `/var/log/sub2api-release/gha-20260910-025637-c82e5df6-2554809`. Prior blue remains under the canonical persistent drain monitor; no manual stop/override was used.

Both installed receiver/helper hashes matched repository before execution. One-shot0600 mode configs sourced the existing root config and set activate/preserve-standby respectively; both were removed after successful receiver completion. Both payment credential-agent container IDs are unchanged and healthy. Both automatic deployment timers remain disabled/inactive. No DNS, purchase, provider, cache configuration or database schema changed. Each canonical release reported app_5xx=0/app_fatal=0/caddy_5xx=0. Public `https://www.turtleligpt.com/health` returned `{"status":"ok"}`.

Final read-only database check after both deployments: payment_enabled=false; order1 wxpay/balance and order2 alipay/balance both REFUNDED, each with one RECHARGE_SUCCESS and one REFUND_SUCCESS audit; zero paid PAID/FAILED/RECHARGING candidates. No new charge/refund or synthetic production order. Active-node startup observation found zero recovery-error logs; no backlog existed to exercise recovery in production, so fault/concurrency assurance comes from the independently executed PostgreSQL test matrix.

Rollback keeps all financial/idempotency records and uses retained7f8b274b image through the canonical release helper with the same background roles. This post-release record changes documentation only; runtime/source identity remains c82e5df699575a7b4f2faa76253b7d96208ab2a7.
