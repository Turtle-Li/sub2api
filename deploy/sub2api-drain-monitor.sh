#!/usr/bin/env bash
set -euo pipefail

APP_DIR="${APP_DIR:-/opt/sub2api}"
DRAIN_CONTAINER="${DRAIN_CONTAINER:-sub2api}"
ACTIVE_CONTAINER="${ACTIVE_CONTAINER:-}"
REQUIRED_CADDY_UPSTREAM="${REQUIRED_CADDY_UPSTREAM:-}"
FORBIDDEN_CADDY_UPSTREAM="${FORBIDDEN_CADDY_UPSTREAM:-}"
CADDY_CONTAINER="${CADDY_CONTAINER:-sub2api-caddy}"
CADDY_ACTIVE_CONFIG_PATH="${CADDY_ACTIVE_CONFIG_PATH:-/etc/caddy/Caddyfile}"
CADDYFILE="${CADDYFILE:-${APP_DIR}/Caddyfile}"
PORT="${PORT:-8080}"
INTERVAL_SECONDS="${INTERVAL_SECONDS:-60}"
ACTIVE_WINDOW_SECONDS="${ACTIVE_WINDOW_SECONDS:-${MAX_WAIT_SECONDS:-600}}"
RETRY_DELAY_SECONDS="${RETRY_DELAY_SECONDS:-3600}"
MAX_RUNTIME_SECONDS="${MAX_RUNTIME_SECONDS:-0}"
STOP_DRAIN_CONTAINER="${STOP_DRAIN_CONTAINER:-true}"
LOG_FILE="${LOG_FILE:-/var/log/sub2api-drain-monitor.log}"
LOCK_FILE="${LOCK_FILE:-/tmp/sub2api-drain-monitor-${DRAIN_CONTAINER}.lock}"
PID_FILE="${PID_FILE:-}"
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-/run/lock/sub2api-maintenance.lock}"

log() {
  printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG_FILE"
}

container_running() {
  docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null | grep -qx true
}

container_health() {
  docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$1" 2>/dev/null || true
}

caddy_active_config_contains() {
  local text="$1"
  docker exec \
    -e CADDY_CHECK_TEXT="$text" \
    "$CADDY_CONTAINER" \
    sh -c '(wget -qO- http://127.0.0.1:2019/config/ 2>/dev/null || curl -fsS http://127.0.0.1:2019/config/) | grep -qF "$CADDY_CHECK_TEXT"'
}

caddy_startup_config_contains() {
  local text="$1"
  docker exec \
    -e CADDY_CHECK_PATH="$CADDY_ACTIVE_CONFIG_PATH" \
    -e CADDY_CHECK_TEXT="$text" \
    "$CADDY_CONTAINER" \
    sh -c 'grep -qF "$CADDY_CHECK_TEXT" "$CADDY_CHECK_PATH"'
}

require_active_ok() {
  if [ -z "$ACTIVE_CONTAINER" ]; then
    return 0
  fi

  if ! container_running "$ACTIVE_CONTAINER"; then
    log "active container $ACTIVE_CONTAINER is not running; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi

  health="$(container_health "$ACTIVE_CONTAINER")"
  if [ "$health" != "healthy" ] && [ "$health" != "running" ]; then
    log "active container $ACTIVE_CONTAINER status is $health; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi
}

require_caddy_ok() {
  if [ -z "$REQUIRED_CADDY_UPSTREAM" ]; then
    return 0
  fi

  if ! grep -qF "$REQUIRED_CADDY_UPSTREAM" "$CADDYFILE"; then
    log "required Caddy upstream $REQUIRED_CADDY_UPSTREAM not found in $CADDYFILE; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi

  if ! container_running "$CADDY_CONTAINER"; then
    log "Caddy container $CADDY_CONTAINER is not running; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi

  if ! caddy_active_config_contains "$REQUIRED_CADDY_UPSTREAM"; then
    log "active Caddy config does not contain $REQUIRED_CADDY_UPSTREAM; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi

  if [ -n "$FORBIDDEN_CADDY_UPSTREAM" ] && caddy_active_config_contains "$FORBIDDEN_CADDY_UPSTREAM"; then
    log "active Caddy config still contains forbidden upstream $FORBIDDEN_CADDY_UPSTREAM; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi

  if ! caddy_startup_config_contains "$REQUIRED_CADDY_UPSTREAM"; then
    log "Caddy startup file $CADDY_ACTIVE_CONFIG_PATH does not contain $REQUIRED_CADDY_UPSTREAM; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi

  if [ -n "$FORBIDDEN_CADDY_UPSTREAM" ] && caddy_startup_config_contains "$FORBIDDEN_CADDY_UPSTREAM"; then
    log "Caddy startup file $CADDY_ACTIVE_CONFIG_PATH still contains forbidden upstream $FORBIDDEN_CADDY_UPSTREAM; leaving $DRAIN_CONTAINER untouched"
    exit 1
  fi
}

established_count() {
  local container="$1"
  local port_hex
  port_hex="$(printf '%04X' "$PORT")"

  if ! container_running "$container"; then
    printf '0\n'
    return 0
  fi

  docker exec "$container" sh -c 'cat /proc/net/tcp /proc/net/tcp6 2>/dev/null' |
    awk -v port=":${port_hex}$" '$4 == "01" && $2 ~ port { n++ } END { print n + 0 }'
}

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  log "another drain monitor is already running for $DRAIN_CONTAINER"
  exit 1
fi

case "$MAINTENANCE_LOCK_FILE" in
  /*) ;;
  *) log "maintenance lock path must be absolute: $MAINTENANCE_LOCK_FILE"; exit 1 ;;
esac
if [ ! -d "${MAINTENANCE_LOCK_FILE%/*}" ]; then
  log "maintenance lock directory does not exist: ${MAINTENANCE_LOCK_FILE%/*}"
  exit 1
fi
exec 8>>"$MAINTENANCE_LOCK_FILE"

cleanup() {
  if [ -n "$PID_FILE" ] && [ -f "$PID_FILE" ] && [ "$(cat "$PID_FILE" 2>/dev/null || true)" = "$$" ]; then
    rm -f "$PID_FILE"
  fi
}
trap cleanup EXIT

if [ -n "$PID_FILE" ]; then
  printf '%s\n' "$$" > "$PID_FILE"
fi

cd "$APP_DIR"

if [ -n "$REQUIRED_CADDY_UPSTREAM" ]; then
  if ! grep -qF "$REQUIRED_CADDY_UPSTREAM" "$CADDYFILE"; then
    log "required Caddy upstream $REQUIRED_CADDY_UPSTREAM not found in $CADDYFILE; refusing to stop $DRAIN_CONTAINER"
    exit 1
  fi
fi

require_active_ok
require_caddy_ok

start_epoch="$(date +%s)"
overall_deadline=0
if [ "$MAX_RUNTIME_SECONDS" -gt 0 ]; then
  overall_deadline=$((start_epoch + MAX_RUNTIME_SECONDS))
fi

log "watching $DRAIN_CONTAINER port $PORT; interval=${INTERVAL_SECONDS}s active_window=${ACTIVE_WINDOW_SECONDS}s retry_delay=${RETRY_DELAY_SECONDS}s max_runtime=${MAX_RUNTIME_SECONDS}s stop=${STOP_DRAIN_CONTAINER}"

attempt=1
last_count="unknown"

while true; do
  window_start="$(date +%s)"
  window_deadline=$((window_start + ACTIVE_WINDOW_SECONDS))
  log "attempt ${attempt} started"

  while true; do
    require_active_ok
    require_caddy_ok

    if ! container_running "$DRAIN_CONTAINER"; then
      log "$DRAIN_CONTAINER is already stopped"
      exit 0
    fi

    count="$(established_count "$DRAIN_CONTAINER")"
    last_count="$count"
    log "$DRAIN_CONTAINER established_port_${PORT}=${count}"

    if [ "$count" = "0" ]; then
      if [ "$STOP_DRAIN_CONTAINER" = "true" ]; then
        if ! flock -n 8; then
          log "production maintenance or runtime recovery is active; delaying stop of $DRAIN_CONTAINER"
          sleep "$INTERVAL_SECONDS"
          continue
        fi
        # State may have changed between the last connection count and lock
        # acquisition. Revalidate every traffic/configuration fence while the
        # same maintenance lock used by release and runtime recovery is held.
        require_active_ok
        require_caddy_ok
        if ! container_running "$DRAIN_CONTAINER"; then
          log "$DRAIN_CONTAINER was stopped by another coordinated operation"
          exit 0
        fi
        log "stopping drained container $DRAIN_CONTAINER"
        docker stop "$DRAIN_CONTAINER" >/dev/null
        log "$DRAIN_CONTAINER stopped after drain"
      else
        log "$DRAIN_CONTAINER drained; STOP_DRAIN_CONTAINER=false so it remains running"
      fi
      exit 0
    fi

    now="$(date +%s)"
    if [ "$overall_deadline" -gt 0 ] && [ "$now" -ge "$overall_deadline" ]; then
      log "max runtime reached with ${count} established connections; leaving $DRAIN_CONTAINER running"
      exit 2
    fi
    if [ "$now" -ge "$window_deadline" ]; then
      break
    fi

    remaining=$((window_deadline - now))
    sleep_for="$INTERVAL_SECONDS"
    if [ "$sleep_for" -gt "$remaining" ]; then
      sleep_for="$remaining"
    fi
    [ "$sleep_for" -gt 0 ] || break
    sleep "$sleep_for"
  done

  log "attempt ${attempt} ended with ${last_count} established connections; sleeping ${RETRY_DELAY_SECONDS}s before retry"
  attempt=$((attempt + 1))

  if [ "$RETRY_DELAY_SECONDS" -le 0 ]; then
    continue
  fi

  slept=0
  while [ "$slept" -lt "$RETRY_DELAY_SECONDS" ]; do
    require_active_ok
    require_caddy_ok
    now="$(date +%s)"
    if [ "$overall_deadline" -gt 0 ] && [ "$now" -ge "$overall_deadline" ]; then
      log "max runtime reached during retry delay; leaving $DRAIN_CONTAINER running"
      exit 2
    fi
    sleep_for=$((RETRY_DELAY_SECONDS - slept))
    if [ "$sleep_for" -gt 60 ]; then
      sleep_for=60
    fi
    sleep "$sleep_for"
    slept=$((slept + sleep_for))
  done
done
