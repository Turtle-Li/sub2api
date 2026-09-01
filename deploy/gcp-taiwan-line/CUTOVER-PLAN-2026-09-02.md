# Sub2API Taiwan Premium ingress cutover plan

Task ID: `SUB2-TW-CUTOVER-20260902`

Status: `EXECUTING_BLOCKED_BEFORE_DNS`

## Outcome and boundaries

Use the retained GCP Taiwan Premium address as a transport-only ingress for
`api.turtleligpt.com`, keep the Azure Japan host as the sole application
origin, and retain the current server as the production and DNS rollback
target through the migration window. The GCP host must not run Sub2API,
PostgreSQL, Redis, a background worker, or an OAuth egress proxy.

This change does not claim uniform acceleration for every mainland user, does
not revive the retired Cloudflare optimized-IP POC, and cannot transparently
migrate a client that has permanently hard-coded the expiring server's IP.

## Acceptance criteria

- `AC-01` — Production DNS remains on `206.119.172.211` until isolated GCP to
  Azure tests, QA, review, and rollback checks all pass.
- `AC-02` — GCP forwards TCP 80 and 443 only to the pinned Azure origin. TLS is
  terminated on Azure with the existing certificate; HTTP/1.1, HTTP/2, SSE,
  and WebSocket stay on a byte-stream path. Authenticated streaming evidence
  is still required; an unauthenticated 401 does not prove stream carriage.
- `AC-03` — The Azure listener accepts PROXY protocol only from
  `130.211.243.139/32`, rejects spoofed PROXY headers from other sources, and
  continues to accept ordinary direct connections during rollback.
- `AC-04` — Azure is the only intended post-cutover application/worker owner.
  Stable OAuth proxy binding and all shared-queue consumers are verified or
  explicitly fenced before production traffic is moved.
- `AC-05` — A canary run proves health, expected unauthenticated failure,
  authenticated basic generation, Responses streaming/continuation, and image
  behavior through the exact GCP address without printing credentials.
- `AC-06` — The complete production record set, proxy status, A-record
  pre-change value, observed TTL, exact rollback command, bounded error/latency
  stop conditions, and a live old-origin health check are recorded.
- `AC-07` — The old origin is not stopped during DNS propagation or while
  established streaming connections remain. It stays available until the
  rollback window closes or the provider expiry forces retirement.
- `AC-08` — Post-cutover evidence confirms the public answer, TLS identity,
  origin reachability, real requests, error rate, and old-origin drain state.

## Task DAG

| Task | Depends on | Owner / allowed writer | Output and validation | Rollback / invalidation |
| --- | --- | --- | --- | --- |
| `T0` Freeze baseline | none | Root operator; docs only | DNS, instance IDs, image digest/tag, Caddy/certificate identity, connection counts, dirty-tree exclusions | Re-run if DNS, active container, image, certificate, or host state changes |
| `T1` Prove single-owner readiness | `T0` | Root operator; authenticated admin API plus node-state controller | OAuth accounts have stable proxy binding; old origin is HTTP-serving/background-standby; Azure is the sole active background owner after recreation | Stop before ingress work if ownership is ambiguous, container-visible state differs, or duplicate consumers cannot be fenced |
| `T2` Build transport artifacts | `T0` | Root developer; `deploy/gcp-taiwan-line/` only | Versioned HAProxy, Azure Caddy patch, bootstrap, validation, smoke, and rollback artifacts; syntax/static tests pass | Revert only these new artifacts; unrelated dirty reports remain untouched |
| `T3` Stage Azure listener | `T1`,`T2` | Authorized root operator on `srv-azure-sub2api-relay-jp` | Atomic Caddy backup, validation, reload, direct-origin health/TLS/API checks, PROXY allowlist test | Restore the exact backup and reload Caddy; production DNS is still old |
| `T4` Stage GCP ingress | `T2`,`T3` | Authorized GCP operator | Start exact retained VM/IP; install pinned transport package; least-privilege firewall; HAProxy syntax, local health, public TCP tests | Stop VM, disable ingress rule/tag, restore Azure Caddy if no longer needed; DNS remains old |
| `T5` Isolated real-request canary | `T4` | Developer then independent QA | Local-resolution/canary evidence for `AC-02`,`AC-03`,`AC-05`; compare direct Azure and GCP responses | Any functional, streaming, auth, image, TLS, or source-IP failure blocks cutover |
| `T6` Frozen review gates | `T5` | Independent QA, Reviewer, then Claude advisory review | QA PASS and Review PASS on the same hashes plus Claude findings disposition | Any P0/P1 or unresolved P2 returns to the owning task and invalidates later evidence |
| `T7` DNS canary/cutover | `T6` | Authorized production executor only | Change only the A record to `130.211.243.139`; monitor real traffic and origin health through at least two TTLs | Immediate A-record rollback to `206.119.172.211` on stop condition; keep both origins running |
| `T8` Drain and handoff | `T7` | Root operator; docs/registry plus remote read-only until retirement authority | Public and real-request verification, old-origin connection drain, cost/expiry and retirement record | Roll DNS back while old service remains healthy; do not retire old host from inferred inactivity alone |

## Applicable gates and stop conditions

- QA level: Level 2, because this is an exact production ingress candidate with
  a confirmed target and a live smoke/cutover runbook.
- Review axes: transport correctness/security and requirement/spec compliance.
- Claude advisory review occurs after the change snapshot and evidence are
  frozen; it cannot override a failed local gate.
- Stop immediately on TLS mismatch, origin loop, loss of ordinary Azure direct
  access, untrusted PROXY spoof acceptance, any duplicate-worker ambiguity,
  authentication regression, materially elevated 5xx/timeout rate, or an
  unavailable old-origin rollback target.
- Do not shut down the old origin solely because DNS propagated. Hard-coded-IP
  usage cannot be inferred from hostname access logs and needs a separate
  owner/client migration decision before the September 11 expiry.

## Rollback invariant

Until retirement is explicitly authorized, rollback is one DNS A-record change
back to `206.119.172.211`; no database migration or data-format change is part
of this work. Azure and GCP configuration changes each have an exact on-host
backup and validation command, and GCP can be removed from service by stopping
the instance without touching Azure application state.

```yaml
knowledge_candidate: yes
knowledge_candidate_reason: Reusable zero-data-migration pattern for a transport-only ingress in front of a stateful single-owner application origin.
candidate_type: infrastructure-cutover-pattern
origin_repo: sub2api
origin_path: deploy/gcp-taiwan-line/CUTOVER-PLAN-2026-09-02.md
origin_revision: 9585e61bcf5a958c1b6aa609efacb4d6bd2c9908
source_change_set: SUB2-TW-CUTOVER-20260902
classification_suggestion: project-local
```

## 2026-09-02 07:05 execution checkpoint

| Task | State | Evidence / blocker |
| --- | --- | --- |
| `T0` | Complete | Authoritative baseline: A `206.119.172.211`, TTL 300, no AAAA/CNAME/SVCB/HTTPS; exact GCP/Azure identities, certificate, image, and rollback origin frozen. Cloudflare control-plane proxy status still needs authorized confirmation |
| `T1` | Blocked | Old origin is healthy, its active state-file binds are repaired, and it is the sole background owner. Azure remains standby with stale application state-file binds; two active OAuth accounts remain unbound, so ownership cannot yet move to Azure |
| `T2` | Complete, revised | Frozen revision adds exact client-IP headers, route-shadow rejection, durable blue-green recovery, inode-preserving writes, runtime-verifier rollback, immutable HAProxy origin state, explicit proxy bypass, CI, and the exact-digest old-origin compatibility hotfix |
| `T3` | Complete, transaction retained | Azure Caddy SHA `ee59c226...`, matching host/container inode, adapted/live fingerprint `8a9e0879...`, forged-PROXY rejection, direct fallback, exact rollback and re-stage pass. Only the Taiwan listener transaction remains; all other Caddy mutators are fenced |
| `T4` | Complete | GCP HAProxy `2.6.12-1+deb12u3` config `e6891a35...` is active as an unprivileged chrooted worker. A forced verifier failure proved exact restore/reload; final update and public canary pass; one-shot metadata is absent |
| `T5` | Partial | HTTP/2, automated TLS fingerprint equality, redirect, no-h3, manual source-IP observation, and unauthenticated SSE/WS 401 reachability pass; authenticated generation/continuation/image and a real carried stream remain pending |
| `T6` | In progress, durable snapshot | Implementation revision `9585e61bcf5a958c1b6aa609efacb4d6bd2c9908` includes the accepted repository and Claude findings. Live transport and rollback gates pass; final frozen re-review is still required |
| `T7` | Not started | Production DNS deliberately unchanged |
| `T8` | Not started | Old origin remains healthy, DNS-active, and the sole background owner |

Detailed evidence and fail-closed defect disposition are recorded in
`LIVE-VALIDATION-2026-09-02.md` and
`REVIEW-DISPOSITION-2026-09-02.md`.
