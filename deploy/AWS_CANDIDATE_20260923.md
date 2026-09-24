# AWS Sub2API migration candidate (2026-09-23)

This is a staging host only. As of 2026-09-25, Azure `sub2api-candidate`
continues to serve production traffic and own background work. AWS accepts
operator-only origin probes while remaining `background=standby`; public DNS
has not changed. The old `sub2api-new` is not a fallback.
Historical GCP Taiwan ingress instructions in this repository are not current:
the API A record resolved directly to Azure `4.216.216.16` on 2026-09-23.
Cloud-resource inventory was rechecked on 2026-09-25 across both Azure
subscriptions and AWS Lightsail. No Azure resource was deleted in this task.

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
| GitHub deployment | Environment `aws-candidate` owns distinct host, user, key and known-host secrets plus non-secret OIDC role, region and instance variables. Forced-command account `sub2api-github-deploy` accepts only the image-release protocol through root-owned `/usr/local/libexec/sub2api-github-deploy-trigger`; the application root remains 0750. Deploy-key fingerprint `SHA256:im2yTlnEhikA+shKRt00rpAuBVhHTvXYwlH8d7nOOpc`; Vault item `86513fc6-74bb-47f5-8942-c91db01e0630`; OIDC role `GitHubSub2APIAWSCandidateDeploy` trusts only `repo:Turtle-Li/sub2api:environment:aws-candidate`. |
| Application release | Fork `main` commit `557d5c079a025f6488c12904137e238734a8c5ed`, version `0.2.8`, active slot `sub2api-green`, healthy with zero restarts/OOM; 2 GiB persistent swap is enabled with swappiness 10 |
| Proxy | AWS-specific Caddy route for API and www; the operator-only origin passed HTTPS health, auth-boundary, public settings, homepage, help, and HTTP-to-HTTPS redirect probes |
| Local Docker state | Healthy application, Caddy, payment Vault Agent, and Feishu Vault Agent containers with project network and separate named volumes |
| Public www assets | Six regular files in the Caddy data volume at `/data/sub2-web/{home,help}`, copied from the serving Azure Caddy volume and SHA-256 matched file by file |
| Automatic-TLS rehearsal | The rendered active-slot file is staged root-only at `/opt/sub2api/Caddyfile.aws-test.staged` with SHA-256 `0947c585dbe57f9ad127dee5d3d12832ce058ea0f75f68cd64d3b601af40013f`; Caddy 2.11 validation passed without reload. `aws-test.turtleligpt.com` still has no DNS record, so ACME issuance has not started. Initial issuance is the pre-cutover gate; later renewal must be observed separately. |

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
slot and then completed verified blue-green releases. GitHub Actions run
`36070650495` built and deployed exact `main` commit
`557d5c079a025f6488c12904137e238734a8c5ed`; its OIDC step opened only the
Runner IPv4 `/32`, the restricted SSH receiver accepted the image, and the
`always()` cleanup restored the steady-state operator/Azure/browser-console SSH
rules. The final release log is
`/var/log/sub2api-release/gha-20260924-231200-557d5c07-*`: health passed,
authenticated `/v1/models` and `/v1/responses` returned 200 using
`gpt-5.6-sol`, and Caddy switched only to `sub2api-green:8080`. A separate
local Mac probe pinned `api.turtleligpt.com` and `www.turtleligpt.com` to
`54.248.123.174`: three API health probes completed in 0.124-0.141 seconds,
the homepage returned 200, and the imported API certificate validated.
PostgreSQL 5432 and Redis 6379 TCP connectivity from the application container
both pass. AWS remains
`traffic=accepting background=standby`; both timers remain disabled and
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
The original `sub2api-aws-small-candidate` instance is also still running at
ephemeral IPv4 `13.115.60.21` without the migration static IP. It is not the
active candidate and continues to incur the `small_3_0` base charge. Confirm
that its only retained dependency is the chargeable base snapshot, then delete
the obsolete instance and separately decide whether to retain that snapshot.

The bootstrap proxy is staged from [`aws-candidate/compose.bootstrap.yml`](aws-candidate/compose.bootstrap.yml)
and [`aws-candidate/Caddyfile.bootstrap`](aws-candidate/Caddyfile.bootstrap).
Its host-network loopback listener is deliberately isolated from the future
application network and must be replaced under the canonical maintenance lock
by a reviewed API/www production configuration. Neither public 80/443 nor
DNS was changed. The source Azure Caddyfile has an old GCP PROXY-protocol
listener and external API certificate bind: do not copy it unchanged to AWS.
The six copied www files exclude the unrecovered historical DMG download.

External anti-degradation harvesters and the current standalone monitoring
solution are deliberately outside this migration. The unstable native
anti-degradation service is default-off in commit `e07d8042f`, and isolated
configuration coverage was added in `557d5c079`; current AWS logs contain no
new service-start entry. The owner intends any future monitoring or mitigation
to be embedded in Sub2API instead of migrating the existing external stack.
Host health, application logs, runtime guards and the AWS status alarm remain
available for the migration window, but external monitoring enrollment is not
a cutover gate. Candidate-specific backup/restore evidence and automatic TLS
issuance are still gates.

## 2026-09-25 functional validation

- A local authenticated edge test used an SSH tunnel terminating at the AWS
  Caddy listener so the protected release-probe key stayed in process memory
  and was neither printed nor written locally. `/v1/models` returned 200 with
  27 models; a non-streaming `gpt-5.6-sol` Responses call returned 200 and
  completed in 2.38 seconds. Five streaming calls all returned 200 with a
  `response.completed` event and no error or disconnect. First response-byte
  times were 10.84, 11.31, 11.31, 11.79 and 11.45 seconds; total times were
  11.02, 32.48, 11.66, 12.32 and 27.81 seconds. Application logs recorded all
  six Responses calls as HTTP 200 with no retry, forward-failure, cancellation
  or broken-pipe event.
- Those OpenAI probes selected account 69, which is still bound to proxy 6,
  `Azure JP` at Tailnet endpoint `100.79.230.109:7890`. Therefore the tests
  prove the AWS application/Caddy path but do not remove the Azure proxy
  dependency. The variable 10-32 second duration remains consistent with
  upstream/account/egress variability. It is not evidence of AWS CPU, memory
  or Caddy saturation, and the earlier user report still has CPA cancellation
  as the direct interruption mechanism after a slow upstream wait.
- The release coordinator is configured with
  `SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE=preserve`, which preserves
  the source generation's exact setting rather than enabling compatibility.
  The active container has no `SUB2API_FIXED_EGRESS_COMPATIBILITY_MODE` entry,
  so the current release remains on the strict/default contract; there is no
  compatibility override to remove before cutover.
- Synchronous `/v1/images/generations` with `gpt-image-1` returned 200 in about
  17 seconds with a valid Base64 image. The asynchronous route accepted a task
  with 202 and later reached `completed` with one object-storage result. The
  release probe key belongs to group 16 (`测试分组`), where ordinary image
  generation is enabled but Gemini Batch Image is disabled;
  `/v1/images/batches/models` correctly returned 403
  `BATCH_IMAGE_GROUP_DISABLED`. A low-cost Batch Image request still requires
  a temporary key in an enabled Gemini group and deletion of that key after
  the test.
- Unified payment is enabled in `live` mode with provider
  `https://pay.totools.cn`, production return/webhook URLs and a healthy
  `sub2api-payment-vault` sidecar. Provider TLS/connectivity passed, the
  monitor-token-protected refund rollback endpoint returned 200 with
  `ready=true` and zero reviewed pending entitlements, and an invalid-signature
  webhook was rejected with 400. A real 1-2 fen owner checkout remains pending
  recent administrator TOTP step-up and explicit action-time confirmation.
- The database-host rules shown by the owner contain exact single-IP permits
  for `54.248.123.174` on PostgreSQL 5432 and Redis 6379. The UI omits `/32`
  when displaying the single IP, but application-container connectivity has
  already passed for both ports. Keep the old Azure source until rollback is
  retired.

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

- Reconcile the operator SSH identity in Vault; the restricted GitHub deploy
  identity is recorded under Vault item `86513fc6-74bb-47f5-8942-c91db01e0630`.
  Replace the temporary operator-address firewall rule when its address changes.
- Establish candidate-only backups, a rollback image, automatic certificate
  issuance evidence, an offsite copy and an isolated restore drill. Observe a
  later automatic renewal separately; first issuance is not renewal evidence.
  External Komari/anti-degradation/standalone monitoring migration is explicitly
  out of scope by owner direction; do not reintroduce it as a hidden cutover
  prerequisite. During the cutover window, use explicit operator observation of
  Lightsail status, application/Caddy errors, latency, memory, network and the
  rollback stop conditions instead of claiming an unverified alert channel.
- Retain successful GitHub Actions run `36070650495` and release log
  `/var/log/sub2api-release/gha-20260924-231200-557d5c07-*` as the
  end-to-end deployment evidence. Keep the separate `azure-production`
  Environment and its secrets unchanged.
- Preserve the exact database allowlist at `54.248.123.174` and remove it only
  after rollback/cutover decisions. Do not rerun the September 12 currency
  conversion or restore an old full DB over new writes.
- Create DNS-only `aws-test.turtleligpt.com`, deploy the tracked automatic-TLS
  test host, validate ACME issuance and run authenticated text/stream/image
  probes through it before changing `api` or `www`. The test-host config is
  staged and validated but intentionally not loaded. The bounded Cloudflare API
  operation remains blocked on one owner-run Vault injection; public DNS still
  returns no record for the test hostname. Before loading it, open a bounded
  public TCP 80/443 ACME window in both Lightsail and UFW; the current
  operator-only `/32` rules block HTTP-01 and TLS-ALPN-01. Recheck host exposure,
  complete issuance and smoke tests, then restore the reviewed ingress boundary
  appropriate for the production cutover.
- Run the low-cost Gemini Batch Image canary with a temporary enabled-group key
  and delete that key. Complete a 1-2 fen owner payment checkout after recent
  administrator TOTP step-up.
- Replace or clear every Azure proxy binding before deleting any Azure node.
  Current dependencies are proxy IDs 6, 7, 40, 41 and 44 across nine accounts.
  One AWS proxy cannot preserve both Tokyo and US-West egress. The minimum
  full replacement is one Tokyo and one US-West node; a third independent
  US-West node preserves the current fixed-egress backup fault domain. Use the
  authenticated CAS endpoint to move parent accounts and credential shadows;
  never edit raw SQL and never permit cross-region automatic fallback.
- Verify 2 GiB memory plus swap headroom with the real image and background
  workload, sustained bandwidth under representative concurrency, DNS, TLS,
  www static assets, and public API smoke before any production cutover.
- Transfer sole background/queue ownership from Azure to AWS under the
  maintenance lock, take a fresh database backup, preserve an offsite copy and
  prove isolated restore before DNS cutover.
- Owner-directed Azure retirement spans two subscriptions. Subscription
  `6835deb1-678b-4067-b516-b57f80e14e25` owns `sub2_group`, `jp_group`, and
  `westus_group`; subscription `65c9db87-f353-427d-80cc-af2953c8761b` owns
  `jp2_group`, `westus2_relay_group`, and `westus3_relay_group`. The six VMs
  were running at the 2026-09-25 inventory. The only Azure public IPv4 found
  was `sub2-ip` (`4.216.216.16`) in `sub2_group`; the relay nodes expose static
  IPv6 addresses. The five relay groups are separate proxy workloads and may
  serve clients outside the nine Sub2 account bindings. Before deleting them,
  perform an independent relay/client dependency inventory and confirm every
  remaining consumer has migrated or is intentionally retired. Preserve both
  subscriptions' `NetworkWatcherRG` groups unless separately approved.

No production DNS or Azure runtime ownership was changed. The database host now
allows the exact AWS source for the tested candidate only.
