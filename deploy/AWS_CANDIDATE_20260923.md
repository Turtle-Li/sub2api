# AWS Sub2API migration candidate (2026-09-23)

This is a staging host only. As of 2026-09-25, Azure `sub2api-candidate`
continues to serve production traffic and own background work. AWS accepts
operator-only origin probes while remaining `background=standby`; public DNS
has not changed. The old `sub2api-new` is not a fallback.
Historical GCP Taiwan ingress instructions in this repository are not current:
the API A record resolved directly to Azure `4.216.216.16` on 2026-09-23.
Cloud-resource deletion was not independently verified in this task.

| Item | Current candidate state |
| --- | --- |
| Registry host ID | `srv-aws-sub2api-candidate` |
| AWS account/region/AZ | `633841884781`, `ap-northeast-1`, `ap-northeast-1a` |
| Lightsail instance | `sub2api-aws-small-candidate-v2`, Ubuntu 24.04 x86_64 |
| Bundle | `small_3_0`: 2 vCPU, 2 GiB RAM, 60 GB disk, 3 TB monthly transfer, $12/month base |
| Static public IPv4 | `54.248.123.174` (`sub2api-aws-small-ip`) |
| SSH | `ubuntu:22`, Mac-owned public key `SHA256:ZNtRYxiEl8geAnff30YCs0lJlc1wi6sMahsFuFe4WwA`; v2 host ED25519 fingerprint `SHA256:j+7YLMWXvxqovDnB4sEYqtnkU8ETrcipmsxUFtH47aU`, verified against the Lightsail control plane |
| Public ingress | TCP 80/443 from the current operator IPv4 `115.195.32.146/32`; steady-state TCP 22 from that `/32`, Azure `4.216.216.16/32`, and the Lightsail browser-SSH alias. An AWS OIDC role may add only the current GitHub-hosted Runner IPv4 `/32` during an `aws-candidate` release and must remove it in an `always()` cleanup step. Database ports are not public. |
| Runtime directories | Root-owned `/opt/sub2api` and `/var/log/sub2api-release`, mode 0750; `secrets`, `db-host-ca`, and `staging` mode 0700 |
| Installed baseline | Docker 29.1.3, Compose 2.40.3, sysstat, unattended-upgrades; UFW default-deny incoming, allow outgoing |
| Release-control staging | Root-owned `/opt/sub2api/scripts` and mode-0600 `/etc/sub2api-autodeploy.env`; external dependency mode, `preserve-standby`, real-request probe enabled, loopback-pinned public health check, both release/recovery timers disabled and inactive |
| GitHub deployment | Environment `aws-candidate` owns distinct host, user, key and known-host secrets plus non-secret OIDC role, region and instance variables. Forced-command account `sub2api-github-deploy` accepts only the image-release protocol; deploy-key fingerprint `SHA256:im2yTlnEhikA+shKRt00rpAuBVhHTvXYwlH8d7nOOpc`; Vault item `86513fc6-74bb-47f5-8942-c91db01e0630`; OIDC role `GitHubSub2APIAWSCandidateDeploy` trusts only `repo:Turtle-Li/sub2api:environment:aws-candidate`. |
| Application release | Fork `main` commit `a9f26360fcacf48a30da0ba67d4e5a1ac8c629fb`, version `0.2.8`, active slot `sub2api-green`, healthy with zero restarts/OOM |
| Proxy | AWS-specific Caddy route for API and www; the operator-only origin passed HTTPS health, auth-boundary, public settings, homepage, help, and HTTP-to-HTTPS redirect probes |
| Local Docker state | Healthy application, Caddy, payment Vault Agent, and Feishu Vault Agent containers with project network and separate named volumes |
| Public www assets | Six regular files in the Caddy data volume at `/data/sub2-web/{home,help}`, copied from the serving Azure Caddy volume and SHA-256 matched file by file |

SSH effective settings were verified as `PubkeyAuthentication yes`,
`PasswordAuthentication no`, `KbdInteractiveAuthentication no`, and
`PermitRootLogin no`. The imported Lightsail key pair selects the public key;
no private key was exported from AWS. The local private key remains device-local
and is not a project artifact. Its Vault reconciliation is pending; no
`vault_ref` has been invented.

The Lightsail firewall admits TCP 80/443 only from the operator's current IPv4
`115.195.32.146/32`. Steady-state TCP 22 additionally admits Azure
`4.216.216.16/32` and the `lightsail-connect` console alias. For an explicitly
dispatched `aws-candidate` release, GitHub OIDC obtains a short-lived AWS role,
discovers the Runner's public IPv4, validates it as a global IPv4 address, adds
only that `/32`, and removes the same `/32` in an `always()` cleanup step. Host
UFW admits IPv4 TCP 22 generally because Lightsail is the source-address gate;
it does not admit IPv6 SSH. UFW continues to mirror the operator-only HTTP/HTTPS
boundary. The operator address is temporary and must be revalidated before
future access; never widen the Lightsail candidate origin to `0.0.0.0/0` before
an approved cutover.

The repository's reviewed deployment tree bootstrapped the first application
slot and then completed a verified blue-green release. The final release log is
`/var/log/sub2api-release/gha-20260924-213221-a9f26360-62626`: health passed,
authenticated `/v1/models` and `/v1/responses` returned 200 using
`gpt-5.6-sol`, and Caddy switched only to `sub2api-green:8080`. PostgreSQL 5432
and Redis 6379 TCP connectivity from the application container both pass. AWS
remains `traffic=accepting background=standby`; both timers remain disabled and
inactive. Do not run `sub2api-autodeploy.sh --check` as a purported no-write
host preflight: it fetches Git refs and creates a server-side worktree cache.

A Lightsail `StatusCheckFailed` alarm is present and currently has notifications
disabled because this account has no verified Lightsail contact method. The
account has an existing $30 monthly cost budget; it is account-wide, not a
candidate-specific traffic cap. One manual 60 GB instance snapshot,
`sub2api-aws-base-20260923`, reached `available`; it predates the public www
assets and is only a base-system recovery point, **not** an application,
database, or offsite backup. Snapshot storage is chargeable. No recurring
snapshot, application backup, verified restore, or offsite copy exists yet;
do not enable chargeable retention without reviewing its scope and cost.

The bootstrap proxy is staged from [`aws-candidate/compose.bootstrap.yml`](aws-candidate/compose.bootstrap.yml)
and [`aws-candidate/Caddyfile.bootstrap`](aws-candidate/Caddyfile.bootstrap).
Its host-network loopback listener is deliberately isolated from the future
application network and must be replaced under the canonical maintenance lock
by a reviewed API/www production configuration. Neither public 80/443 nor
DNS was changed. The source Azure Caddyfile has an old GCP PROXY-protocol
listener and external API certificate bind: do not copy it unchanged to AWS.
The six copied www files exclude the unrecovered historical DMG download.

Monitoring and recovery are **not** complete: sysstat and an AWS status alarm
without verified notification delivery are not sufficient cutover coverage.
The application, payment, Feishu, and database injection paths are active, but
the GitHub deploy identity, candidate-specific backup/restore drill, certificate
renewal automation, and owner-notified alert path remain separate gates.

## Bandwidth probe

Short single-connection IPv4 POSTs from the candidate to Cloudflare's speed
endpoint returned HTTP 200. Reported upload rates were 10.3 MB/s for 16 MiB,
44.1 MB/s for 32 MiB, 88.0 MB/s for 64 MiB, and 27.3 MB/s for 128 MiB.
The `ens5` transmit counter and Lightsail `NetworkOut` metric both advanced
by approximately 264 MB during the probe window. These are destination- and
burst-dependent observations, not a sustained throughput guarantee. No `tc`
rate limiter was configured on `ens5`. The candidate was not limited to a
fixed 16 MB/s in this test.

## Remaining migration gates

- Reconcile the SSH and GitHub deploy identities in Vault; replace the temporary
  operator-address firewall rule when its address changes.
- Establish candidate-only backups, verified notification delivery, rollback
  image, certificate renewal automation, and a restore drill.
- Trigger the repository workflow against `aws-candidate` after the deployment
  changes reach `main`, and retain the successful run as end-to-end evidence.
  Keep the separate `azure-production` Environment and its secrets unchanged.
- Preserve the exact database allowlist at `54.248.123.174` and remove it only
  after rollback/cutover decisions. Do not rerun the September 12 currency
  conversion or restore an old full DB over new writes.
- Verify 2 GiB memory headroom with the real image and background workload,
  sustained bandwidth under representative concurrency, DNS, TLS, www static
  assets, and public API smoke before any production cutover.

No production DNS or Azure runtime ownership was changed. The database host now
allows the exact AWS source for the tested candidate only.
