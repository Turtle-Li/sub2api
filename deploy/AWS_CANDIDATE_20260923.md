# AWS Sub2API migration candidate (2026-09-23)

This is a staging host only. Azure `sub2api-candidate` continues to serve the
application and own background work. The old `sub2api-new` is not a fallback.
Historical GCP Taiwan ingress instructions in this repository are not current:
the API A record resolved directly to Azure `4.216.216.16` on 2026-09-23.
Cloud-resource deletion was not independently verified in this task.

| Item | Current candidate state |
| --- | --- |
| Registry host ID | `srv-aws-sub2api-candidate` |
| AWS account/region/AZ | `633841884781`, `ap-northeast-1`, `ap-northeast-1a` |
| Lightsail instance | `sub2api-aws-small-candidate`, Ubuntu 24.04 x86_64 |
| Bundle | `small_3_0`: 2 vCPU, 2 GiB RAM, 60 GB disk, 3 TB monthly transfer, $12/month base |
| Static public IPv4 | `54.248.123.174` (`sub2api-aws-small-ip`) |
| SSH | `ubuntu:22`, Mac-owned public key `SHA256:ZNtRYxiEl8geAnff30YCs0lJlc1wi6sMahsFuFe4WwA`; host ED25519 fingerprint `SHA256:pzaFT0Kylal6P5nKsQtoaoTvgORxIxb1BxDMxposYGk` |
| Public ingress | TCP 22 from the temporary operator IPv4 `/32` and Lightsail browser-SSH alias only; no public HTTP/HTTPS or database ports |
| Runtime directories | Root-owned `/opt/sub2api` and `/var/log/sub2api-release`, mode 0750; `secrets`, `db-host-ca`, and `staging` mode 0700; no application containers |
| Installed baseline | Docker 29.1.3, Compose 2.40.3, sysstat, unattended-upgrades; UFW default-deny incoming, allow outgoing |
| Release-control staging | Root-owned `/opt/sub2api/scripts` and mode-0600 `/etc/sub2api-autodeploy.env`; external dependency mode, candidate-pinned health resolve, both release/recovery timers disabled and inactive |

SSH effective settings were verified as `PubkeyAuthentication yes`,
`PasswordAuthentication no`, `KbdInteractiveAuthentication no`, and
`PermitRootLogin no`. The imported Lightsail key pair selects the public key;
no private key was exported from AWS. The local private key remains device-local
and is not a project artifact. Its Vault reconciliation is pending; no
`vault_ref` has been invented.

The Lightsail firewall admits TCP 22 only from the operator's current IPv4
`125.120.233.128/32` and the `lightsail-connect` console alias. Host UFW
additionally permits TCP 22 only from that IPv4 and denies other inbound
traffic. The console alias is therefore not independently sufficient to pass
host UFW; retain tested Mac access before changing either layer. The operator
address is temporary and must be revalidated before future access.

The repository's exact-commit `deploy/` tree was staged root-owned on the
candidate. `install-autodeploy.sh` installed the blue-green helper and control
units with `--dependency-mode external`, `--no-enable-runtime-guard`, and
`--no-enable`. Both timers were confirmed disabled/inactive; Docker has zero
application containers. The configured external runtime file and CA path are
references only: neither file nor any database credential has been provisioned.
The generated release configuration contains a default `activate` value, but
no image receiver or release may be invoked before the migration gates in
[`AWS_MIGRATION_RUNBOOK.md`](AWS_MIGRATION_RUNBOOK.md) are passed. Do not run
`sub2api-autodeploy.sh --check` as a purported no-write host preflight: it
fetches Git refs and creates a server-side worktree cache.

A Lightsail `StatusCheckFailed` alarm is present and currently has notifications
disabled because this account has no verified Lightsail contact method. The
account has an existing $30 monthly cost budget; it is account-wide, not a
candidate-specific traffic cap. No automated instance snapshot or offsite
backup is configured yet. Do not enable chargeable snapshot retention without
reviewing the backup scope and cost.

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

- Reconcile the SSH identity in Vault and configure a stable SSH alias;
  replace the temporary operator-address firewall rule when its address changes.
- Establish candidate-only backups, notification delivery, rollback image,
  and the restricted GitHub receiver under the canonical maintenance-lock
  contract. An inert control-script install is already complete.
- Restore compatible application configuration and its credential agents
  through the reviewed Vault injection flow, never by copying raw secrets.
- Decide the PostgreSQL/Redis location; authorize only exact candidate network
  sources after a backup and isolated restore test. Do not rerun the completed
  September 12 currency conversion or restore an old full DB over new writes.
- Verify 2 GiB memory headroom with the real image and background workload,
  sustained bandwidth under representative concurrency, DNS, TLS, www static
  assets, and public API smoke before any production cutover.

No production DNS, Azure runtime, database firewall, or customer traffic was
changed when preparing this candidate.
