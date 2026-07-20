#!/usr/bin/env bash

# Build a source tree that was prepared by sub2api-autodeploy.sh, then release
# it through the existing blue-green switch.  It keeps the build and release
# lock on the server so timer runs and manual releases cannot overlap.

set -Eeuo pipefail

if [ "$#" -ne 6 ]; then
  echo "Usage: sub2api-server-release.sh SOURCE_DIR IMAGE COMMIT VERSION HEALTH_URL RUN_ID" >&2
  exit 2
fi

SOURCE_DIR="$1"
IMAGE="$2"
COMMIT="$3"
VERSION="$4"
PUBLIC_HEALTH_URL="$5"
RUN_ID="$6"

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
WORK_ROOT="${SUB2API_AUTODEPLOY_WORK_ROOT:-/var/lib/sub2api-autodeploy/worktrees}"
BLUE_GREEN_SCRIPT="${APP_DIR}/scripts/sub2api-blue-green-release.sh"
LOG_ROOT="${SUB2API_RELEASE_LOG_DIR:-/var/log/sub2api-release}"
LOG_DIR="${LOG_ROOT}/${RUN_ID}"
BUILD_LOG="${LOG_DIR}/build.log"
SWITCH_LOG="${LOG_DIR}/switch.log"
LOCK_FILE="${SUB2API_RELEASE_LOCK_FILE:-/var/lock/sub2api-release.lock}"
MIN_FREE_BYTES="${SUB2API_RELEASE_MIN_FREE_BYTES:-8589934592}"
BUILD_TIMEOUT_SECONDS="${SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS:-3000}"
CADDY_CONTAINER="${SUB2API_CADDY_CONTAINER:-sub2api-caddy}"

timestamp() {
  date '+%Y-%m-%d %H:%M:%S'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

die() {
  log "ERROR: $*" >&2
  log "Server logs: ${LOG_DIR}" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_positive_integer() {
  case "$2" in
    ''|*[!0-9]*) die "$1 must be a positive integer" ;;
  esac
  [ "$2" -gt 0 ] || die "$1 must be a positive integer"
}

for command_name in docker curl flock grep awk timeout perl; do
  require_cmd "$command_name"
done
require_positive_integer SUB2API_RELEASE_MIN_FREE_BYTES "$MIN_FREE_BYTES"
require_positive_integer SUB2API_RELEASE_BUILD_TIMEOUT_SECONDS "$BUILD_TIMEOUT_SECONDS"

case "$SOURCE_DIR" in
  "${WORK_ROOT%/}"/*) ;;
  *) die "refusing source outside automatic-release work root: $SOURCE_DIR" ;;
esac
case "$IMAGE" in
  sub2api:auto-*) ;;
  *) die "refusing unexpected image tag: $IMAGE" ;;
esac
case "$COMMIT" in
  *[!0-9a-f]*|'') die "invalid commit: $COMMIT" ;;
esac
case "$RUN_ID" in
  *[!A-Za-z0-9._-]*|'') die "invalid release run id" ;;
esac

mkdir -p "$LOG_DIR"
exec 9>"$LOCK_FILE"
flock -n 9 || die "another production release is already running"

[ -d "$SOURCE_DIR" ] || die "source directory does not exist: $SOURCE_DIR"
[ -f "$SOURCE_DIR/Dockerfile" ] || die "repository Dockerfile is missing"
[ -x "$BLUE_GREEN_SCRIPT" ] || die "blue-green script is missing or not executable"

available_bytes="$(df --output=avail -B1 / | tail -1 | tr -d '[:space:]')"
[ "$available_bytes" -ge "$MIN_FREE_BYTES" ] || die "less than 8 GiB is free on the server"

active_upstream="$(grep -oE 'sub2api(-(blue|green))?:8080' "${APP_DIR}/Caddyfile" | sort -u)"
upstream_count="$(printf '%s\n' "$active_upstream" | sed '/^$/d' | wc -l)"
[ "$upstream_count" -eq 1 ] || die "Caddy upstream is ambiguous: $active_upstream"

OLD_CONTAINER="${active_upstream%:8080}"
# A third color is intentional. Long-lived Responses WebSocket connections can
# keep a previous container draining through the next release, so a strict
# two-name toggle can otherwise leave no safe target. Never recycle another
# running color merely to free a name.
case "$OLD_CONTAINER" in
  sub2api-blue)
    release_candidates=(sub2api-green sub2api)
    ;;
  sub2api-green)
    release_candidates=(sub2api-blue sub2api)
    ;;
  sub2api)
    release_candidates=(sub2api-green sub2api-blue)
    ;;
  *)
    die "unsupported active container: $OLD_CONTAINER"
    ;;
esac

NEW_CONTAINER=""
for candidate in "${release_candidates[@]}"; do
  candidate_running="$(docker inspect "$candidate" --format '{{.State.Running}}' 2>/dev/null || true)"
  if [ "$candidate_running" != "true" ]; then
    NEW_CONTAINER="$candidate"
    break
  fi
done
[ -n "$NEW_CONTAINER" ] || die "no absent or stopped release target; other colors are still draining"

OLD_UPSTREAM="${OLD_CONTAINER}:8080"
NEW_UPSTREAM="${NEW_CONTAINER}:8080"
OLD_IMAGE="$(docker inspect "$OLD_CONTAINER" --format '{{.Config.Image}}' 2>/dev/null || true)"
OLD_RUNNING="$(docker inspect "$OLD_CONTAINER" --format '{{.State.Running}}' 2>/dev/null || true)"
OLD_HEALTH="$(docker inspect "$OLD_CONTAINER" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' 2>/dev/null || true)"
[ "$OLD_RUNNING" = "true" ] || die "active container is not running: $OLD_CONTAINER"
[ "$OLD_HEALTH" = "healthy" ] || die "active container is not healthy: $OLD_CONTAINER ($OLD_HEALTH)"

if docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
  target_running="$(docker inspect "$NEW_CONTAINER" --format '{{.State.Running}}')"
  if [ "$target_running" = "true" ]; then
    die "inactive target ${NEW_CONTAINER} is still running, probably draining a previous release; retry later"
  fi
fi

recent_requests="$(docker logs --since 2m "$OLD_CONTAINER" 2>&1 | grep -c '"component": "http.access"' || true)"
log "Preflight: active=${OLD_CONTAINER} target=${NEW_CONTAINER} recent_requests_2m=${recent_requests}"
log "Preflight: disk_free=$(df -h / | awk 'NR==2 {print $4}') load=$(cut -d' ' -f1-3 /proc/loadavg)"

build_started="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
log "Building ${IMAGE} on the server; detailed output is in ${BUILD_LOG}"
if ! timeout "$BUILD_TIMEOUT_SECONDS" env DOCKER_BUILDKIT=1 docker build \
  --progress=plain \
  --tag "$IMAGE" \
  --build-arg "GOPROXY=https://goproxy.cn,direct" \
  --build-arg "GOSUMDB=sum.golang.google.cn" \
  --build-arg "NPM_CONFIG_REGISTRY=https://registry.npmmirror.com" \
  --build-arg "COMMIT=${COMMIT}" \
  --build-arg "VERSION=${VERSION}" \
  --build-arg "DATE=${build_started}" \
  --file "${SOURCE_DIR}/Dockerfile" \
  "$SOURCE_DIR" >"$BUILD_LOG" 2>&1; then
  tail -100 "$BUILD_LOG" >&2 || true
  die "Docker build failed"
fi

docker image inspect "$IMAGE" >/dev/null || die "built image is missing"
log "Build completed: $(docker image inspect "$IMAGE" --format '{{.Id}} {{.Size}} bytes')"

rollback() {
  log "Public verification failed; attempting automatic rollback to ${OLD_CONTAINER}"
  env \
    OLD_CONTAINER="$NEW_CONTAINER" \
    NEW_CONTAINER="$OLD_CONTAINER" \
    NEW_IMAGE="$OLD_IMAGE" \
    CADDY_UPSTREAM_FROM="$NEW_UPSTREAM" \
    CADDY_UPSTREAM_TO="$OLD_UPSTREAM" \
    PULL_IMAGE=false \
    RUN_BACKUP=false \
    REMOVE_EXISTING_NEW_CONTAINER=false \
    bash "$BLUE_GREEN_SCRIPT" >>"${LOG_DIR}/rollback.log" 2>&1 || {
      tail -100 "${LOG_DIR}/rollback.log" >&2 || true
      log "ERROR: automatic rollback failed; manual intervention is required" >&2
      return 1
    }
  log "Rollback completed"
}

cleanup_failed_inactive_target() {
  local active_config

  if ! docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
    return 0
  fi
  if ! active_config="$(docker exec "$CADDY_CONTAINER" sh -c 'wget -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl -fsS http://127.0.0.1:2019/config/')"; then
    log "WARNING: could not read Caddy configuration; retaining failed target ${NEW_CONTAINER}" >&2
    return 0
  fi
  if ! printf '%s' "$active_config" | grep -qF "$OLD_UPSTREAM" \
    || printf '%s' "$active_config" | grep -qF "$NEW_UPSTREAM"; then
    log "WARNING: Caddy does not conclusively point at ${OLD_UPSTREAM}; retaining ${NEW_CONTAINER}" >&2
    return 0
  fi

  log "Removing failed inactive target ${NEW_CONTAINER}; Caddy still points at ${OLD_CONTAINER}"
  docker rm -f "$NEW_CONTAINER" >>"${LOG_DIR}/failed-target-cleanup.log" 2>&1 \
    || log "WARNING: could not remove failed inactive target ${NEW_CONTAINER}" >&2
}

log "Switching ${OLD_UPSTREAM} to ${NEW_UPSTREAM} through the existing blue-green script"
if ! env \
  OLD_CONTAINER="$OLD_CONTAINER" \
  NEW_CONTAINER="$NEW_CONTAINER" \
  NEW_IMAGE="$IMAGE" \
  CADDY_UPSTREAM_FROM="$OLD_UPSTREAM" \
  CADDY_UPSTREAM_TO="$NEW_UPSTREAM" \
  PULL_IMAGE=false \
  RUN_BACKUP=true \
  bash "$BLUE_GREEN_SCRIPT" >"$SWITCH_LOG" 2>&1; then
  tail -120 "$SWITCH_LOG" >&2 || true
  if docker inspect "$NEW_CONTAINER" >/dev/null 2>&1; then
    if rollback; then
      cleanup_failed_inactive_target
    fi
  fi
  die "blue-green release failed"
fi

if ! curl -fsS --max-time 20 --retry 3 --retry-delay 2 "$PUBLIC_HEALTH_URL" >/dev/null; then
  rollback || true
  die "public health check failed after switch"
fi

NEW_HEALTH="$(docker inspect "$NEW_CONTAINER" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}')"
[ "$NEW_HEALTH" = "healthy" ] || {
  rollback || true
  die "new container lost health after switch: $NEW_HEALTH"
}

if ! active_config="$(docker exec "$CADDY_CONTAINER" sh -c 'wget -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl -fsS http://127.0.0.1:2019/config/')"; then
  rollback || true
  die "could not read the active Caddy configuration after switch"
fi
printf '%s' "$active_config" | grep -qF "$NEW_UPSTREAM" || {
  rollback || true
  die "active Caddy config does not contain $NEW_UPSTREAM"
}
if printf '%s' "$active_config" | grep -qF "$OLD_UPSTREAM"; then
  rollback || true
  die "active Caddy config still contains old upstream $OLD_UPSTREAM"
fi

app_5xx="$(docker logs --since "$build_started" "$NEW_CONTAINER" 2>&1 | grep -Ec '"status_code":[[:space:]]*5[0-9]{2}' || true)"
app_fatal="$(docker logs --since "$build_started" "$NEW_CONTAINER" 2>&1 | grep -Eic 'panic|fatal|redis.*(error|fail|timeout)|database.*(error|fail)' || true)"
caddy_5xx="$(docker logs --since "$build_started" "$CADDY_CONTAINER" 2>&1 | grep -Ec '"status":[[:space:]]*5[0-9]{2}' || true)"

if [ "$app_fatal" -gt 0 ]; then
  docker logs --since "$build_started" "$NEW_CONTAINER" >"${LOG_DIR}/suspicious-app.log" 2>&1 || true
  log "WARNING: found ${app_fatal} suspicious application log lines; review ${LOG_DIR}/suspicious-app.log"
fi

# Retain active and rollback images.  Only old generated release images that
# are no longer referenced by a container are eligible for removal.
generated_index=0
while IFS= read -r old_tag; do
  [ -n "$old_tag" ] || continue
  generated_index=$((generated_index + 1))
  [ "$generated_index" -le 3 ] && continue
  if docker ps -a --format '{{.Image}}' | grep -qxF "$old_tag"; then
    continue
  fi
  docker image rm "$old_tag" >>"${LOG_DIR}/image-cleanup.log" 2>&1 || true
done < <(docker images --format '{{.Repository}}:{{.Tag}}' | grep '^sub2api:auto-' || true)

if docker buildx prune --help 2>&1 | grep -q -- '--max-used-space'; then
  docker buildx prune --force --max-used-space 8GB >"${LOG_DIR}/cache-cleanup.log" 2>&1 || true
fi

log "Release verified: container=${NEW_CONTAINER} image=${IMAGE} health=${NEW_HEALTH}"
log "Audit counts: app_5xx=${app_5xx} app_fatal=${app_fatal} caddy_5xx=${caddy_5xx}"
log "Disk after release: $(df -h / | awk 'NR==2 {print $5 " used, " $4 " free"}')"
log "Server logs: ${LOG_DIR}"
