# Sub2API: New AWS host and migration runbook

This document governs a future first deployment, validation, and authorized
cutover. It is **not** authorization to change production DNS, the database
allowlist, payment writers, or background ownership. Current host facts and
what is already installed: [AWS_CANDIDATE_20260923.md](AWS_CANDIDATE_20260923.md).
For the release and runtime contracts read [README.md](README.md), the project
`AGENTS.md`, and the latest dated production operation records. Historical
GCP/Taiwan and expired `sub2api-new` instructions are not rollback targets.
The standalone anti-degradation harvesters and current external monitoring
solution are intentionally not migrated. The default-off native
anti-degradation configuration must remain off during migration; any future
monitoring or mitigation belongs inside Sub2API under a separately reviewed
design.

## 1. New host prerequisites

1. Resolve the current production topology from project docs and the private
   Registry. Confirm the active Azure image revision, background owner, public
   API/www routes, database location, live backup state, and deployment agents.
   Keep existing production traffic and rollback capacity unchanged.
2. Use the approved AWS account/region and explicit Lightsail bundle; verify
   the price, monthly transfer allowance, credits, budget and any regional
   allowance adjustment in the account. Create a project-isolated instance
   with a per-device public key at launch and attach a static IP. Record the
   instance ID, region, AZ, IP, host fingerprint, and owner in the Registry.
   Never export an AWS-generated private key as an access shortcut.
3. Admit only the operator's current `/32` on TCP 22 at the Lightsail firewall
   and host firewall. Verify public-key login and effective `sshd -T` values:
   password/keyboard-interactive/root login disabled. Confirm a second login
   works before closing any access path. A changed operator IP needs a
   separately verified firewall update; never open SSH to the world.
4. Install OS security updates, Docker/Compose, sysstat and unattended
   upgrades. Keep project directories root-owned and private. Establish
   monitored backup and restore capability before storing application state;
   review snapshot storage cost and offsite retention. External standalone
   monitoring migration is waived by owner direction for this cutover. Record
   the explicit operator observation checklist and rollback stop conditions; do
   not claim that a disabled or unverified alarm will notify the owner.
5. Size the instance against the real image: two GiB RAM and 60 GB disk are
   assumptions, not proof of application headroom. Measure memory, swap,
   Docker storage, CPU burst balance, and sustained network transfer under a
   representative concurrent workload. Upgrade the bundle if the measured
   workload cannot safely fit; do not weaken health or drain gates.

## 2. Prepare release control, not the application

Use an exact reviewed fork `main` revision as the source of deployment scripts.
Stage the `deploy/` tree in a root-owned, non-group-writable directory and
verify its digest. The candidate already has these scripts installed with
the following inert configuration:

```text
SUB2API_APP_DIR=/opt/sub2api
SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL=https://github.com/Turtle-Li/sub2api.git
SUB2API_AUTODEPLOY_PRODUCTION_BRANCH=main
SUB2API_PUBLIC_HEALTH_RESOLVE=api.turtleligpt.com:443:127.0.0.1
SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=external
SUB2API_EXTERNAL_RUNTIME_ENV_FILE=/etc/sub2api-external-runtime.env
SUB2API_EXTERNAL_CA_FILE=/opt/sub2api/db-host-ca/ca.crt
SUB2API_RELEASE_BACKGROUND_MODE=preserve-standby
SUB2API_RELEASE_REAL_REQUEST_PROBE_ENABLED=true
```

The installer invocation used `--install-blue-green-helper`,
`--no-enable-runtime-guard`, and `--no-enable`. Before any later installation,
check the canonical maintenance-lock contract and existing config; never
replace a live config merely to rerun an installer. A `--replace-config` run
rewrites the generated file and does **not** reconstruct the unified-payment
managed block. Back up and reapply the complete managed block, including both
markers and all project-defined keys, before allowing a new slot to start; a
volume name alone is not sufficient. Re-verify `preserve-standby`, the real
request probe, and the loopback health resolve after every replacement. Both
timers must remain `disabled` and `inactive` until cutover is separately
approved.

### Deployment checkpoint (2026-09-25)

- Fork `main` commit `32eeb9e2bc38e7d7874d50e33f11d2691db0790a`
  (`0.2.8`) is healthy on `sub2api-blue`; Caddy targets only that slot.
- The final release passed authenticated model-list and tiny Responses probes
  using `gpt-5.6-sol`; application/Caddy fatal and 5xx gates were clear.
- Operator-only pinned-IP checks passed health, unauthenticated `/v1/models`
  (401), `/api/v1/settings/public`, homepage, help, redirect, and TLS identity.
- DNS-only `aws-test.turtleligpt.com` now resolves to `54.248.123.174`.
  Lightsail and UFW expose public TCP 80/443 for the automatic-TLS rehearsal;
  production `api` and `www` DNS remain unchanged. Caddy loaded the tracked
  active-slot file with SHA-256
  `0947c585dbe57f9ad127dee5d3d12832ce058ea0f75f68cd64d3b601af40013f`.
- Let's Encrypt HTTP-01 validation completed on 2026-09-25 and issued a
  certificate for only `aws-test.turtleligpt.com`, valid from
  `2026-09-25 03:59:10 UTC` through `2026-12-24 03:59:09 UTC`, with leaf
  SHA-256 fingerprint
  `02:EA:89:BE:62:3C:C4:47:25:D6:18:5B:55:F7:12:75:31:DA:EC:F4:E2:12:18:4D:60:83:61:4A:56:73:24:22`.
  OpenSSL hostname/chain verification returned `Verify return code: 0`.
- Public HTTPS canaries through `aws-test` passed health, auth boundary, public
  settings and homepage. The protected release key then passed a 27-model
  authenticated list, non-streaming Responses, completed SSE, one low-quality
  synchronous `gpt-image-1` generation and one asynchronous generation whose
  result was stored and returned by URL. No application/Caddy 5xx, fatal, OOM
  or container restart was observed in the probe window.
- PostgreSQL 5432 and Redis 6379 connectivity from the application container
  passed after adding exact source `54.248.123.174` to the data-host allowlist.
- AWS remains `traffic=accepting background=standby`; Azure remains the live
  production host. DNS and background ownership are unchanged.
- A five-minute authenticated `/v1/models` soak completed 1,500/1,500 HTTP 200
  responses with P95 90.6 ms, zero restarts/errors, application CPU below 3.7%
  and at least 1,240 MiB host memory available. A bounded 1 GiB sustained upload
  completed 16/16 HTTP 200 transfers in 68.254 seconds at 15.753 MiB/s average
  `ens5` transmit throughput, with no swap, restart, OOM or log error. These
  tests cover standby request admission and egress; repeat observation with AWS
  as the sole background owner before DNS change.
- Automatic database archive `sub2api-db-backup-20260925-121659.tar.gz` passed
  its outer and internal PostgreSQL/Redis checksums. An isolated restore passed
  `pg_amcheck`, schema fingerprint
  `060cb9c360c8dfa99c774e6a4177797e`, and 1,825 live Redis keys. The remaining
  backup gate is the restricted NAS offsite copy and restore; its target-specific
  Vault/SSH identity does not yet exist, so the guarded sync timer stays disabled.
- A read-only inventory found nine active parent accounts on proxies 6, 7, 40,
  41 and 44. No binding changed. Use only the authenticated bulk-update CAS with
  recorded reverse-CAS before Azure relay retirement.
- The restricted GitHub receiver is installed with a distinct Vault-backed key
  and `aws-candidate` GitHub Environment. Its OIDC role is restricted to that
  repository environment and may only manage Lightsail public-port state. The
  workflow opens the current Runner IPv4 `/32` immediately before restricted
  SSH and removes it in `always()` cleanup. The current evidence is successful
  fix release run `36101027759` for exact `main` commit
  `32eeb9e2bc38e7d7874d50e33f11d2691db0790a`; release log
  `/var/log/sub2api-release/gha-20260925-060932-32eeb9e2-379538/` is the host
  record. Earlier end-to-end candidate evidence remains run `36070650495` and
  `/var/log/sub2api-release/gha-20260924-231200-557d5c07-*`.
  Initial certificate issuance, isolated database restore, standby request-load
  headroom and bounded sustained egress evidence are complete. Offsite backup,
  background-active observation and later certificate renewal remain open gates.
  External alert delivery is not a gate under the owner's monitoring exclusion;
  the cutover still requires active operator observation and stop conditions.

## 3. First application deployment: separate approval gate

1. Decide whether PostgreSQL/Redis remain on the dedicated Tokyo data host
   or move with their own backup/restore plan. Take a fresh, verified backup
   and perform an isolated restore. Establish an offsite copy and a tested
   recovery point. Never restore a pre-cutover full database over later
   financial writes or rerun the September 12 currency conversion.
2. Review the latest exact database host firewall, PostgreSQL HBA, Redis TLS,
   and source-address boundary. Only after a reviewed rollback plan, authorize
   the candidate's exact source for a bounded test. Verify egress address and
   TLS hostname/CA, then inject the external runtime file as root-owned 0600
   and the CA through the approved Vault/host mechanism. No secrets enter Git,
   terminal output, shell history, or an unprotected transfer archive.
3. Stage the exact production image, application config, protected agent
   mounts, network and data volumes. Preserve payment/signing identities and
   immutable financial records. Reconcile migrations and image compatibility
   before first process startup: startup may run forward SQL migrations and
   background jobs. Build a reviewed **first-slot bootstrap** procedure that
   starts only a fenced, non-serving generation with background work disabled;
   the normal blue-green receiver is not a substitute. If isolation or
   migration semantics cannot be proven, stop here.
4. Reconstruct the *actual* API and www Caddy routes, static assets, and
   certificate plan from the serving host and its recovery record. Neither
   repository Caddy example is a drop-in production config. Keep public
   HTTP/HTTPS closed until pinned-host TLS, challenge reachability, route
   verification, trusted client IP, and www downloads have passed. Use the
   canonical maintenance lock for Caddy/container lifecycle operations. The
   candidate's `aws-candidate/compose.bootstrap.yml` is a loopback 503 stub,
   **not** the eventual production Caddy. Preserve its separate data/config
   volumes and copied `/data/sub2-web` public files when replacing it; do not
   import the Azure GCP PROXY listener, live private key, or active Caddy
   certificate state by blind copying. Ensure the API certificate renewal and
   www ACME challenge both work on the AWS route before DNS cutover.
5. Preserve the candidate-only release image/rollback point and restricted
   GitHub Actions SSH receiver with its distinct Vault-managed Ed25519 key.
   The receiver must remain forced-command-only with no PTY, forwarding, user
   rc or shell path. Install the unprivileged forced-command parser under
   root-owned `/usr/local/libexec`; do not weaken the 0750 application root just
   to make the SSH entrypoint executable. The `aws-candidate` workflow must use its environment-bound
   OIDC role to add only the current Runner IPv4 `/32` to Lightsail TCP 22 and
   remove that exact rule in `always()` cleanup; never statically allow all
   GitHub Actions ranges or cloud SSH `0.0.0.0/0`. Host UFW may admit IPv4 SSH
   generally only because Lightsail remains the exact source gate; do not add an
   IPv6 SSH allow rule. Prove the complete `main` workflow against the separate
   `aws-candidate` Environment; do not replace `azure-production` secrets.
6. Do not migrate Komari, the standalone anti-degradation harvesters or other
   unstable external monitoring components. Preserve host metrics, application
   logs, runtime guards and the AWS status alarm for the migration window. Back
   up candidate-local config, Docker volumes, certificates and the release
   image with off-host retention and a restore drill. The base-system snapshot
   `sub2api-aws-base-20260923` does not cover these later changes; the production
   PostgreSQL/Redis backup remains owned by the data host.

## 4. Acceptance before traffic

- Confirm image revision/labels, health, Caddy's host/startup/Admin agreement,
  Docker restart/OOM counts, migration ledger and all dependency TLS paths.
- Run pinned-IP API and www checks without changing public DNS. Include
  authenticated model-list and tiny real Responses probes with bounded cost,
  SSE/WS, login, synchronous and asynchronous image generation, Batch Image,
  purchase callback safety, assets, and payment-agent health. Maintain
  candidate background standby and block unintended financial jobs.
- Create a DNS-only `aws-test.turtleligpt.com` A record pointing to the AWS
  static IP. The test Caddy host must omit imported certificates so automatic
  ACME issuance is exercised. Before loading it, open a bounded public TCP
  80/443 window in both Lightsail and UFW because the current operator-only
  rules block HTTP-01 and TLS-ALPN-01. Run the same authenticated smoke suite
  through that hostname before editing the production records. Restore the
  reviewed steady-state ingress boundary after the rehearsal/cutover decision.
  Observe automatic renewal later; initial issuance alone is not renewal proof.
- Verify unified-payment live configuration, payment Vault sidecar health,
  refund rollback readiness, invalid-signature rejection and one owner-approved
  1-2 fen checkout after recent administrator TOTP step-up. Do not synthesize
  a successful payment or bypass the provider callback.
- Verify ordinary and asynchronous image generation plus one low-cost Gemini
  Batch Image job. If the release-probe group has Batch Image disabled, create
  a temporary enabled Gemini-group key through the normal admin contract, run
  the canary and delete the key. Do not enable Batch Image globally merely to
  make the probe pass. For a legacy/non-COS result, require `result-files` to
  return `BATCH_IMAGE_RESULT_ARCHIVE_UNAVAILABLE` and verify the authenticated
  `/download` ZIP fallback. For a COS-archive result, never fall back after
  delivery configuration, CORS, signature, network or integrity errors.
- Verify public ingress controls, logs, database backup/restore and current
  recovery point. Compare 2 GiB memory plus swap and sustained/concurrent
  egress to measured production demand. A short single-upload burst is
  insufficient evidence. External monitoring enrollment is not required.
- Before Azure retirement, replace or clear every database proxy binding. A
  single AWS node cannot preserve both Tokyo and US-West fixed egress. Use at
  least one isolated Tokyo node and one isolated US-West node; use a third
  US-West node if the current independent OAuth backup fault domain must be
  preserved. Migrate parent accounts and credential shadows only through the
  authenticated CAS operation with recorded old/new proxy IDs and a reverse-CAS
  rollback. Never use raw SQL or cross-region automatic fallback.
  The current read-only set is proxy 6 -> accounts 9/15/54/55/69, proxy 7 ->
  account 6, proxy 40 -> account 56, proxy 41 -> account 59 and proxy 44 ->
  account 60. Call `POST /api/v1/admin/accounts/bulk-update` with only
  `account_ids`, `proxy_id` and `expected_proxy_id`; a clear uses new proxy `0`
  and a rollback expects `0`. Any expected-value mismatch must stop the batch.
- Obtain independent review and owner approval for the exact release image,
  data boundary, DNS changes, maintenance window, stop conditions and rollback.

## 5. Cutover and rollback

1. Complete the Azure proxy replacement or remove the affected account
   bindings, then rerun text/SSE/WS/OAuth refresh probes without any Azure
   proxy dependency. Keep the old proxy records and nodes intact for reverse-CAS
   rollback throughout the observation window.
2. Under the canonical lock and documented node-state protocol, fence new
   background claims on Azure and prove zero old claims before assigning the
   sole owner to AWS. Preserve request-triggered refresh semantics and the
   financial refund-readiness gate. Do not run two active queue consumers.
3. Change only the approved API/www DNS targets after the automatic-TLS test
   host and Cloudflare API dry run pass. Observe real traffic
   through at least two effective TTLs and compare error rate, latency,
   WebSocket duration, payment callbacks, database connections, memory and
   network usage. Keep Azure running as the verified same-data rollback host.
4. On a stop condition, drain admissions and verify refund/readiness fences,
   then return DNS/proxy and background ownership using the existing
   lock-owning transaction recovery. Do not manually stop containers, delete
   Caddy transactions, restore stale full DB, or select expired `sub2api-new`.
5. Only after a stable observation window, fresh backup/restore evidence and
   confirmation that DNS no longer resolves to `4.216.216.16`, reconcile
   obsolete DB sources and retire Azure. Delete the six owner-approved Azure
   resource groups (`sub2_group`, `jp_group`, `westus_group`, `jp2_group`,
   `westus2_relay_group`, `westus3_relay_group`) and verify that no VM, disk,
   NIC, public IPv4 or billable attachment remains. Preserve `NetworkWatcherRG`
   unless separately approved. Update project and Registry topology records
   together. The first three groups belong to Azure subscription
   `6835deb1-678b-4067-b516-b57f80e14e25`; the final three belong to
   `65c9db87-f353-427d-80cc-af2953c8761b`. Inventory and delete in each explicit
   subscription instead of relying on the CLI default. `sub2_group` is the
   application origin; the other five groups are independent relay workloads.
   Before deleting any relay group, complete a separate client dependency
   inventory covering non-Sub2 consumers as well as the nine known account
   bindings, and either migrate or explicitly retire each consumer.

## New-host checklist and current status

The candidate has a static IP, imported public key, key-only SSH, host/cloud
firewalls, OS/Docker baseline, root-owned private paths, release-control
scripts and disabled timers. It has separate Docker volumes/network, the
AWS-specific Caddy route, copied public www assets, protected external-runtime
configuration, payment/Feishu Vault Agents and a healthy exact-commit
application release. PostgreSQL/Redis connectivity, operator origin checks,
the restricted GitHub workflow and local pinned-IP authenticated models and
Responses requests have passed. Synchronous and asynchronous image generation,
payment configuration/readiness and invalid webhook rejection have also passed.
The enabled-group Gemini Batch Image canary has completed through the AWS test
hostname, produced valid PNGs, settled correctly, verified the legacy
`result-files` error and authenticated server-ZIP fallback, and had both
temporary keys tombstoned with immediate 401 verification. The owner-approved
1-2 fen live checkout is intentionally deferred until the configured production
`www` result origin points to AWS. The automatic data-host backup, isolated
restore, standby request-load headroom and bounded sustained network evidence
now pass. The candidate still lacks the restricted offsite NAS copy/restore,
Azure proxy replacement, background-active observation and the controlled
background-owner handoff.
Initial automatic TLS issuance on the test hostname has passed; later renewal
observation remains separate evidence. Azure remains production and DNS is
unchanged. Do not label it cutover-ready until those Section 4 gates pass. Also
retire the obsolete running Lightsail instance
`sub2api-aws-small-candidate` after confirming its snapshot dependency, because
it is separate from the active `sub2api-aws-small-candidate-v2` candidate.
