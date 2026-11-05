#!/usr/bin/env bash
set -euo pipefail
source /opt/sub2api/scripts/sub2api-maintenance-lock.sh
sub2api_maintenance_lock_open "$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE"
flock -n 8 || { echo 'Production maintenance is busy' >&2; exit 1; }
exec python3 -I /opt/sub2api/staging/shop-daypass-concurrency-limit.py "$@"
