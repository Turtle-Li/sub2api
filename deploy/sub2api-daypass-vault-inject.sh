#!/usr/bin/env bash
# Bounded Vault consumer: the machine bearer travels only through SSH stdin.
set -Eeuo pipefail
set +x
[ "$#" = 1 ] || exit 64
image="$1"
[[ "$image" =~ ^sub2api:prebuilt-[a-f0-9]{40}$ ]] || exit 64
bearer="${DAYPASS_SHOP_BEARER-}"
unset DAYPASS_SHOP_BEARER SHOP_DAYPASS_BEARER
trap 'bearer=""; unset bearer DAYPASS_SHOP_BEARER SHOP_DAYPASS_BEARER' EXIT
[[ "$bearer" =~ ^[a-f0-9]{64}$ ]] || exit 64
printf '%s\n' "$bearer" | ssh -o BatchMode=yes -o StrictHostKeyChecking=yes sub2api-aws-candidate \
  "sudo -n bash /opt/sub2api/scripts/sub2api-daypass-vault-container.sh load $image"
