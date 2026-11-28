# Caddy config publication misclassified a host-managed proxy

## Impact and evidence

- Of the 12 most recent production dispatches sampled (November 20–28, 2026
  UTC), five attempted the optional Caddy step and failed with `host Caddyfile
  does not uniquely select blue, green or legacy`: runs `44941485173`,
  `45376680080`, `45709877822`, `46027983766`, and `46152977377`. The other seven
  succeeded with that step skipped. At the time `deploy_caddy` defaulted to true.
- The November 28 DayPass run `46152977377` completed the image switch to
  `6edd05806231ac1f286c3353ecd90d10e8b1de75` and failed only in the separate
  Caddy step. The production slot was healthy and the direct `/health` check
  returned `{"status":"ok"}`; the host file, bound startup file and live Admin
  configuration all selected `sub2api-blue:8080` for the application.
- The additional dial belongs to an independently managed exact-path WebSocket
  route added November 10. It lives on the shared API site but is not a Sub2API
  slot. The old receiver treated every JSON `dial` as an app slot. The image
  switch uses the known Sub2API upstreams instead. The November 8 template or
  certificate changes are not what made the slot ambiguous; the Caddy run for
  `c47d7a34e` succeeded before that separate route was added.

## Repair and activation boundary

- Keep the app-only workflow as the default. An explicit Caddy publication
  still requires the same commit, config digest, lock, convergence checks and
  rollback. It carries forward the supported host-managed block from the
  verified original file, including its scope and precedence. Do not put the
  private path or upstream in Git, CI output, or a re-uploaded template.
- Receiver regression tests cover the successful projection, mixed upstreams,
  a missing insertion point, changed host scope, template-injected routes and
  rollback. The pinned Caddy image was used to adapt a synthetic reproduction.
  A read-only AWS projection also matched the entire current Caddy config
  after normalization, with the independent route and the 128 MiB policy
  intact. Neither check installed the receiver or reloaded Caddy.
- Before a future approved Caddy rollout, stage and verify just the reviewed
  `sub2api-caddy-config-release.sh` on the authorized AWS target under the
  canonical maintenance lock (root:root, `0750`), retaining the old helper
  as in the [previous receiver repair](API_CERT_AUTORENEW_20261108.md#activation-evidence).
  Verify no pending transaction and recheck live state before any reload.
  For an intentional edge change, use the exact-revision workflow and check
  direct API, API alias, www/TLS and the separately managed route after release.
  Do not retry the old DayPass run just to turn its historical failure green.
