# GCP Taiwan Premium line candidate

This record is authoritative for the retained GCP Taiwan network candidate.
It does not authorize a production cutover, DNS change, or installation of an
unspecified proxy, VPN, tunnel, or relay protocol.

## Resource identity

| Field | Value |
| --- | --- |
| GCP project | `project-28424c50-8df2-46e2-a27` |
| Region / zone | `asia-east1` / `asia-east1-b` |
| Instance | `sub2-tw-line-candidate` |
| Machine type | `e2-standard-2` |
| Boot image / disk | Debian 12 Bookworm; 10 GiB `pd-balanced` |
| VPC / subnet | `sub2-tw-line-test-vpc` / `sub2-tw-line-test-subnet` |
| Internal IPv4 | `10.30.0.2` |
| Retained static IPv4 | `130.211.243.139` |
| Network tier | `PREMIUM` |
| Current state | `RUNNING`; HAProxy transport active; exact public TCP `80/443` rule/tag active |
| Access | GCP control plane plus serial output; no project SSH key, service account, or runtime credential is provisioned |

The exact Premium address is the tested network identity. Do not release it,
replace the instance, or recreate its network interface without an explicit
owner decision and a fresh same-address validation plan. The one-shot startup
artifacts were removed from instance metadata after their hashes and success
marker were verified.

## Approved role and traffic path

The approved topology is transport-only:

```text
mainland client -> GCP Taiwan HAProxy (TCP 80/443)
                -> Azure Japan Caddy (TLS, HTTP/1.1 + HTTP/2)
                -> Azure Sub2API application
                -> external PostgreSQL / Redis
```

- HAProxy does not terminate TLS, inspect HTTP, run Sub2API, hold an API key,
  connect to PostgreSQL/Redis, or act as an OAuth egress proxy.
- TCP `80` is forwarded without PROXY protocol so Caddy's automatic redirect
  listener remains ordinary HTTP. TCP `443` sends PROXY v2.
- Azure Caddy parses PROXY v2 only from `130.211.243.139/32`, then terminates
  TLS with the retained certificate. Direct Azure TLS remains available for
  rollback, and forged PROXY prefaces from other sources fail TLS.
- Azure advertises HTTP/1.1 and HTTP/2 only. HTTP/3 is intentionally disabled
  because the GCP ingress does not forward UDP `443`.
- The old origin remains the production DNS target and rollback origin until
  the cutover gates pass. A client that hard-codes the old IP cannot be moved
  transparently by a DNS change.

## Versioned artifacts

| File | Purpose |
| --- | --- |
| `haproxy.cfg` | Frozen TCP-only GCP frontend/backend contract |
| `install-gcp-haproxy.sh` | Exact-host, Debian-source-verified stage/activate/update/rollback transaction |
| `gcp-startup-bootstrap.sh` | Hash-pinned one-shot GCE metadata bootstrap; fail-closed and credential-free |
| `gcp-update-bootstrap.sh` | Hash-pinned one-shot metadata updater; validates and seamlessly reloads HAProxy, restoring the previous config on failure |
| `render-azure-caddy-listeners.py` | Inserts only the exact `servers :443` listener policy into an empty global block |
| `verify-azure-caddy-json.py` | Verifies and fingerprints the effective `:443` wrappers, API route, upstream, and safe default X-Forwarded-For contract |
| `azure-caddy-listeners.sh` | Maintenance-lock-protected Azure Caddy stage/rollback/commit transaction |
| `verify-transport.sh` | Azure, GCP-local, and public canary verification |
| `tests/transport-config-test.sh` | Offline syntax, renderer, ordering, and L4 boundary regression checks |
| `AZURE-CADDY-RUNTIME-EVIDENCE-2026-09-02.md` | Frozen non-secret live site/listener evidence used to close the client-IP/XFF review boundary |

Run the local gate before copying an artifact:

```bash
./deploy/gcp-taiwan-line/tests/transport-config-test.sh
shellcheck deploy/gcp-taiwan-line/*.sh \
  deploy/gcp-taiwan-line/tests/*.sh
```

The GCP bootstrap must be attached only while the instance has no ingress tag.
Supply the three artifact files and their SHA-256 metadata, set the exact
`startup-script`, start the exact VM, and require `GCP_BOOTSTRAP_PASS` in serial
output before creating or attaching the `sub2-tw-line-ingress` rule/tag.
Remove `startup-script`, all three artifact bodies, and all three SHA metadata
keys immediately after success. Do not add a service account or SSH credential
merely to bootstrap HAProxy. An already successful bootstrap exits
idempotently instead of disabling a healthy listener because of a later
transient Azure check.

After activation, apply reviewed HAProxy changes only through
`gcp-update-bootstrap.sh` and the installer's `update` phase. It publishes a
recovery transaction before changing the config, validates the candidate,
uses `systemctl reload`, and restores/reloads the exact previous config on
failure. The frozen HAProxy config retains Debian's `haproxy` user/group and
chroot; `verify-transport.sh gcp` proves the live worker actually dropped
privileges and entered `/var/lib/haproxy`.

The Azure listener transaction uses the canonical shared maintenance lock and
requires the current runtime-control scripts. Its bounded commands are:

```bash
sudo /opt/sub2api/scripts/azure-caddy-listeners.sh status
sudo /opt/sub2api/scripts/azure-caddy-listeners.sh stage
sudo /opt/sub2api/scripts/azure-caddy-listeners.sh rollback
sudo /opt/sub2api/scripts/azure-caddy-listeners.sh commit
sudo /opt/sub2api/scripts/verify-transport.sh azure
```

Do not start a blue-green application release while the listener transaction
is present: the server release, blue-green helper, and runtime guard all fail
closed on both the Taiwan listener transaction and the older customer-Host
transaction. The two Caddy transactions are mutually exclusive at their
respective stage/prepare boundaries. Finish the transport review, then either
rollback or commit the retained listener transaction before an application
release. All Caddyfile writers preserve the existing file-bind inode; the
Azure verifier also requires host/container inode and SHA equality, validates
the adapted startup JSON, validates the live admin-API JSON, and requires both
security-relevant fingerprints to match. Its HTTP probes explicitly bypass
ambient proxy variables, so `--resolve` is an actual direct-IP canary.

## Qualification evidence

The 2026-09-02 off-peak diagnostic compared the Premium address with a same-zone
Standard Tier control from exact matching 17CE non-IDC nodes in Beijing,
Shanghai, and Guangdong. Each HTTP result verified the destination IP, HTTP
status, origin size, 200 KiB provider sample size, and sampled-body MD5; each
PING used 10 packets.

| Carrier | Premium median HTTP rate vs Standard | Premium vs Standard median PING | Loss |
| --- | ---: | ---: | ---: |
| China Telecom | +217.6% | 48.7 ms vs 268.1 ms | 0% / 0% |
| China Unicom | +392.5% | 43.1 ms vs 285.0 ms | 0% / 0% |
| China Mobile, two-window aggregate | +8.6% | 66.3 ms vs 69.7 ms | 0% / 0% |

For Mobile, Premium improved the aggregate HTTP floor by 52.1% and the worst
PING by 74.4%, but per-node HTTP throughput varied between adjacent windows.
The candidate is therefore qualified for retention and further line-server
work, not yet for a broad production or marketing claim of uniformly faster
three-carrier throughput.

The Standard control instance/address, public HTTP/ICMP test firewall, Nginx
test endpoint, and payload were removed after testing. The retained instance's
test metadata and tag were also removed before it was stopped.

## Production cutover gates

The transport candidate is live for isolated `--resolve` canaries, but public
DNS remains blocked until all of these pass on one frozen snapshot:

1. Bind every active OpenAI OAuth account to one of the two verified fixed
   Tailnet SOCKS egress gateways through the authenticated admin API with a
   compare-and-set expected proxy ID. No raw database update is allowed.
2. Recreate the Azure application with the exact retained image through the
   audited blue-green release so its traffic/background bind mounts have fresh
   inodes. Azure must become the sole background owner before DNS moves. Keep
   the old origin in `traffic=accepting ... background=standby`; verify both
   host state and the state seen inside its active container.
3. Pass authenticated basic generation, Responses streaming/continuation, and
   image behavior through the exact GCP address without printing credentials.
4. Pass independent QA, repository review, and the requested read-only Claude
   review on the same file hashes and evidence snapshot.
5. Record the complete Cloudflare record set and proxy status. The public
   baseline is A `206.119.172.211`, TTL 30, with no AAAA, CNAME, SVCB, or HTTPS
   record. Confirm that control-plane state, then change only the
   `api.turtleligpt.com` A record.
6. Keep the old origin healthy through propagation and drain. Repeat the fixed
   mainland carrier roster during evening peak before making a broad
   "three-carrier optimized" claim.

Fast traffic rollback is the single A-record change back to
`206.119.172.211`; GCP and Azure stay running while that answer propagates.
Stopping the GCP instance or removing its exact ingress tag is a secondary
containment action, not a substitute for DNS rollback.

The old origin's explicit rollback-ready fence keeps user HTTP enabled while
stopping new shared leases and OAuth refresh/queue work. On the currently
installed legacy helper this is `drain` followed by `preflight`; the reviewed
helper exposes the equivalent single `rollback-standby` command. Do not
reactivate the old background owner unless Azure has first been conclusively
fenced, because DNS rollback alone must never create two shared-work owners.

Google Premium Tier keeps traffic on Google's network for more of the path;
that behavior is not itself a carrier-specific SLA. See Google's
[Network Service Tiers overview](https://cloud.google.com/network-tiers/docs/overview)
and [tier configuration documentation](https://cloud.google.com/network-tiers/docs/set-network-tier).
