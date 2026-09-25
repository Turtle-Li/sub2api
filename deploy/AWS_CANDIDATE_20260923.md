# AWS Sub2API migration candidate (2026-09-23)

This is a staging host only. As of 2026-09-25, Azure `sub2api-candidate`
continues to serve production traffic and own background work. AWS accepts
public probes only through the DNS-only rehearsal host
`aws-test.turtleligpt.com` while remaining `background=standby`; production
`api` and `www` DNS have not changed. The old `sub2api-new` is not a fallback.
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
| Public ingress | TCP 80/443 from `0.0.0.0/0` for the DNS-only automatic-TLS rehearsal and eventual public service. TCP 22 remains limited in Lightsail to the current operator IPv4 `115.195.32.146/32`, Azure `4.216.216.16/32`, and the Lightsail browser-SSH alias. An AWS OIDC role may add only the current GitHub-hosted Runner IPv4 `/32` during an `aws-candidate` release and must remove it in an `always()` cleanup step. Database ports are not public. |
| Runtime directories | Root-owned `/opt/sub2api` and `/var/log/sub2api-release`, mode 0750; `secrets`, `db-host-ca`, and `staging` mode 0700 |
| Installed baseline | Docker 29.1.3, Compose 2.40.3, sysstat, unattended-upgrades; UFW default-deny incoming, allow outgoing |
| Release-control staging | Root-owned `/opt/sub2api/scripts` and mode-0600 `/etc/sub2api-autodeploy.env`; external dependency mode, `preserve-standby`, real-request probe enabled, loopback-pinned public health check, both release/recovery timers disabled and inactive |
| GitHub deployment | Environment `aws-candidate` owns distinct host, user, key and known-host secrets plus non-secret OIDC role, region and instance variables. Forced-command account `sub2api-github-deploy` accepts only the image-release protocol through root-owned `/usr/local/libexec/sub2api-github-deploy-trigger`; the application root remains 0750. Deploy-key fingerprint `SHA256:im2yTlnEhikA+shKRt00rpAuBVhHTvXYwlH8d7nOOpc`; Vault item `86513fc6-74bb-47f5-8942-c91db01e0630`; OIDC role `GitHubSub2APIAWSCandidateDeploy` trusts only `repo:Turtle-Li/sub2api:environment:aws-candidate`. |
| Application release | Fork `main` commit `32eeb9e2bc38e7d7874d50e33f11d2691db0790a` from successful GitHub Actions run `36101027759`, active image `sub2api:auto-20260925-060932-32eeb9e2` and slot `sub2api-blue`, healthy with zero restarts/OOM; 2 GiB persistent swap is enabled with swappiness 10 |
| Proxy | AWS-specific Caddy route for API and www plus DNS-only `aws-test`; public HTTPS passed health, auth-boundary, public settings, homepage, authenticated Responses/SSE and synchronous/asynchronous image probes |
| Local Docker state | Healthy application, Caddy, payment Vault Agent, and Feishu Vault Agent containers with project network and separate named volumes |
| Public www assets | Six regular files in the Caddy data volume at `/data/sub2-web/{home,help}`, copied from the serving Azure Caddy volume and SHA-256 matched file by file |
| Automatic-TLS rehearsal | Cloudflare DNS-only A record `aws-test.turtleligpt.com` points to `54.248.123.174`. The active Caddyfile matches the tracked staged SHA-256 `0947c585dbe57f9ad127dee5d3d12832ce058ea0f75f68cd64d3b601af40013f`. Let's Encrypt HTTP-01 validation and initial issuance passed on 2026-09-25; later renewal must still be observed separately. |

SSH effective settings were verified as `PubkeyAuthentication yes`,
`PasswordAuthentication no`, `KbdInteractiveAuthentication no`, and
`PermitRootLogin no`. The imported Lightsail key pair selects the public key;
no private key was exported from AWS. The local private key remains device-local
and is not a project artifact. Its Vault reconciliation is pending; no
`vault_ref` has been invented.
The effective local alias resolves final `hostname 54.248.123.174` with
`ProxyJump sub2api-candidate`. A remote identity check returned hostname
`ip-172-26-4-61`, public IPv4 `54.248.123.174` and manufacturer `Amazon EC2`,
so the Azure address seen during SSH setup is the documented jump transport,
not the inspected runtime target.

The Lightsail firewall admits public IPv4 TCP 80/443 for the DNS-only ACME and
public-route rehearsal. Steady-state TCP 22 admits the operator's current IPv4
`115.195.32.146/32`, Azure `4.216.216.16/32` and the
`lightsail-connect` console alias. For an explicitly
dispatched `aws-candidate` release, GitHub OIDC obtains a short-lived AWS role,
discovers the Runner's public IPv4, validates it as a global IPv4 address, adds
only that `/32`, and removes the same `/32` in an `always()` cleanup step. Host
UFW admits IPv4 TCP 22 generally because Lightsail is the source-address gate;
it does not admit IPv6 SSH. UFW admits public TCP 80/443 and keeps database
ports closed. The operator address is temporary and must be revalidated before
future SSH access. Public web ingress is now intentional for the DNS-only test
host and eventual production service, but it is not authorization to change
production DNS.

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
a cutover gate. Candidate-specific backup/restore evidence remains a gate;
initial automatic TLS issuance has passed and later renewal observation is
separate evidence.

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
- A separate read-only audit at 2026-09-25 06:39 UTC confirmed that the active
  `sub2api-blue` container, Caddy and both Vault Agents had zero restarts and no
  OOM state. The 2-vCPU host had about 1.2 GiB available RAM, negligible swap
  use and 14% root-disk use; the application and Caddy used about 53 MiB and
  18-20 MiB respectively. In the two post-deploy Batch canary windows, status
  and item requests returned HTTP 200 in about 7-80 ms, the ZIP fallback in
  1.081 seconds and item content in 0.926 seconds. Application and proxy output
  showed no 5xx, panic, fatal, OOM, cancellation, broken pipe or client-disconnect
  signal. Caddy stdout currently contains administrative/configuration output
  rather than complete request access lines; this is a request-correlation
  evidence gap, not a confirmed runtime fault, and must be considered during
  operator observation.
- The release coordinator is configured with
  `SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE=preserve`, which preserves
  the source generation's exact setting rather than enabling compatibility.
  The active container has no `SUB2API_FIXED_EGRESS_COMPATIBILITY_MODE` entry,
  so the current release remains on the strict/default contract; there is no
  compatibility override to remove before cutover.
- Synchronous `/v1/images/generations` with `gpt-image-1` returned 200 in about
  17 seconds with a valid Base64 image. The asynchronous route accepted a task
  with 202 and later reached `completed` with one object-storage result.
- A temporary owner key in enabled Gemini group 7 submitted low-cost Vertex
  Batch Image job `imgbatch_9b7e6f6e539f3ecf0f21d55314fc596d` through the
  AWS public test hostname. It moved `queued -> running -> completed`, produced
  one successful item and no failed item, and settled actual cost
  `0.0001447875`. The authenticated item-content endpoint returned a valid
  1024x1024 PNG (722,989 bytes). The temporary key was tombstoned immediately
  after the test and then returned 401. This proves AWS API/Caddy admission,
  PostgreSQL/Redis state, the current Azure background owner, GCS/Vertex
  execution, settlement and AWS result reads; it does not yet prove AWS queue
  ownership.
- Both Azure and AWS currently have `BATCH_IMAGE_DELIVERY_ENABLED=false`, so
  completed Vertex provider-output jobs use the authenticated server-ZIP
  fallback rather than a private COS archive. Release `32eeb9e2b` checks for
  the archive marker before requiring COS delivery configuration: legacy jobs
  return `BATCH_IMAGE_RESULT_ARCHIVE_UNAVAILABLE`, while real COS-archive jobs
  still fail closed on configuration, storage, signature, network or integrity
  errors. Post-deploy job `imgbatch_8f4ccab2700afedec0be0fbd561964e5`
  moved `queued -> running -> completed`, settled one success and zero failures
  at actual cost `0.0001447875`, and exposed public item status `succeeded` with
  MIME type `image/png`. `result-files` returned HTTP 409 with the required
  fallback code; `/download` returned a valid ZIP containing a 1024x1024 PNG
  of 206,491 bytes, and the item-content endpoint returned the same valid PNG.
  Temporary key 109 was tombstoned, the original credential was absent from the
  database, and the next authenticated request returned 401. Earlier
  post-deploy job `imgbatch_41625cfb7603bd165fea4707a0c5d132` also completed
  successfully at the same cost; its verifier stopped only because it expected
  the internal item state `success` instead of the public API state
  `succeeded`, and temporary key 108 was still tombstoned and verified 401.
- Unified payment is enabled in `live` mode with provider
  `https://pay.totools.cn`, production return/webhook URLs and a healthy
  `sub2api-payment-vault` sidecar. Provider TLS/connectivity passed, the
  monitor-token-protected refund rollback endpoint returned 200 with
  `ready=true` and zero reviewed pending entitlements, and an invalid-signature
  webhook was rejected with 400. Checkout from `aws-test.turtleligpt.com` is
  intentionally not a valid payment result origin because the configured Sub2
  result page is `www.turtleligpt.com/payment/result`; the owner chose not to
  weaken that production binding for the rehearsal hostname. A real 1-2 fen
  owner checkout therefore remains a post-DNS-cutover verification after recent
  administrator TOTP step-up and explicit action-time confirmation.
- The database-host rules shown by the owner contain exact single-IP permits
  for `54.248.123.174` on PostgreSQL 5432 and Redis 6379. The UI omits `/32`
  when displaying the single IP, but application-container connectivity has
  already passed for both ports. Keep the old Azure source until rollback is
  retired.
- A five-minute authenticated `/v1/models` admission soak ran 1,500 requests
  with 5 workers and no billable model invocation. All 1,500 returned HTTP 200
  in 329 seconds; P50/P95/P99 were 58.4/90.6/119.3 ms and the maximum was
  261.2 ms. Application CPU peaked at 3.7%, application memory at 77.4 MiB,
  host available memory never fell below 1,240 MiB, swap remained unused and
  application restarts stayed `0 -> 0`. No curl, application or Caddy error
  signal was observed. This proves request-path headroom while AWS remains
  `background=standby`; repeat representative observation after the controlled
  background-owner handoff.
- Read-only node-state checks returned AWS as `traffic=accepting`,
  `active_container=sub2api-blue`, `background=standby`, and Azure as
  `traffic=accepting`, `active_container=sub2api-blue`, `background=active`.
  Azure is still the
  sole background owner; no ownership transfer was attempted.

## Backup and restore evidence

The dedicated data host completed automatic archive
`/opt/sub2api-db-backups/sub2api-db-backup-20260925-121659.tar.gz` at 12:18 CST
on 2026-09-25. The six-hour backup timer is enabled and active. The outer
archive checksum and the internal `postgres.dump` and `redis.rdb` checksums all
returned `OK`. An isolated restore smoke then restored PostgreSQL and Redis in
temporary containers, passed `pg_amcheck`, produced `schema_count=317`,
`schema_hash=060cb9c360c8dfa99c774e6a4177797e` and `redis_live_keys=1825`, and
left no restore container behind.

Offsite retention remains open. The guarded NAS sync service/timer is installed
but intentionally disabled because `/root/.ssh/sub2api_nas_backup_target` is
absent and no target archive has a `.nas-synced` marker. Registry credential
record `cred-sub2api-db-nas-backup` confirms that the restricted write-only NAS
identity has not been created in Vault. Do not copy raw database archives to an
uncontrolled AWS location as a substitute. The owner must unlock Vault, create
the target-specific identity, pin the NAS host key, and validate upload plus
restore before enabling the timer.

## Bandwidth probe

Short single-connection IPv4 POSTs from the candidate to Cloudflare's speed
endpoint returned HTTP 200. Reported upload rates were 10.3 MB/s for 16 MiB,
44.1 MB/s for 32 MiB, 88.0 MB/s for 64 MiB, and 27.3 MB/s for 128 MiB.
The `ens5` transmit counter and Lightsail `NetworkOut` metric both advanced
by approximately 264 MB during the probe window. These are destination- and
burst-dependent observations, not a sustained throughput guarantee. No `tc`
rate limiter was configured on `ens5`. The candidate was not limited to a
fixed 16 MB/s in this test.

A later 512 MiB single-request attempt was rejected immediately by the test
endpoint with HTTP 413 after 65,536 bytes; that was an endpoint/request-size
limit, not a candidate service failure. The sustained replacement test used 16
sequential 64 MiB IPv4 uploads, each rate-limited to 16 MiB/s, for exactly 1
GiB of application payload. All 16 returned HTTP 200. The run lasted 68.254
seconds; the `ens5` transmit counter advanced 1,127,398,960 bytes and averaged
15.753 MiB/s including protocol overhead and inter-request gaps. Minimum host
available memory was 1,143.9 MiB, swap stayed at 0 MiB, load1 peaked at 0.09,
application CPU at 4.61% and application memory at 55.9 MiB. Application and
Caddy retained zero restarts/OOM and emitted no error signal in the test window.
This establishes bounded sustained egress and host headroom to the selected
destination; it is not a universal Internet throughput guarantee.

## Proxy dependency inventory

A privacy-safe read-only database inventory on 2026-09-25 found nine active
accounts still bound to Azure relay proxies and no credential values were read:

| Proxy ID | Azure relay / region | Account IDs |
| --- | --- | --- |
| `6` | `jp1` / Japan East | `9, 15, 54, 55, 69` |
| `7` | `jp2` / Japan East | `6` |
| `40` | `westus1` / West US | `56` |
| `41` | `westus2` / West US | `59` |
| `44` | `westus3` / West US | `60` |

All nine rows are parent accounts (`parent_account_id IS NULL`); the service
still atomically propagates a parent CAS to any credential shadow. Replacement
or clearing must use authenticated `POST /api/v1/admin/accounts/bulk-update`
with only `account_ids`, `proxy_id` and `expected_proxy_id`. A reverse-CAS must
record and swap the old/new proxy IDs, including expected `0` after a clear. Any
mismatch rejects the operation. Never mutate these bindings with SQL, and keep
the old proxies/nodes available through the observation window.

## Public automatic TLS and real-request evidence

Cloudflare DNS-only record `65bbd7f20163df4ebd734b9b71279e85` maps
`aws-test.turtleligpt.com` to `54.248.123.174` with TTL 300. Both
`1.1.1.1` and `8.8.8.8` returned the expected address. Caddy served the
HTTP-01 challenge to multiple Let's Encrypt validators and obtained a leaf
certificate for only `aws-test.turtleligpt.com`. The leaf is valid from
`2026-09-25 03:59:10 UTC` through `2026-12-24 03:59:09 UTC`; its SHA-256
fingerprint is
`02:EA:89:BE:62:3C:C4:47:25:D6:18:5B:55:F7:12:75:31:DA:EC:F4:E2:12:18:4D:60:83:61:4A:56:73:24:22`.
OpenSSL SNI, hostname and chain verification returned code 0. This proves first
issuance only, not automatic renewal.

Public HTTPS returned health 200, unauthenticated models 401, public settings
200 and homepage 200. A root-only protected release key was passed to clients
without appearing in process arguments or output. Through the public hostname
it returned 27 models including `gpt-5.6-sol` and `gpt-image-1`, completed one
non-streaming Responses request, completed one 18-event SSE request, returned
one low-quality synchronous `gpt-image-1` output, and completed asynchronous
task `imgtask_6e28c87748f94d7794405081db78f548` with one stored URL result.
Immediately afterward the application used about 83 MiB and Caddy about 23 MiB
of the 1.861 GiB container-visible memory; both had zero restarts/OOM and no
application/Caddy 5xx or fatal log entry in the probe window.

## Remaining migration gates

- Reconcile the operator SSH identity in Vault; the restricted GitHub deploy
  identity is recorded under Vault item `86513fc6-74bb-47f5-8942-c91db01e0630`.
  Replace the temporary operator-address firewall rule when its address changes.
- Retain the verified automatic database archive and isolated restore evidence;
  establish the restricted NAS offsite copy and test its restore before cutover.
  Preserve a known-good rollback image. Initial automatic certificate issuance is complete; observe a
  later automatic renewal separately; first issuance is not renewal evidence.
  External Komari/anti-degradation/standalone monitoring migration is explicitly
  out of scope by owner direction; do not reintroduce it as a hidden cutover
  prerequisite. During the cutover window, use explicit operator observation of
  Lightsail status, application/Caddy errors, latency, memory, network and the
  rollback stop conditions instead of claiming an unverified alert channel.
- Retain successful GitHub Actions run `36070650495` and release log
  `/var/log/sub2api-release/gha-20260924-231200-557d5c07-*` as the first
  end-to-end candidate evidence, plus successful fix release run `36101027759`
  and `/var/log/sub2api-release/gha-20260925-060932-32eeb9e2-379538/` as the
  current deployed evidence. Keep the separate `azure-production`
  Environment and its secrets unchanged.
- Preserve the exact database allowlist at `54.248.123.174` and remove it only
  after rollback/cutover decisions. Do not rerun the September 12 currency
  conversion or restore an old full DB over new writes.
- Keep DNS-only `aws-test.turtleligpt.com` and its automatic certificate in
  service for later renewal observation. Initial issuance and authenticated
  text/SSE/synchronous-image/asynchronous-image probes are complete. Do not
  change production `api` or `www` DNS until the remaining backup, proxy,
  background-owner, payment and observation gates pass.
- The Batch Image legacy-download compatibility fix and real request validation
  are complete. Complete a 1-2 fen owner payment checkout only after production
  `www` DNS points to AWS and recent administrator TOTP step-up.
- Replace or clear every Azure proxy binding before deleting any Azure node.
  Current dependencies are proxy IDs 6, 7, 40, 41 and 44 across accounts
  `6, 9, 15, 54, 55, 56, 59, 60, 69`.
  One AWS proxy cannot preserve both Tokyo and US-West egress. The minimum
  full replacement is one Tokyo and one US-West node; a third independent
  US-West node preserves the current fixed-egress backup fault domain. Use the
  authenticated CAS endpoint to move parent accounts and credential shadows;
  never edit raw SQL and never permit cross-region automatic fallback.
- Request-path concurrency, 2 GiB memory/swap headroom and bounded sustained
  egress have passed with the real image while AWS is standby. Recheck memory,
  queue progress, latency and errors after the controlled background handoff
  before changing DNS. DNS, initial TLS issuance, www static assets and public
  API smoke have passed.
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
