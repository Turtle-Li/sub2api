#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONITOR="${TEST_DIR}/../sub2api-drain-monitor.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-drain-lock-test.XXXXXX")"
FAKE_BIN="${TEST_ROOT}/bin"
EVENTS="${TEST_ROOT}/events.log"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_before() {
  local first="$1"
  local second="$2"
  local first_line
  local second_line

  first_line="$(grep -nF -- "$first" "$EVENTS" | head -n 1 | cut -d: -f1 || true)"
  second_line="$(grep -nF -- "$second" "$EVENTS" | head -n 1 | cut -d: -f1 || true)"
  [ -n "$first_line" ] && [ -n "$second_line" ] && [ "$first_line" -lt "$second_line" ] \
    || fail "expected ${first} before ${second}"
}

mkdir -p "$FAKE_BIN" "${TEST_ROOT}/app"
printf 'reverse_proxy sub2api-green:8080\n' >"${TEST_ROOT}/app/Caddyfile"

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
printf 'docker:%s\n' "$*" >>"$FAKE_EVENTS"
case "${1:-}" in
  inspect)
    case "${3:-}" in
      *State.Running*) printf 'true\n' ;;
      *State.Health*) printf 'healthy\n' ;;
    esac
    ;;
  exec)
    case "$*" in
      *CADDY_CHECK_TEXT=sub2api-blue:8080*) exit 1 ;;
    esac
    exit 0
    ;;
  stop)
    exit 0
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
printf 'flock:%s\n' "$*" >>"$FAKE_EVENTS"
if [ "${FAKE_FLOCK_MODE:-success}" = maintenance-locked ] && [ "${2:-}" = "8" ]; then
  exit 1
fi
exit 0
EOF
chmod +x "${FAKE_BIN}/flock"

cat >"${FAKE_BIN}/sleep" <<'EOF'
#!/usr/bin/env bash
printf 'sleep:%s\n' "$*" >>"$FAKE_EVENTS"
exit "${FAKE_SLEEP_RESULT:-0}"
EOF
chmod +x "${FAKE_BIN}/sleep"

cat >"${FAKE_BIN}/date" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = "-Is" ]; then
  printf '2026-08-25T00:00:00+00:00\n'
else
  /bin/date "$@"
fi
EOF
chmod +x "${FAKE_BIN}/date"

run_monitor() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_EVENTS="$EVENTS" \
    FAKE_FLOCK_MODE="${FAKE_FLOCK_MODE:-success}" \
    FAKE_SLEEP_RESULT="${FAKE_SLEEP_RESULT:-0}" \
    APP_DIR="${TEST_ROOT}/app" \
    DRAIN_CONTAINER=sub2api-blue \
    ACTIVE_CONTAINER=sub2api-green \
    REQUIRED_CADDY_UPSTREAM=sub2api-green:8080 \
    FORBIDDEN_CADDY_UPSTREAM=sub2api-blue:8080 \
    CADDY_CONTAINER=sub2api-caddy \
    CADDY_ACTIVE_CONFIG_PATH=/etc/caddy/Caddyfile \
    INTERVAL_SECONDS=1 \
    ACTIVE_WINDOW_SECONDS=1 \
    RETRY_DELAY_SECONDS=1 \
    MAX_RUNTIME_SECONDS=2 \
    STOP_DRAIN_CONTAINER=true \
    LOG_FILE="${TEST_ROOT}/monitor.log" \
    LOCK_FILE="${TEST_ROOT}/drain.lock" \
    PID_FILE="${TEST_ROOT}/drain.pid" \
    SUB2API_MAINTENANCE_LOCK_FILE="${TEST_ROOT}/maintenance.lock" \
    /bin/bash "$MONITOR"
}

: >"$EVENTS"
run_monitor >/dev/null
grep -Fq -- 'flock:-n 8' "$EVENTS" || fail 'maintenance lock was not acquired'
grep -Fq -- 'docker:stop sub2api-blue' "$EVENTS" || fail 'drained container was not stopped'
grep -Fq -- 'wget -Y off -qO- http://127.0.0.1:2019/config/' "$EVENTS" \
  || fail 'active Caddy verification did not disable wget proxy inheritance'
grep -Fq -- 'curl --noproxy "*" -fsS http://127.0.0.1:2019/config/' "$EVENTS" \
  || fail 'active Caddy verification curl fallback did not bypass ambient proxies'
assert_before 'flock:-n 8' 'docker:stop sub2api-blue'

: >"$EVENTS"
if FAKE_FLOCK_MODE=maintenance-locked FAKE_SLEEP_RESULT=88 run_monitor >/dev/null 2>&1; then
  fail 'monitor unexpectedly completed while maintenance lock was held'
fi
grep -Fq -- 'flock:-n 8' "$EVENTS" || fail 'maintenance lock contention was not exercised'
if grep -Fq -- 'docker:stop sub2api-blue' "$EVENTS"; then
  fail 'drained container was stopped without the maintenance lock'
fi

printf 'Drain monitor maintenance-lock tests passed.\n'
