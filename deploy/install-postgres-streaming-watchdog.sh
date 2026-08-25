#!/usr/bin/env bash

set -Eeuo pipefail

ORIGINAL_ARGS=("$@")
LOCK_FILE="${SUB2API_PG_STREAMING_LOCK_FILE:-/run/lock/sub2api-db-streaming.lock}"
LOCKED=false
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME=sub2api-postgres-streaming-watchdog.service
TIMER_NAME=sub2api-postgres-streaming-watchdog.timer

die() {
  echo "ERROR: $*" >&2
  exit 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --locked) LOCKED=true ;;
    --help|-h)
      echo "Usage: install-postgres-streaming-watchdog.sh"
      exit 0
      ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

if [ "$LOCKED" != true ]; then
  case "$LOCK_FILE" in /*) ;; *) die "streaming lock path must be absolute" ;; esac
  command -v flock >/dev/null 2>&1 || die "flock is required"
  exec flock -w 30 "$LOCK_FILE" "$0" --locked "${ORIGINAL_ARGS[@]}"
fi

[ "$(id -u)" -eq 0 ] || die "run as root on the Tokyo candidate host"
for command_name in cp install mktemp systemctl systemd-analyze; do
  command -v "$command_name" >/dev/null 2>&1 || die "$command_name is required"
done

runner_source="$SCRIPT_DIR/sub2api-postgres-streaming-watchdog.sh"
service_source="$SCRIPT_DIR/$SERVICE_NAME"
timer_source="$SCRIPT_DIR/$TIMER_NAME"
runner_target=/usr/local/libexec/sub2api-postgres-streaming-watchdog.sh
service_target="/etc/systemd/system/$SERVICE_NAME"
timer_target="/etc/systemd/system/$TIMER_NAME"
[ -x "$runner_source" ] || die "watchdog runner is missing or not executable"
[ -r "$service_source" ] || die "watchdog service unit is missing"
[ -r "$timer_source" ] || die "watchdog timer unit is missing"

backup_dir="$(mktemp -d)"
was_enabled=false
was_active=false
systemctl is-enabled --quiet "$TIMER_NAME" 2>/dev/null && was_enabled=true
systemctl is-active --quiet "$TIMER_NAME" 2>/dev/null && was_active=true

backup_file() {
  local name="$1" path="$2"
  if [ -e "$path" ]; then cp -a "$path" "$backup_dir/$name"; else : >"$backup_dir/$name.absent"; fi
}

restore_file() {
  local name="$1" path="$2"
  if [ -e "$backup_dir/$name.absent" ]; then rm -f "$path"; else cp -a "$backup_dir/$name" "$path"; fi
}

backup_file runner "$runner_target"
backup_file service "$service_target"
backup_file timer "$timer_target"
rollback_armed=true
cleanup() {
  local status=$?
  trap - EXIT
  if [ "$status" -ne 0 ] && [ "$rollback_armed" = true ]; then
    systemctl disable --now "$TIMER_NAME" >/dev/null 2>&1 || true
    restore_file runner "$runner_target"
    restore_file service "$service_target"
    restore_file timer "$timer_target"
    systemctl daemon-reload >/dev/null 2>&1 || true
    [ "$was_enabled" = false ] || systemctl enable "$TIMER_NAME" >/dev/null 2>&1 || true
    [ "$was_active" = false ] || systemctl start "$TIMER_NAME" >/dev/null 2>&1 || true
  fi
  rm -rf "$backup_dir"
  exit "$status"
}
trap cleanup EXIT

install -d -o root -g root -m 755 /usr/local/libexec
install -o root -g root -m 755 "$runner_source" "$runner_target"
install -o root -g root -m 644 "$service_source" "$service_target"
install -o root -g root -m 644 "$timer_source" "$timer_target"
systemd-analyze verify "$service_target" "$timer_target" >/dev/null
systemctl daemon-reload
systemctl enable --now "$TIMER_NAME" >/dev/null
SUB2API_PG_STREAMING_LOCK_HELD=true "$runner_target" >/dev/null
systemctl is-active --quiet "$TIMER_NAME" || die "watchdog timer is not active"
grep -q '^state=healthy ' /run/sub2api-postgres-streaming-watchdog.status \
  || die "watchdog did not write a healthy status"

rollback_armed=false
rm -rf "$backup_dir"
trap - EXIT
echo "Installed PostgreSQL streaming watchdog."
