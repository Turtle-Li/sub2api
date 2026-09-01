# Taiwan Premium ingress live validation

Task: `SUB2-TW-CUTOVER-20260902`

Checkpoint: 2026-09-02 07:05 Asia/Shanghai

Verdict: **transport candidate PASS; production DNS cutover BLOCKED**. The
public A record remains `206.119.172.211` and the old origin remains the sole
active background owner.

Frozen implementation revision:
`9585e61bcf5a958c1b6aa609efacb4d6bd2c9908`.

## Frozen topology

- GCP `130.211.243.139`: Debian HAProxy `2.6.12-1+deb12u3`, config SHA-256
  `e6891a35c90702d6df139f231a2d842c7eaa66b7148f354b730a1a86131d1601`,
  TCP transport only, Premium Tier, no service account, no retained startup
  metadata.
- Azure `4.216.216.16`: Caddy `v2.11.4` terminates TLS and accepts PROXY v2
  only from the exact GCP `/32`; ordinary direct TLS remains enabled. The
  Caddyfile SHA-256 is
  `ee59c226f5679464828a07eea013bc8f58054258af1da40bbf8f2cd89d8d4715`.
- Old `206.119.172.211`: unchanged production DNS and rollback origin. Its
  exact current application image was moved through a same-image blue-green
  release after the legacy state writer was hotfixed; active host/container
  bind inodes now match.
- No database schema, PostgreSQL, Redis, application image, OAuth binding, or
  Cloudflare record was changed by this transport checkpoint.

## Evidence

| Gate | Result |
| --- | --- |
| Exact macOS CI shell job | PASS: lifecycle, release, lock, runtime guard, blue-green, certificate, fixed-egress, node-state, Compose, Caddy, transport, and ShellCheck gates |
| GCP resource identity | `RUNNING`, retained Premium static IP, exact TCP `80/443` rule/tag, no service account or retained metadata |
| HAProxy local contract | `GCP_TRANSPORT_VERIFY_PASS`; unprivileged `haproxy` worker and `/var/lib/haproxy` chroot |
| HAProxy rollback injection | Forced post-update verifier failure emitted `GCP_UPDATE_FAIL`, restored/reloaded the exact pre-update config, and kept the public canary healthy |
| HAProxy final update | `GCP_HAPROXY_UPDATED ... config_sha=e6891a35...` then `GCP_UPDATE_PASS` |
| Public GCP health/models | HTTP 200 / 401 |
| HTTP redirect through GCP | HTTP 308 to the HTTPS hostname |
| TLS identity through GCP | Direct-Azure/GCP SHA-256 certificate fingerprint equality |
| HTTP protocols | h1/h2 only; no h3 advertisement on direct Azure or GCP |
| Direct Azure fallback | HTTP 200 health and HTTP 401 unauthenticated models |
| Forged PROXY v2 from an untrusted source | TLS handshake rejected |
| Client-IP header boundary | Both production reverse proxies replace XFF from the PROXY-restored peer and delete `X-Real-IP`/`CF-Connecting-IP`; forged-header and route-shadow tests pass |
| Azure effective Caddy runtime | Host/container device:inode `66305:265615`; adapted startup and live admin fingerprints both `8a9e08798e183dc4566fb7a72bd357ca4ad54a13d84d7fb5d20fd3336d75dd4a` |
| Azure transactions | Taiwan listener transaction retained for exact rollback; customer-Host and blue-green transactions absent; all other Caddy mutators are fenced |
| Durable blue-green transaction | Regression tests cover normal rollback, retained SIGKILL recovery, clean retry, and mutation fencing |
| Old-origin legacy hotfix | Exact source `421082e4...` transformed to `2c53593a...`; no-op `activate` preserved state inodes |
| Old-origin bind recovery | Same image `0.1.185` switched green→blue through the audited release; active traffic/background host and container inodes match, inactive stale generation drained and stopped |
| Old-origin live result | Health 200, models 401, active container healthy, traffic accepting, background active, release window app/Caddy 5xx counts 0 |
| Production DNS | Unchanged authoritative A `206.119.172.211`, TTL 300; no AAAA, CNAME, SVCB, or HTTPS answer |

Unauthenticated Responses SSE/WebSocket probes returned 401 as expected; no
stream or upgraded connection was carried, so those rows are reachability
checks rather than functional streaming evidence.

## Accepted defects and repairs

- The application prioritizes inbound `CF-Connecting-IP` and `X-Real-IP` over
  XFF. Caddy's safe XFF default was therefore insufficient by itself. The
  renderer now adds an explicit three-header policy to both production
  reverse proxies, and the effective-JSON verifier requires that exact policy.
- A structurally valid production route could previously be shadowed by an
  earlier catch-all. The verifier now requires the production route to be
  terminal and every prior route to be provably exclusive of the production
  hostname.
- HAProxy's runtime verifier was previously outside the update rollback
  branch. It now participates in the same transaction and retains an immutable
  origin backup across updates; the controlled live failure above proved the
  restore path.
- An interrupted blue-green Caddy mutation previously had no durable recovery
  authority. The switch now writes a transaction before its first mutation,
  restores all three Caddy views on ordinary failure, and conservatively
  recovers a retained transaction on the next run.
- Transport probes could inherit a shell proxy and accidentally bypass their
  pinned destination. Host curls now use `--noproxy '*'`, container probes
  disable proxy use, and GCE metadata calls bypass proxies explicitly.
- The old origin's legacy node-state helper used rename for existing Docker
  single-file bind mounts. A release reproduced the detached-inode failure.
  The exact-digest compatibility hotfix changed existing-file writes to
  in-place updates while retaining the legacy lock, then a same-image release
  rebuilt the current mounts. The old host is again the single background
  owner.

## Deliberate blockers before DNS

1. Two active OpenAI OAuth accounts still have no fixed proxy binding. Create
   the proxy rows and compare-and-set bindings through the authenticated admin
   API; no raw SQL.
2. The Azure application container still has stale traffic/background
   single-file bind inodes (`620534` vs `620499` and `620513` vs `620521`).
   Commit the retained listener transaction, then recreate the exact retained
   image through the audited blue-green release. Azure remains background
   standby until this is deliberately completed.
3. Run authenticated basic generation, Responses continuation/streaming, and
   image canaries through the exact GCP address without exposing credentials.
   No task-scoped Vault grant was available, so no credential was copied from
   a database, browser, or host file.
4. Confirm Cloudflare control-plane proxy status and obtain action-time owner
   confirmation before changing the A record. The retired POC token remains
   out of scope.
5. Independent final review and the requested Claude final review must accept
   the same frozen revision and checksum manifest.

Until those blockers close, the safe state is: GCP and Azure transport stay
available for isolated canaries, the Azure listener transaction remains
rollback-capable, Azure runtime recovery stays disabled, the old origin keeps
serving production and owns background work, and public DNS remains unchanged.
