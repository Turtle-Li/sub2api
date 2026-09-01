# Taiwan Premium ingress live validation

Task: `SUB2-TW-CUTOVER-20260902`

Checkpoint: 2026-09-02 05:45 Asia/Shanghai

Verdict: **transport candidate PASS; production cutover BLOCKED**. The public A
record remains `206.119.172.211`.

## Frozen topology

- GCP `130.211.243.139`: Debian HAProxy `2.6.12-1+deb12u3`, config SHA-256
  `9ff4cf8df2f40eed0a1c8c0fab2bc7494737e7961457dcb46051431e6a3f90cf`,
  TCP transport only, Premium Tier, no service account, no retained startup
  metadata.
- Azure `4.216.216.16`: Caddy `v2.11.4` terminates TLS and accepts PROXY v2
  only from the exact GCP `/32`; ordinary direct TLS remains enabled.
- Old `206.119.172.211`: unchanged production DNS and rollback origin.
- No database schema, PostgreSQL, Redis, application image, or application
  container lifecycle change was performed in this transport checkpoint.

## Evidence

| Gate | Result |
| --- | --- |
| Local transport regression test | PASS |
| Shell syntax and ShellCheck | PASS |
| GCP bootstrap hash checks | PASS |
| GCP serial success marker | `GCP_BOOTSTRAP_PASS` |
| GCP seamless hardening update | `GCP_HAPROXY_UPDATED` then `GCP_UPDATE_PASS`; exact prior config retained |
| HAProxy local config/listeners/backend check | `GCP_TRANSPORT_VERIFY_PASS` |
| HAProxy process isolation | Config and live verifier require `user/group haproxy` plus worker chroot `/var/lib/haproxy` |
| Public GCP health | HTTP 200 |
| Public GCP unauthenticated models | HTTP 401 |
| HTTP redirect through GCP | HTTP 308 to the HTTPS hostname |
| TLS identity through GCP | Automated direct-Azure/GCP SHA-256 fingerprint equality |
| HTTP/2 | HTTP/2 on both direct Azure and GCP paths |
| HTTP/3 | Not advertised on the staged TCP-only listener |
| Unauthenticated Responses SSE reachability only | HTTP 401 on direct Azure and GCP; no stream was carried |
| Unauthenticated Responses WebSocket reachability only | HTTP 401 on direct Azure and GCP; no upgraded connection was carried |
| Direct Azure fallback | HTTP 200 health after listener staging |
| Forged PROXY v2 from an untrusted source | TLS handshake rejected |
| Client IP preservation | Manual live probes logged the same real client address. The versioned verifier now also proves the active PROXY wrapper and Caddy's no-header-trust/default-XFF contract |
| Azure rollback drill | Exact pre-stage SHA restored; direct health stayed 200; GCP TLS failed closed |
| Azure re-stage after rollback | PASS; public GCP canary and forged-PROXY checks passed again |
| Caddy transaction ownership | Older customer-Host transaction committed only after the listener rollback restored its exact expected `878825...` state; listener then re-staged to the unchanged `9dc842...` state. Both stage paths and all application/runtime mutators now fail closed on the other transaction |
| Azure effective runtime | Host/container Caddy inode and SHA equal; adapted startup and live admin-API security fingerprints both `9b992e3e3f0a4b12a88c1120fa0818a92301629c0427f82b7e1ac0a1813c33ee` |
| Old-origin rollback fence | Host and active-container views both `traffic=accepting`, `background=standby`; public health PASS |
| Production DNS | Unchanged: A `206.119.172.211`, TTL 30; no AAAA, CNAME, SVCB, or HTTPS answer |

The first Azure stage exposed a transaction-script defect after Caddy had
already reloaded: the first-run path had written but not loaded the validated
transaction variables. Direct Azure health stayed 200 and DNS remained on the
old origin. The script was corrected to load the transaction before its shared
verification/restore helpers, a regression assertion was added, and a full
rollback/re-stage drill then passed.

The first GCP bootstrap failed closed because the source validator did not yet
recognize GCE Debian's `mirror+file` indirection. HAProxy stayed disabled and no
ingress tag existed. The validator now accepts only the exact root-owned GCE
mirror manifest when its sole active entry is Debian's official HTTPS mirror;
the retry installed Debian HAProxy `2.6.12-1+deb12u3` and passed all local checks before
the public tag was attached.

The first frozen QA/review/Claude pass intentionally failed the release gate.
The accepted findings were repaired as follows:

- Every production Caddy mutator now refuses to run while the retained Taiwan
  listener transaction exists. Blue-green Caddy writes preserve the existing
  file-bind inode, and the listener transaction restores before deleting state
  after a write failure.
- Azure verification now requires host/container inode and SHA equality,
  structurally verifies the `:443` wrappers and production route in both
  adapted startup JSON and live admin JSON, compares their contract
  fingerprints, and rejects explicit HTTP-header proxy trust/XFF rewrites.
- HAProxy keeps a retained `STAGED`/`ROLLED_BACK` transaction, supports safe
  retry after package installation or rollback, and has a non-disruptive
  `update` phase. The live config was hardened by seamless reload, then the
  local verifier and public canary passed.
- HAProxy now uses Debian's unprivileged user/group and chroot; bootstrap die
  paths emit a failure marker, a completed bootstrap cannot later disable a
  healthy listener, Debian security mirror manifests are accepted under the
  same root-owned official-HTTPS policy, and the transport tests are wired
  into CI.
- A later audit found that the retained customer-Host and Taiwan listener
  transactions coexisted as a valid hash chain. The listener was rolled back
  to the customer transaction's exact `AFTER_SHA`, the customer transaction
  was verified and committed, and the listener was re-staged. The customer
  prepare path, Taiwan stage path, server release, blue-green helper, and
  runtime guard now enforce mutual exclusion; a live negative guard check and
  the direct-Azure/GCP canaries passed afterward.
- Every HTTP probe in the versioned transport verifier now uses
  `--noproxy '*'`, preventing an inherited proxy environment from bypassing
  the exact address pinned by `--resolve`.

## Deliberate blockers before DNS

1. Two active OpenAI OAuth accounts still have no fixed proxy binding. They
   must be bound through the authenticated admin API with compare-and-set
   semantics before Azure owns user traffic or background refresh work.
2. The running Azure application container still sees stale single-file
   traffic/background bind-mount inodes. The upgraded runtime guard correctly
   fails closed on this state and its timer is disabled. An exact-image
   blue-green recreation is required before enabling Azure as sole background
   owner. The old origin is already explicitly fenced to accept HTTP while its
   background owner remains standby; Azure is also standby until this gate is
   deliberately completed.
3. Authenticated basic generation, Responses continuation/streaming, and image
   canaries are still required. The authorized Windows real-client source was
   offline at this checkpoint and no task-scoped Vault grant was available;
   no credential was copied from a database, browser, or host file.
4. The production Cloudflare credential is not provisioned to the cutover
   controller. The retired POC token is explicitly out of scope. A logged-in
   browser change still requires action-time owner confirmation.
5. The first independent QA/review and requested Claude advisory review
   correctly returned blocked findings. Independent QA/review and Claude must
   re-run on the final frozen hashes after these repairs and documentation are
   frozen. The artifact directory also needs a durable Git revision before the
   review anchor can be called release-grade.

Until those blockers close, the correct state is: GCP and Azure transport stay
available for isolated canaries, the Azure Caddy transaction remains
rollback-capable, Azure runtime recovery stays disabled, and public DNS stays
on the old origin.
