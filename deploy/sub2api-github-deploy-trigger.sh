#!/usr/bin/env bash

# This is an SSH forced-command handler for GitHub Actions. The corresponding
# key has no shell, port-forwarding, or agent-forwarding privileges. It can
# only start the root-owned one-shot release service.

set -Eeuo pipefail

if [ "${SSH_ORIGINAL_COMMAND:-}" != "deploy" ]; then
  echo "Only the 'deploy' command is permitted." >&2
  exit 2
fi

exec /usr/bin/sudo -n /usr/bin/systemctl start sub2api-autodeploy.service
