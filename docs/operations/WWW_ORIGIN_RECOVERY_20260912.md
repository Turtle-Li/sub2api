# WWW origin recovery — 2026-09-12

## Cause and scope

The earlier migration moved API DNS and application/background ownership only.
`www.turtleligpt.com` still depended on the expired old origin. Public `/health`
returned Cloudflare 522; the Azure Caddy had no www site and its external
certificate covered only api.turtleligpt.com. Public API health remained 200.
The owner changed Cloudflare DNS to Taiwan `130.211.243.139` during this task
and authorized server-side Sub2 recovery. No chat/Turtle GPT resource was touched.

## Serving topology

www.turtleligpt.com -> Cloudflare -> GCP Taiwan TCP 80/443 -> Azure
`sub2api-candidate` -> `sub2api-candidate-caddy` -> current Sub2 app color.
API remains on its existing path. The old `sub2api-new` alias is NOT the active
web/application origin and must not be selected as a fallback after expiry.

- `/opt/sub2api/Caddyfile` now has a dedicated www block, static `/` and
  `/helpcenter` handling, and application fallback to `sub2api-green:8080`.
- Static files are persisted in existing Caddy data volume
  `sub2api-candidate-caddy-data`, container paths `/data/sub2-web/home` and
  `/data/sub2-web/help`; no container recreation or extra listener was needed.
- Homepage bytes were recovered from this task's successful public 200 response:
  SHA256 `c5b06aa5d590e978aeb883944ba8c40cd4755362cc573e66e8b6f6d13c42fe1a`.
  Local older homepage was intentionally not substituted. Help-center HTML,
  logo and favicon were recovered from deployment-kit local files.
- www uses Caddy-managed Let's Encrypt HTTP-01, TLS-ALPN disabled because the
  public host is proxied by Cloudflare. TCP80 and the ACME challenge path must
  remain reachable. Certificate SAN www.turtleligpt.com; valid until
  2026-12-11 02:37:07 UTC. Certificate/private key remain in the protected Caddy
  data volume; never copy them into Git. Existing API external certificate unchanged.
- The canonical blue-green helper globally replaces the selected old upstream
  with the new one, so the new web proxy follows subsequent app-color switches.
  Do not replace the live Caddyfile with the API-only example template.

## Change and evidence

Operation held `/opt/sub2api/scripts/sub2api-maintenance-lock.sh`'s canonical
lock. Caddyfile was modified in place with fsync to preserve the bind inode;
Caddy was gracefully reloaded, application/Caddy containers not restarted.
Original routes were compared structurally, preserving matcher groups while
normalizing only Caddy-generated group identifiers. A first strict literal
comparison detected renumbering and safely rolled back; after confirming the
only difference was numbering, the semantic comparison and reload passed.

Remote backup/evidence: `/var/log/sub2api-release/www-migration-20260912/`.
Final Caddyfile SHA256:
`18cea556d49367f60b0e95b90f04ba2ed44abb1802386dc415da4132fbd147cf`.
Local operational evidence: deployment kit `evidence/www-migration-20260912/`.
Public www health and Taiwan-pinned login returned 200; API health returned 200.
Pinned homepage exactly matches recovered bytes. Certificate acquisition passed.

## Remaining asset limitation

The recovered historical homepage links to the old TT Switch 0.1.0 canary DMG,
checksum and demo under `/helpcenter/downloads/tt-switch/canary/macos/3ba1dbbc.../`.
Those old downloadable files were not present in the local help-center source;
DMG URL returns 404. Do not relabel a different installer version as that file.
Recover the exact artifacts or update links in the separately owned desktop
website task. Login, app/purchase routes, health and API do not depend on them.

## Rollback boundary

The previous Caddyfile is retained remotely. Restoring it under the maintenance
lock and reloading preserves original API behavior but removes www again;
therefore use only for a demonstrated new proxy regression. Do not restore DNS
to the expired old server. Preserve the Caddy data volume and static files.
