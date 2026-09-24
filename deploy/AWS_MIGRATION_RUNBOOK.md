# Sub2API: New AWS host and migration runbook

This document governs a future first deployment, validation, and authorized
cutover. It is **not** authorization to change production DNS, the database
allowlist, payment writers, or background ownership. Current host facts and
what is already installed: [AWS_CANDIDATE_20260923.md](AWS_CANDIDATE_20260923.md).
For the release and runtime contracts read [README.md](README.md), the project
`AGENTS.md`, and the latest dated production operation records. Historical
GCP/Taiwan and expired `sub2api-new` instructions are not rollback targets.

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
   review snapshot storage cost and offsite retention. Verify monitoring
   notifications actually reach an owner before treating an alarm as coverage.
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

- Fork `main` commit `38835f5b9d031fab5331238178cf10f91ea7dc30`
  (`0.2.8`) is healthy on `sub2api-blue`; Caddy targets only that slot.
- The final release passed authenticated model-list and tiny Responses probes
  using `gpt-5.6-sol`; application/Caddy fatal and 5xx gates were clear.
- Operator-only pinned-IP checks passed health, unauthenticated `/v1/models`
  (401), `/api/v1/settings/public`, homepage, help, redirect, and TLS identity.
- PostgreSQL 5432 and Redis 6379 connectivity from the application container
  passed after adding exact source `54.248.123.174` to the data-host allowlist.
- AWS remains `traffic=accepting background=standby`; Azure remains the live
  production host. DNS and background ownership are unchanged.
- The restricted GitHub receiver is installed with a distinct Vault-backed key
  and `aws-candidate` GitHub Environment. Its OIDC role is restricted to that
  repository environment and may only manage Lightsail public-port state. The
  workflow opens the current Runner IPv4 `/32` immediately before restricted
  SSH and removes it in `always()` cleanup. Run `36067946246` completed the
  exact-`main` build, restricted upload, blue-green release and cleanup; release
  log `/var/log/sub2api-release/gha-20260924-224053-38835f5b-106998` is the host
  evidence. Certificate renewal automation, alert delivery, backup/restore
  drill, and load/headroom evidence remain open gates.

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
6. Enroll the candidate in Komari using an independent Vault-backed token
   and the pinned Agent described in the `infra-monitoring` project. Test a
   delivered notification; the present Lightsail status alarm has notifications
   disabled. Back up candidate-local config, Docker volumes, certificates and
   release image with off-host retention and a restore drill. The base-system
   snapshot `sub2api-aws-base-20260923` does not cover these later changes;
   the production PostgreSQL/Redis backup remains owned by the data host.

## 4. Acceptance before traffic

- Confirm image revision/labels, health, Caddy's host/startup/Admin agreement,
  Docker restart/OOM counts, migration ledger and all dependency TLS paths.
- Run pinned-IP API and www checks without changing public DNS. Include
  authenticated model-list and tiny real Responses probes with bounded cost,
  SSE/WS, login, purchase callback safety, assets, and payment-agent health.
  Maintain candidate background standby and block unintended financial jobs.
- Verify public ingress controls, logs, monitoring notification delivery,
  database backup/restore and current recovery point. Compare 2 GiB memory
  and sustained/concurrent egress to measured production demand. A short
  single-upload burst is insufficient evidence.
- Obtain independent review and owner approval for the exact release image,
  data boundary, DNS changes, maintenance window, stop conditions and rollback.

## 5. Cutover and rollback

1. Under the canonical lock and documented node-state protocol, fence new
   background claims on Azure and prove zero old claims before assigning the
   sole owner to AWS. Preserve request-triggered refresh semantics and the
   financial refund-readiness gate. Do not run two active queue consumers.
2. Change only the approved API/www DNS or proxy targets. Observe real traffic
   through at least two effective TTLs and compare error rate, latency,
   WebSocket duration, payment callbacks, database connections, memory and
   network usage. Keep Azure running as the verified same-data rollback host.
3. On a stop condition, drain admissions and verify refund/readiness fences,
   then return DNS/proxy and background ownership using the existing
   lock-owning transaction recovery. Do not manually stop containers, delete
   Caddy transactions, restore stale full DB, or select expired `sub2api-new`.
4. Only after a stable observation window, reconcile obsolete DB sources,
   renew backup and monitoring ownership, and retire Azure in a separate
   approved task. Update project and Registry topology records together.

## New-host checklist and current status

The candidate has a static IP, imported public key, key-only SSH, host/cloud
firewalls, OS/Docker baseline, root-owned private paths, release-control
scripts and disabled timers. It has separate Docker volumes/network, the
AWS-specific Caddy route, copied public www assets, protected external-runtime
configuration, payment/Feishu Vault Agents and a healthy exact-commit
application release. PostgreSQL/Redis connectivity, operator origin checks,
the restricted GitHub workflow and local pinned-IP authenticated models and
Responses requests have passed. It still lacks current candidate/offsite
backup plus restore evidence, verified alert delivery, certificate renewal
automation, sustained 2 GiB memory/load/network headroom evidence and the
controlled background-owner handoff. Azure remains production and DNS is
unchanged. Do not label it cutover-ready until those Section 4 gates pass.
