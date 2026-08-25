# Sub2API project operations

## Production host

- Connect only through the configured SSH alias `sub2api-new`; do not copy a
  raw host, port, or key into project scripts.
- This server is shared only with Turtle's GPT. Sub2API owns `/opt/sub2api`,
  its `sub2api*` containers, volumes, images, release logs, and loopback ports;
  do not inspect, modify, restart, prune, or reuse Turtle's GPT resources from
  a Sub2API task. Keep both projects' deployment directories, Compose projects,
  containers, volumes, ports, reverse-proxy sites, backups, and rollback paths
  isolated.
- Before any remote action, read `deploy/README.md` and inspect the root-owned
  release configuration read-only. Do not assume the production branch or
  active container from local state.

## Windows desktop test client

- Connect only through the configured SSH alias `turtle-windows`; do not copy
  its address, port, credentials, or relay details into repository files.
- This host is an authorized real-client canary source for Sub2API HTTP/WS and
  attachment tests. Keep work inside a dedicated temporary test directory and
  do not install, remove, or change unrelated desktop software or user files.
- Never print or copy Codex/API credentials. Use the desktop's existing client
  configuration and report only numeric Sub2 IDs plus privacy-safe byte/count,
  cache, timing, status, and transport metrics.

## Release boundary

- The documented production path is the explicitly dispatched blue-green
  release in `deploy/README.md`; ordinary pushes and tags must not start
  GitHub Actions. GitHub Actions builds and packages the exact `main` commit;
  production may only validate and load that image before the blue-green
  switch, not fetch source or compile it. Application state lives under
  `/opt/sub2api`, release configuration under
  `/etc/sub2api-autodeploy.env`, and logs under `/var/log/sub2api-release/`.
- A local test, build, or report does not authorize an SSH session, push,
  release, production configuration change, Caddy change, or cache deletion.
- Before a release, resolve the configured production repository and branch,
  confirm the current active container is healthy, and retain the documented
  automatic rollback path.
- Every planned database cutover, application-container lifecycle change, and
  Caddy upstream change must hold the shared production maintenance lock
  documented in `deploy/README.md`. Do not issue a raw `docker stop`,
  `docker restart`, or Compose lifecycle command against production application
  containers outside that boundary. Emergency recovery goes through
  `sub2api-runtime-guard.service`.
- Production may temporarily use `sub2api-blue`, `sub2api-green`, and the
  legacy `sub2api` name at the same time. Long-lived Responses WebSockets can
  keep an old color draining across a later release. Resolve the active color
  from Caddy and select only an absent or stopped target; never assume a fixed
  two-name toggle or stop a running drain container to free a name.
- Attachment Gateway releases must keep the feature disabled by default. Any
  canary enablement must be scoped to explicitly approved API-key, user, or
  group IDs; `allow_unscoped` stays `false` and Caddy limits are a separate
  change.

## Upstream update scope

- Classify upstream updates against enabled production configuration, existing
  traffic, and locally owned behavior before expanding feature review. An
  upstream capability that is not enabled or used in production, and cannot
  alter an existing path through migrations, defaults, shared serialization,
  or cache materialization, should pass the normal merge, build, migration,
  and release gates without local feature repairs.
- Add compatibility work only when a change reaches an enabled production path
  or a locally owned optimization, or when repository evidence shows that an
  otherwise unused feature changes existing behavior through a shared boundary.
  Record the existing path that justifies the added work.
