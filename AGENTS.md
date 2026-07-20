# Sub2API project operations

## Production host

- Connect only through the configured SSH alias `sub2api-new`; do not copy a
  raw host, port, or key into project scripts.
- This server is dedicated to Sub2API. Do not install, deploy, clean up, or
  debug unrelated applications, databases, games, or temporary services on it.
- Before any remote action, read `deploy/README.md` and inspect the root-owned
  release configuration read-only. Do not assume the production branch or
  active container from local state.

## Release boundary

- The documented production path is the event-driven blue-green release in
  `deploy/README.md`, with application state under `/opt/sub2api`, release
  configuration in `/etc/sub2api-autodeploy.env`, and logs under
  `/var/log/sub2api-release/`.
- A local test, build, or report does not authorize an SSH session, push,
  release, production configuration change, Caddy change, or cache deletion.
- Before a release, resolve the configured production repository and branch,
  confirm the current active container is healthy, and retain the documented
  automatic rollback path.
- Production may temporarily use `sub2api-blue`, `sub2api-green`, and the
  legacy `sub2api` name at the same time. Long-lived Responses WebSockets can
  keep an old color draining across a later release. Resolve the active color
  from Caddy and select only an absent or stopped target; never assume a fixed
  two-name toggle or stop a running drain container to free a name.
- Attachment Gateway releases must keep the feature disabled by default. Any
  canary enablement must be scoped to explicitly approved API-key, user, or
  group IDs; `allow_unscoped` stays `false` and Caddy limits are a separate
  change.
