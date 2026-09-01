# Sub2 dual-node runtime, certificate, and fixed-egress contract

Status: implementation candidate only. This document does not authorize a
Cloudflare/DNS change, certificate activation, production deployment, or traffic
cutover.

Deployment checkpoint (2026-08-30): both live Caddy origins now use the same
imported bootstrap certificate generation through read-only external mounts,
and direct-IP TLS plus `/health` passed on both. GCP's forced-command identity
reports the same `CURRENT` generation from each receiver; its certificate
service/timer remain inactive/disabled. Runtime state/health-token deployment,
fixed account egress, Azure edge-policy convergence, staging prepare/discard,
and receiver activation/rollback remain incomplete. Public DNS still points
only to the old production origin; no traffic allocation is authorized.

## 1. Runtime model: identical nodes, dynamic ownership

There is no permanent `primary` or `replica-web` image. Every healthy Sub2 node
runs the same immutable application image and starts the same service graph.
Coordination happens at work-acquisition boundaries:

- `/var/lib/sub2api/runtime/traffic-state` is mounted read-only in the app as
  `/run/sub2api-runtime/traffic-state` and exported through
  `SUB2API_TRAFFIC_STATE_FILE`. Only the exact value `accepting` allows
  `/internal/readyz` to become ready.
- `/var/lib/sub2api/runtime/background/CONTAINER` is mounted read-only in that
  specific application generation as
  `/run/sub2api-runtime/background-state` and exported through
  `SUB2API_BACKGROUND_STATE_FILE`. Only the exact value `active` allows a
  generation to acquire new shared work.
- Missing or malformed configured files fail closed. An unconfigured legacy
  deployment retains its current behavior so a one-node upgrade is possible.
- The host runtime directories are root-owned `0755` and the state files are
  root-owned `0644`, so the application image's non-root UID/GID 1000 can read
  the bind mounts without being able to change them. The
  `sub2api-node-state.sh bootstrap` command must run before those paths are
  mounted into the first container;
  it preserves valid existing state and rejects directories or symlinks where a
  regular state file is required.
- `deploy/sub2api-node-state.sh drain` resolves the one Caddy-active container,
  first removes node traffic readiness, and then stops new claims for that
  generation. After the external controller has removed the node from its pool,
  `preflight` permits dependency-ready smoke while leaving the old generation's
  background state at `standby`; after the local Caddy switch, `activate` enables
  the new generation's claims before keeping traffic readiness at `accepting`.
  `abort` restores the Caddy-selected generation to `active`/`accepting` after a
  failed release. These actions use a bounded wait on the global maintenance
  lock and never change the shared lock directory's permissions. Cluster
  `drain`/`preflight` fail closed while an interrupted single-host transaction
  exists; the operator or runtime guard must complete `recover-local` first.
- SIGTERM disables new work and process readiness before HTTP shutdown. In-flight
  operations keep their existing context/lease and drain normally.

### Starter inventory

All nodes start these classes; no class is disabled merely because another node
exists.

| Class | Starters | Multi-node contract |
| --- | --- | --- |
| Request-local runtime | HTTP server, plugin manager, WebSocket pool/session maintenance, API-key cache subscriber, concurrency slot cleanup, local cache/log/metrics flushers | Always local to every Web generation. No leader election. |
| Multi-consumer claimed queues | Batch image worker, Prompt Audit worker, auth-cache invalidation outbox | Generation gate before each reserve/claim. Existing per-job lock, claim owner/version, heartbeat, and compare-and-update rules remain authoritative. |
| Singleton scheduled work | Batch image cleanup, CN balance check, scheduled account tests, scheduler outbox poll, usage cleanup, channel monitor checks, proxy expiry, OpenAI Codex version sync | Every active node contends for a short-lived renewable Redis lease each cycle. Each acquisition has a unique fencing owner; losing the Redis lease cancels the job context; leadership is not sticky. PostgreSQL advisory locking is used only when Redis is not configured. |
| Existing coordinated schedulers | Backup, dashboard aggregation, subscription/payment expiry, Ops cleanup/aggregation/alerts/reports, OpenAI quota auto-reset, idempotency cleanup | Keep their existing database/Redis leader, claim, or idempotency contracts. The common singleton-lock path now also applies the generation gate, unique acquisition fencing, renewable Redis leases, and lost-lease cancellation. |
| Idempotent local writers | Deferred last-used flush, user-platform quota flush, audit/system-log batching, local runtime samplers | May run on each node; writes are additive, conditional, or idempotent. They must not be converted into a permanent server role. |
| Token refresh | OAuth refresh scanner and request-triggered refresh | The generation gate stops new background scans while draining; account-scoped refresh locks and credential compare-and-update remain the cross-node fence. Request-triggered refresh remains available on traffic-accepting nodes. |

Redis lock TTLs are short crash-recovery bounds and are renewed while their job
context remains active. A cycle releases immediately and re-contends on the next
interval. When Redis is configured, a Redis error fails closed instead of
falling through to an independent PostgreSQL-only leader; this prevents split
leadership during a partial partition. Redis is the sole production fence when
configured, so the shared Redis deployment must use `maxmemory-policy noeviction`
and a persistence/failover policy that does not silently discard live lease keys.
PostgreSQL is the sole fence only when Redis is not configured. A new job
without a claim/version/idempotency/lease contract is not dual-node safe and
must not be added to the starter graph until it has one.

## 2. Internal health contract

The public `/health` response remains unchanged. The load-balancer/controller
contract is:

- Header: `X-Monitor-Token`.
- Token source: root-injected file named by
  `SUB2API_INTERNAL_HEALTH_TOKEN_FILE`; it must be a regular non-symlink file,
  owned by the application UID/GID 1000, mode `0600`, non-empty, and at most
  4096 bytes. It is reread for rotation. Root owns the parent directory and the
  application receives the file read-only.
- `GET /internal/livez`: `200 {"live":true}` when the process is running.
- `GET /internal/readyz`: `200 {"ready":true}` only when traffic state is
  accepting and bounded PostgreSQL and Redis pings both pass. Otherwise it
  returns generic `503 {"ready":false}` without dependency details.
- Missing/wrong token returns 401 before any dependency probe.

The controller must connect directly to the node IP while using SNI and Host
`api.turtleligpt.com`. This repository does not create or copy its token and
does not change the GCP monitoring deployment.

## 3. Fixed account egress (T-EG-01)

Each Sub2 application node is also an egress gateway for exactly one account,
but the listener is Tailnet-only. Both application nodes may send a bound
account through either gateway, so request placement does not change that
account's public exit IP.

| Logical gateway | Listener | Proxy record | Account binding |
| --- | --- | --- | --- |
| old-node egress | old node Tailnet IPv4, TCP 1080 | active `socks5h`, `expires_at=NULL`, `fallback_mode=none`, `backup_proxy_id=NULL` | Existing OpenAI OAuth account selected for the old public exit. |
| Azure egress | `100.80.10.114:1080` | active `socks5h`, `expires_at=NULL`, `fallback_mode=none`, `backup_proxy_id=NULL` | Existing OpenAI OAuth account selected for the Azure public exit. |

The actual account IDs and Proxy IDs are deployment state and must be recorded
by the account-state owner before cutover; this repository deliberately does
not guess them or execute raw SQL.

Binding and rollback use the authenticated account bulk-update endpoint as a
compare-and-set operation. A first binding sends `proxy_id=P` and
`expected_proxy_id=0`; rollback sends `proxy_id=0` and
`expected_proxy_id=P`. The server locks the eligible active OpenAI OAuth parent
rows, validates the no-credential Tailnet `socks5h:1080` Proxy row, updates all
credential shadows in the same transaction, and rejects any partial match.
Legacy single-account and bulk proxy edits are rejected for OpenAI OAuth parents,
so the UI cannot bypass the fixed-egress CAS/shadow invariant.

Minimum gateway controls:

1. Bind Dante (or an equivalent CONNECT-only SOCKS5 daemon) to the node's exact
   Tailscale IPv4, never `0.0.0.0` or the public NIC.
2. Permit clients only from the two Sub2 Tailnet node `/32` addresses and the
   node's local Docker subnet. Tailscale ACLs allow only those two node tags to
   TCP 1080. Host firewall rules repeat the exact source/destination/port rule.
3. Permit only SOCKS `CONNECT`; deny UDP associate and bind. Deny loopback,
   RFC1918, link-local/metadata, and Tailnet/CGNAT destinations before the
   Internet allow rule.
4. Use Tailnet identity plus ACL/firewall isolation; do not expose anonymous
   SOCKS on a public interface. No GCP controller or monitoring host needs this
   port.
5. The application normalizes `socks5` to `socks5h`, so DNS resolution occurs
   at the fixed egress node.

Application behavior is fail-closed: `proxy_id IS NULL` is the only direct
connection case. A bound proxy that is missing, mismatched, inactive, expired,
unparseable, or not hydrated returns `503 ACCOUNT_PROXY_UNAVAILABLE`; it never
silently connects directly. HTTP, OAuth refresh, quota/usage/privacy probes,
WebSocket ingress/forwarding, and pooled WebSocket compatibility all use the
same binding. The WS pool includes the normalized proxy URL in its compatibility
key so a connection cannot survive an egress rebind.

Before decommissioning either server, explicitly move its account to a new
tested Proxy record (or explicitly approve direct egress), verify the new public
IP, and only then stop the old gateway. Rollback is the inverse audited Proxy ID
change. There is no automatic proxy or direct fallback.

## 4. Central certificate receiver

The only authoritative wire protocol is
`infra-monitoring-beszel-feishu/gcp/sub2-cert-manager/PROTOCOL.md`. The node
receiver implements these exact commands and single-line success responses:

| Command | Success response |
| --- | --- |
| `prepare GENERATION CERT_SHA KEY_SHA MIN_SECONDS DOMAIN` | `PREPARED GENERATION CERT_SHA` |
| `activate GENERATION DOMAIN` | `ACTIVATED GENERATION` |
| `status DOMAIN` | `CURRENT GENERATION CERT_SHA` |
| `rollback GENERATION DOMAIN` | `ROLLED_BACK GENERATION` |
| `commit GENERATION DOMAIN` | `COMMITTED GENERATION` |
| `discard GENERATION DOMAIN` | `DISCARDED GENERATION` |

The forced SSH key uses `restrict`, an exact Tailnet `from=` address, and a
forced command. Generations are exactly 20 lowercase hexadecimal characters;
hashes are exactly 64 lowercase hexadecimal characters; the generation must be
the certificate hash prefix; the requested lifetime is bounded; and DOMAIN must
equal the configured domain. The archive is read with a hard byte cap and must
contain exactly two regular files (`fullchain.pem`, `privkey.pem`) with no links,
devices, duplicates, or alternate paths.

`prepare` checks P-256, SAN, lifetime, certificate hash, public-key hash, and
key match, then validates a rendered Caddy configuration without selecting it.
`activate` atomically changes `current`, validates/reloads Caddy, verifies the
served SNI certificate and HTTPS `/health`, and automatically restores the
previous generation on failure. Automatic rollback is recorded as an
idempotent `rolled_back` transaction so the GCP coordinator can replay
`rollback` and then `discard` after an uncertain SSH result. One transaction file retains exactly one
bounded rollback predecessor; `commit` does not remove it, so the named
transaction remains rollbackable until a later activation replaces the bounded
history. After rollback the failed candidate is inactive and discardable;
`discard` refuses the current generation or an active rollback predecessor.
An attempted activation that failed before creating a transaction is an
idempotent rollback no-op when the named generation is not current. Interrupted
`activating` transactions are recoverable on either side of the current-link
switch. `commit` prunes every other valid generation, so only current plus its
single rollback predecessor remain. Receiver and maintenance locks have bounded
waits; stale private staging payloads older than one hour are swept only while
both locks are held, including interrupted atomic-link and transaction temp
files.

Before the GCP coordinator is enabled, each node must import its currently
served public certificate as the managed bootstrap `current` generation. The
receiver refuses an activation without that predecessor, preserving the local
rollback guarantee even for the first coordinated release.

`current` and `previous` must point to `generations/GENERATION` with relative
symlink targets. The host certificate root is mounted at a different absolute
path inside Caddy, so a host-absolute target would be unreadable in the
container. Receiver tests cover relative-link preservation across activation
and rollback.

The Caddy container needs a read-only mount:

```text
/opt/sub2api/certs/api.turtleligpt.com:/etc/sub2api-certs:ro
```

and the domain site must use:

```caddyfile
tls /etc/sub2api-certs/current/fullchain.pem /etc/sub2api-certs/current/privkey.pem
```

## 5. Current Caddy difference (read-only audit, 2026-08-30)

| Item | Old node | Azure candidate |
| --- | --- | --- |
| Container | `sub2api-caddy` | `sub2api-candidate-caddy` |
| Image/version | `caddy:2.11-alpine`, v2.11.4 | `caddy:2.11-alpine`, v2.11.4 |
| Runtime user | container default/root | container default/root |
| Caddyfile host mount | `/opt/sub2api/Caddyfile` | `/opt/sub2api/Caddyfile` |
| Current site address | `api.turtleligpt.com` with external certificate | `api.turtleligpt.com` with external certificate |
| App upstream | `sub2api-green:8080` | `sub2api:8080` |
| Large request budget | 100 MB on responses/image-batch routes | 128 MB on responses/image-batch routes |
| Default request budget | 16 MB | 16 MB |
| Streaming tuning | `flush_interval -1`, 1800-second read/write transport bounds | no equivalent explicit streaming transport block |
| Other sites | Existing unrelated `www/chat` sites share the old Caddy instance | candidate contains only the IP test site |
| External cert mount | `/opt/sub2api/certs/api.turtleligpt.com:/etc/sub2api-certs:ro` | `/opt/sub2api/certs/api.turtleligpt.com:/etc/sub2api-certs:ro` |

Target convergence keeps the old node's unrelated sites untouched, gives both
Sub2 API domain sites the same externally managed certificate paths and request
budgets, preserves streaming behavior, and changes only each node's appropriate
application upstream name. The generic example Caddyfile is a contract fixture,
not a production replacement for either complete live file.

## 6. Release topology

Topology is an explicit operator/controller decision; it is never inferred from
ping results.

Because the API record is DNS-only and may resolve to a different node during a
rolling release, every node sets `SUB2API_PUBLIC_HEALTH_RESOLVE` to
`api.turtleligpt.com:443:<that-node-public-IPv4>`. Release and runtime-recovery
health checks pass this value to curl `--resolve`, preserving the public TLS/SNI
contract while proving the local origin that was actually changed. Omitting the
override is supported for a true single-origin deployment, but a dual-node
deployment must not use a load-balanced DNS answer as node-local rollback
evidence.

- `cluster-rolling` (two nodes): build one immutable image, remove node A from
  the external pool, run node `drain`, wait for shared-work claims to stop, start
  the candidate in `standby`, run node `preflight`, require authenticated direct
  candidate ready smoke, switch local Caddy, run node `activate`, require direct
  TLS smoke, return A to the external pool, then repeat on B. At least one node must
  remain ready. Any gate failure stops before the next node and invokes the
  node-local rollback.
- `single-host-blue-green` (one node): use the existing
  `sub2api-server-release.sh` blue/green switch and its local Caddy rollback.
  The absence of a second node never disables background services; the active
  color owns traffic and competes for all shared work. The release wrapper
  bootstraps state, prepares the candidate color as `standby`, marks the
  Caddy-selected color `active` after the switch, and invokes `abort-local` after a
  successful Caddy rollback. These calls reuse the maintenance lock already
  held by the release transaction.
  A root-owned local-release transaction distinguishes this path from an
  intentional cluster `drain`/`preflight`. If the release process is killed on
  either side of the Caddy switch, the periodic runtime guard first proves the
  selected container, all Caddy views, and public health, then runs
  `recover-local` to promote the selected candidate or restore the previous
  owner. It never reconciles cluster drain state.

The repository installer installs the certificate receiver/trigger and node
state helper as root-owned scripts, and CI runs their protocol/state tests. The
repository also contains the audited blue/green helper, but replacing an
existing externally managed helper requires the explicit
`--install-blue-green-helper` option; the installer first writes a root-only
rollback backup. `SUB2API_DUAL_NODE_RUNTIME_ENABLED=true` then makes the helper
require and mount the per-color read-only background state, shared traffic state,
and UID-1000 health token. With the flag unset/false, legacy single-node releases
retain their prior container contract instead of failing on absent runtime files.

On an external-PG/Redis node, stage the installer with explicit runtime object
names and `--no-enable-runtime-guard`. This writes the external dependency and
dual-runtime contract while installing the guard timer in the disabled state.
Create/verify the state files and the first conforming application color before
enabling the timer; never let a fresh install run once with local-dependency
defaults. The required installer options are:

```text
--dependency-mode external
--runtime-network <exact-docker-network>
--runtime-data-volume <exact-docker-volume>
--caddy-container <exact-caddy-container>
--external-runtime-env-file /etc/sub2api-external-runtime.env
--external-ca-file /opt/sub2api/db-host-ca/ca.crt
--dual-node-runtime-enabled true
--replace-config
--no-enable-runtime-guard
```

Runtime-mode options are never merged into an existing autodeploy config
implicitly. When the config already exists, the installer fails closed unless
`--replace-config` is present, preventing an external node from retaining the
legacy local-dependency mode.

Host runtime-state relocation uses `SUB2API_TRAFFIC_STATE_FILE_HOST` and
`SUB2API_BACKGROUND_STATE_DIR_HOST` consistently across node-state,
blue/green release, and runtime recovery. The application container continues
to receive the separate container paths through `SUB2API_TRAFFIC_STATE_FILE`
and `SUB2API_BACKGROUND_STATE_FILE`.

The guard rereads the root-owned external environment and CA immediately before
every application start/restart. It also checks the exact network, data volume,
external CA mounts and environment, and all three dual-runtime mounts/environment
variables for the active or fallback container. A mismatch fails closed before
Docker lifecycle or Caddy actions. Only after authenticated direct-origin
`livez`/`readyz` and Caddy checks pass may the operator run
`systemctl enable --now sub2api-runtime-guard.timer`.

Runtime recovery calls the blue/green helper with the internal
`ALLOW_ISOLATED_OLD_CONTAINER=true` contract only after it has stopped (or
confirmed the absence of) the failed Caddy-selected slot and verified an
already-running fallback. That mode forbids precreate, backup, pull, target
removal, and identical old/new names; ordinary releases retain the strict
requirement that the old slot exists and is running.

The GCP/Cloudflare controller owns pool weights and DNS/LB state. Node scripts
own only local readiness, background admission, image/color transition, Caddy,
and local rollback. No node-side script in this change modifies Cloudflare.

## 7. Cutover gates still external to this change

### Fixed account egress gateway

Each application host may also act as one account's fixed SOCKS5 gateway. Use
`deploy/install-sub2api-fixed-egress.sh` with the host's exact Tailnet IPv4,
its public egress interface, and only the two application Tailnet `/32` values
plus the local Docker bridge CIDR. The installer binds TCP `1080` only to the
Tailnet address, permits only SOCKS TCP CONNECT, rejects UDP/BIND and private,
metadata, multicast, and Tailnet destinations, and installs a dedicated
nftables input chain. It records a root-only rollback backup path. Proxy
records must use `socks5h`, no expiry, and `fallback_mode=none`; application
policy remains the final fail-closed boundary if the gateway is unavailable.

- Create/inject the existing health token paths without copying the token into
  this repository.
- The bootstrap public certificate, restricted receiver, and read-only mounts
  are installed and direct-IP TLS is verified. Run staging prepare/discard plus
  receiver activation/rollback drills before enabling renewal.
- Create two no-expiry/no-fallback Proxy records, bind the two confirmed OpenAI
  OAuth accounts, and verify the public egress IP from either application node.
- Update the live deployment helper/Compose mounts for both runtime state files
  and health token file, then execute authenticated direct-IP health smokes.
- Complete controller-owned Cloudflare/LB gates in its own task. Monitoring
  frequency and Beszel/Gatus configuration remain frozen and out of scope.
