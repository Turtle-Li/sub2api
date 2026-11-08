#!/usr/bin/env bash
# Run via the approved project SSH alias after the matching application release.
# Upload the three exact reviewed files into a dedicated /tmp directory first.
set -euo pipefail

incoming=$1
previous_sha=$2
html_sha=$3
module_sha=$4
css_sha=$5
[[ "$incoming" =~ ^/tmp/sub2api-model-groups\.[a-zA-Z0-9]+$ ]]
for digest in "$previous_sha" "$html_sha" "$module_sha" "$css_sha"; do [[ "$digest" =~ ^[a-f0-9]{64}$ ]]; done

source /opt/sub2api/scripts/sub2api-maintenance-lock.sh
sub2api_maintenance_lock_open "$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE"
flock -w 20 8 || { echo 'Production maintenance is busy' >&2; exit 1; }

caddy=sub2api-candidate-caddy
home=/data/sub2-web/home
version=turtleroute-status-20261108-v7
assets=/data/sub2-web/help/$version
backup="$home/index.before-status-probe-metrics-20261108.html"
[[ $(docker inspect --format '{{.State.Status}}' "$caddy") == running ]]
read_remote_sha() { docker exec "$caddy" sha256sum "$1" | awk '{print $1}'; }
read_origin_sha() {
  curl -fsS --max-time 8 --resolve www.turtleligpt.com:443:127.0.0.1 "https://www.turtleligpt.com$1" | sha256sum | awk '{print $1}'
}
[[ "$(read_remote_sha "$home/index.html")" == "$previous_sha" ]]
[[ "$(read_origin_sha /)" == "$previous_sha" ]]
curl -fsS --max-time 15 --resolve www.turtleligpt.com:443:127.0.0.1 https://www.turtleligpt.com/api/v1/model-status |
  python3 -c 'import json,sys; p=json.load(sys.stdin); d=p.get("data",{}); assert p.get("code")==0 and any((g.get("availability") or {}).get("window_minutes")==180 for g in d.get("groups",[])) and all((g.get("availability") or {}).get("cache_scope")=="shared_account_pool" and (g.get("availability") or {}).get("cache_statistic")=="best_complete_hour" and (g.get("availability") or {}).get("cache_lookback_hours")==24 and (g.get("availability") or {}).get("cache_read_rate") is None and (g.get("availability") or {}).get("probe_metrics_version")==1 for g in d.get("groups",[]))'

evidence=$(mktemp -d /var/log/sub2api-release/model-groups-20261107.XXXXXX)
for name in index.html group-availability.mjs group-availability.css; do install -m 0644 "$incoming/$name" "$evidence/$name"; done
printf '%s  %s\n' "$html_sha" "$evidence/index.html" "$module_sha" "$evidence/group-availability.mjs" "$css_sha" "$evidence/group-availability.css" | sha256sum --check --status
docker cp "$caddy:$home/index.html" "$evidence/index.previous.html"
printf '%s  %s\n' "$previous_sha" "$evidence/index.previous.html" | sha256sum --check --status

suffix=${evidence##*.}
staged_html="$home/.index.model-groups-$suffix.html"
staged_assets="/data/sub2-web/help/.$version.$suffix"
cleanup() { docker exec "$caddy" rm -f "$staged_html"; }
trap cleanup EXIT
docker cp "$evidence/index.html" "$caddy:$staged_html"
[[ "$(read_remote_sha "$staged_html")" == "$html_sha" ]]
if docker exec "$caddy" test -d "$assets"; then
  # A prior failed publication may have left these exact immutable assets.
  [[ "$(read_remote_sha "$assets/group-availability.mjs")" == "$module_sha" ]]
  [[ "$(read_remote_sha "$assets/group-availability.css")" == "$css_sha" ]]
else
  docker exec "$caddy" mkdir "$staged_assets"
  docker cp "$evidence/group-availability.mjs" "$caddy:$staged_assets/group-availability.mjs"
  docker cp "$evidence/group-availability.css" "$caddy:$staged_assets/group-availability.css"
  [[ "$(read_remote_sha "$staged_assets/group-availability.mjs")" == "$module_sha" ]]
  [[ "$(read_remote_sha "$staged_assets/group-availability.css")" == "$css_sha" ]]
  docker exec "$caddy" mv "$staged_assets" "$assets"
fi
[[ "$(read_origin_sha "/helpcenter/$version/group-availability.mjs")" == "$module_sha" ]]
[[ "$(read_origin_sha "/helpcenter/$version/group-availability.css")" == "$css_sha" ]]
if docker exec "$caddy" test -e "$backup"; then
  [[ "$(read_remote_sha "$backup")" == "$previous_sha" ]]
else
  docker exec "$caddy" cp -p "$home/index.html" "$backup"
fi
docker exec "$caddy" mv -f "$staged_html" "$home/index.html"
if [[ "$(read_origin_sha /)" != "$html_sha" ]]; then
  docker exec "$caddy" cp -p "$backup" "$staged_html"
  docker exec "$caddy" mv -f "$staged_html" "$home/index.html"
  [[ "$(read_origin_sha /)" == "$previous_sha" ]]
  echo 'Origin did not serve new homepage; prior HTML restored' >&2
  exit 1
fi
printf 'homepage=%s module=%s stylesheet=%s rollback=%s evidence=%s\n' "$html_sha" "$module_sha" "$css_sha" "$backup" "$evidence"
