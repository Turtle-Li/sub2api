# Runtime guard rejected approved Vault mounts

## Symptom and impact

`sub2api-runtime-guard.timer` was intentionally left disabled after an older
single-file bind-mount inode incident. On 2026-09-17, the active production
container was healthy and its traffic/background state inodes and contents
matched the host, but a documented manual guard run still exited before any
lifecycle action with:

```text
application runtime verification failed before lifecycle action: sub2api-blue
ERROR: active application runtime does not match the configured dependency and dual-node contract
```

The public health endpoint remained HTTP 200, the active container remained
healthy with zero restarts and no OOM, and the timer was not enabled after this
failed rehearsal. `sub2api-autodeploy.timer` is a separate legacy polling
mechanism and remains disabled by design.

## Root cause

The historical failure had one real cause: older node-state writes replaced a
single-file inode while Docker retained the old bind mount. Current
`sub2api-node-state.sh` writes an existing state file in place, and the current
active container has matching host/container state-file identities.

The new rehearsal exposed a second, independent contract drift. The canonical
blue-green release helper permits two approved read-only socket volumes when
their features are enabled:

- `sub2api_unified_payment_vault` at `/run/sub2api-payment-vault`;
- `sub2api_feishu_vault` at `/run/sub2api-feishu-vault`.

The runtime guard still required exactly the data, two CA, and three dual-node
runtime mounts. It therefore rejected the two valid Vault mounts before it
could reach the live inode/content checks. This was fail-closed and caused no
container or traffic mutation, but it made the recovery timer unusable.

## Repair contract

The guard must derive `UNIFIED_PAYMENT_ENABLED` and
`SUB2API_FEISHU_ENABLED` from the inspected container, reject duplicate or
invalid values, and reject legacy raw secret/webhook environment variables.
For each enabled feature it must require the exact approved read-only volume,
source, and target. A disabled or absent feature normally must have no mount at
that target. The sole compatibility exception is the repository's default
local, non-dual Compose topology: because that Compose file always declares
the public payment socket volume, it may retain zero or one exact
`sub2api_unified_payment_vault` to `/run/sub2api-payment-vault` read-only mount
while payment is disabled. The feature flag remains authoritative, so the
dormant mount does not enable payment or expose a raw key to the application.
External or dual-node deployments receive no such exception, and a disabled
Feishu feature never permits a residual mount.

Exact total mount counting remains in force so duplicate targets and unknown
mounts fail closed. Wrong sources, targets, or access modes fail closed in the
local exception as well. Raw private-key and webhook environment values remain
forbidden in every topology. Apart from the narrowly documented dormant
payment mount, the same feature contract applies in local and external
dependency modes and with or without the dual-node runtime state mounts; no
otherwise healthy single-node container may bypass it.

Tests cover both approved volumes together, missing/wrong volumes, disabled
features with residual volumes, duplicate/invalid switches, the exact dormant
local Compose payment mount with both false and absent switches, rejected
wrong/read-write/duplicate dormant mounts, local single-node feature and
unknown-mount cases, and the existing state inode/content drift cases.

The frozen source checks include `deploy/tests/runtime-guard-test.sh`,
`deploy/tests/node-state-test.sh`,
`deploy/tests/install-autodeploy-runtime-mode-test.sh`, Bash syntax validation,
ShellCheck, and `git diff --check`. They all pass on the repair source.

## Safe rollout

1. Pass source QA and review for the guard and its tests.
2. Install the exact reviewed script while leaving the timer disabled.
3. Confirm the active runtime's mount/env contract and state-file identities.
4. Start `sub2api-runtime-guard.service` once under its own maintenance lock.
5. Require a successful no-op result and HTTP 200 health before enabling
   `sub2api-runtime-guard.timer`.
6. Observe scheduled runs, then confirm the timer is active and the separate
   `sub2api-autodeploy.timer` is still disabled.

Runtime enablement evidence is recorded in the private infrastructure registry
after these gates complete so the repository release commit stays immutable.
