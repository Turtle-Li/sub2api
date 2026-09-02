# Taiwan ingress review disposition

Task: `SUB2-TW-CUTOVER-20260902`

Verdict before final frozen re-review: **transport implementation ready;
production DNS cutover remains blocked**.

Frozen implementation revision:
`89e202866dce5ef52889ab332a51617c70cb7780`.

## Review findings and disposition

| Finding | Disposition |
| --- | --- |
| Azure verifier could pass a detached or ineffective startup file | Fixed. It requires host/container SHA and device:inode equality, structurally verifies adapted startup JSON and live admin JSON, and requires equal security-contract fingerprints. |
| Client-IP handling still trusted application-prioritized spoofable headers | Fixed. Both production reverse proxies set XFF from Caddy's PROXY-restored peer and delete `X-Real-IP`/`CF-Connecting-IP`; the renderer, JSON verifier, live fingerprints, and forged-header tests enforce it. |
| A prior catch-all route could shadow the reviewed production route | Fixed. The production route must be terminal and every prior route must be provably host-exclusive of `api.turtleligpt.com`; a negative catch-all fixture is covered. |
| Blue-green Caddy writes had no durable crash recovery | Fixed. A durable transaction precedes mutation, normal failures restore host/container/live views, SIGKILL recovery is conservative, and all other Caddy mutators are fenced. |
| HAProxy post-update verification was outside rollback | Fixed and live-proved. Runtime verification now shares the update transaction, the immutable origin backup survives repeated updates, and a forced verifier failure restored/reloaded the previous config. |
| HAProxy retained root privileges and lacked a safe live update | Fixed. Debian's `haproxy` user/group, `/var/lib/haproxy` chroot, retryable recovery state, and seamless reload are live-verified. |
| Bootstrap failure/idempotency and Debian mirror handling were incomplete | Fixed. Success/failure markers, healthy rerun behavior, exact root-owned official mirror handling, and the installed package revision are verified; one-shot metadata was removed. |
| Ambient proxy variables could invalidate pinned direct canaries or local Caddy control checks | Fixed. Host, container, GCE metadata, customer-Host Admin, and drain-stop decision calls now explicitly bypass proxy variables at the relevant boundary; static and behavioral drain tests enforce the contract. |
| Manual HAProxy update could silently omit the runtime verifier | Fixed. The updater defaults to its root-owned sibling verifier, and the GCE metadata path still pins it explicitly inside the rollback transaction. |
| Blue-green recovery could leave the Caddy startup bind writable after a nested restore failure | Fixed. EXIT cleanup performs a final read-only remount sweep, reports persistent failure, and retains the durable transaction for the next conservative recovery. |
| Caddy automatic HTTPS was suspected to invalidate the one-server Admin JSON verifier | Retracted after live and source-level verification. Admin `/config/` exposes raw config, while automatic redirects are added only to the provisioned in-memory app; the separate live HTTP 308 canary covers redirect behavior. |
| Old-origin legacy state writes detached Docker single-file binds | Fixed on the old origin without migrating its lock contract. An exact-digest transformer changed only `write_state`, a no-op transition preserved inodes, and a same-image blue-green release rebuilt current mounts. Active host/container inodes match; 5xx remained zero. |
| Transport tests were absent from CI and stream/TLS evidence was overstated | Fixed where evidence exists. The exact shell CI job passes, certificate fingerprints are compared, and unauthenticated 401 rows explicitly say no stream was carried. Authenticated streaming remains a gate. |
| DNS baseline omitted other record types and control-plane state | Partially fixed. Authoritative DNS proves A `206.119.172.211`, TTL 300, and no AAAA/CNAME/SVCB/HTTPS. Cloudflare proxy status and action-time owner confirmation remain required. |
| Review snapshot was not durable | Fixed. The implementation is committed at the frozen revision above; the checksum manifest covers the implementation/evidence corpus, including the drain controller and its behavioral test, and excludes unrelated user-owned optimizer edits. |

## Transaction and ownership state

The older customer-Host transaction was previously resolved at its exact hash.
Azure now retains only the Taiwan listener transaction; its live Caddyfile is
`ee59c226...`, host/container inode is `66305:265615`, and adapted/live
security fingerprints both equal `8a9e0879...`. The customer-Host and
blue-green transactions are absent.

The old origin is intentionally `traffic=accepting`, `background=active` and
is the sole background owner while production DNS still targets it. Its active
container is healthy and sees the same traffic/background inodes as the host.
Azure is `traffic=accepting`, `background=standby`; its runtime guard and
autodeploy timers remain disabled until its application is safely recreated.

## Remaining production gates

1. Create authenticated fixed-egress proxy records and compare-and-set bind the
   two still-unbound active OAuth accounts; no raw SQL.
2. Commit the retained listener transaction, recreate the Azure application
   from the exact retained image, prove current state-file bind inodes, then
   transfer sole background ownership from old to Azure under the maintenance
   locks.
3. Prove the runtime custom forwarded-IP header list is empty through the
   authenticated admin API, then run basic generation, Responses
   continuation/streaming, and image canaries through the exact GCP address
   without exposing credentials.
4. Confirm Cloudflare proxy status and obtain action-time confirmation before
   changing the A record. Keep the old origin online as the rapid DNS rollback
   target.
5. Complete independent final review and the requested Claude review against
   the same frozen revision and checksum manifest.

No remaining gate is silently treated as passed. DNS remains on the old
origin.
