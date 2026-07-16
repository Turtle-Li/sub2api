#!/usr/bin/env bash

# Install the Sub2API automatic release controller on the dedicated Sub2API
# host. It installs the server-side release service and optional polling
# fallback; it does not alter Caddy, containers, data, or immediately release
# an image.

set -Eeuo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
SCRIPT_DIR="${APP_DIR}/scripts"
CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
UNIT_DIR="${SUB2API_AUTODEPLOY_UNIT_DIR:-/etc/systemd/system}"

PRODUCTION_BRANCH="${SUB2API_AUTODEPLOY_PRODUCTION_BRANCH:-}"
PRODUCTION_REPO_URL="${SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL:-}"
UPSTREAM_REPO_URL="${SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL:-}"
HEALTH_URL="${SUB2API_PUBLIC_HEALTH_URL:-https://www.turtleligpt.com/health}"
REPLACE_CONFIG=false
ENABLE_TIMER=false

usage() {
  cat <<'EOF'
Usage: install-autodeploy.sh [options]

Options:
  --production-branch BRANCH  Branch that holds the site's custom production code.
  --production-repo URL       Git URL of the production fork.
  --upstream-repo URL         Git URL of the official upstream.
  --health-url URL            Public URL checked after the blue-green switch.
  --replace-config            Replace an existing /etc/sub2api-autodeploy.env.
  --enable-timer              Enable the periodic polling fallback (off by default).
  --no-enable                 Do not enable the timer (kept for compatibility).
  --help                      Show this help.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --production-branch)
      [ "$#" -ge 2 ] || { echo "--production-branch requires a value" >&2; exit 2; }
      PRODUCTION_BRANCH="$2"
      shift
      ;;
    --production-repo)
      [ "$#" -ge 2 ] || { echo "--production-repo requires a value" >&2; exit 2; }
      PRODUCTION_REPO_URL="$2"
      shift
      ;;
    --upstream-repo)
      [ "$#" -ge 2 ] || { echo "--upstream-repo requires a value" >&2; exit 2; }
      UPSTREAM_REPO_URL="$2"
      shift
      ;;
    --health-url)
      [ "$#" -ge 2 ] || { echo "--health-url requires a value" >&2; exit 2; }
      HEALTH_URL="$2"
      shift
      ;;
    --replace-config)
      REPLACE_CONFIG=true
      ;;
    --enable-timer)
      ENABLE_TIMER=true
      ;;
    --no-enable)
      ENABLE_TIMER=false
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_simple_value() {
  local name="$1"
  local value="$2"
  [ -n "$value" ] || die "$name must not be empty"
  case "$value" in
    *$'\n'*|*$'\r'*|*' '*) die "$name must not contain whitespace" ;;
  esac
}

derive_remote_url() {
  local preferred_remote="$1"
  local fallback_remote="$2"
  git -C "$SOURCE_ROOT" remote get-url "$preferred_remote" 2>/dev/null \
    || git -C "$SOURCE_ROOT" remote get-url "$fallback_remote" 2>/dev/null \
    || true
}

[ "$(id -u)" -eq 0 ] || die "run this installer as root on the Sub2API server"
for command_name in git install systemctl docker curl flock; do
  require_cmd "$command_name"
done
[ -d "$APP_DIR" ] || die "Sub2API application directory does not exist: $APP_DIR"

if [ -z "$PRODUCTION_BRANCH" ]; then
  PRODUCTION_BRANCH="$(git -C "$SOURCE_ROOT" branch --show-current 2>/dev/null || true)"
fi
if [ -z "$PRODUCTION_REPO_URL" ]; then
  PRODUCTION_REPO_URL="$(derive_remote_url fork origin)"
fi
if [ -z "$UPSTREAM_REPO_URL" ]; then
  UPSTREAM_REPO_URL="$(derive_remote_url origin fork)"
fi

require_simple_value SUB2API_AUTODEPLOY_PRODUCTION_BRANCH "$PRODUCTION_BRANCH"
require_simple_value SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL "$PRODUCTION_REPO_URL"
if [ -n "$UPSTREAM_REPO_URL" ]; then
  require_simple_value SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL "$UPSTREAM_REPO_URL"
fi
require_simple_value SUB2API_PUBLIC_HEALTH_URL "$HEALTH_URL"
git -C "$SOURCE_ROOT" check-ref-format --branch "$PRODUCTION_BRANCH" >/dev/null

for file in \
  deploy/sub2api-autodeploy.sh \
  deploy/sub2api-server-release.sh \
  deploy/sub2api-github-deploy-trigger.sh \
  deploy/sub2api-autodeploy.service \
  deploy/sub2api-autodeploy.timer; do
  [ -r "${SOURCE_ROOT}/${file}" ] || die "installer source is incomplete: ${file}"
done

bash -n "${SOURCE_ROOT}/deploy/sub2api-autodeploy.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-server-release.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-github-deploy-trigger.sh"

if [ -e "$CONFIG_FILE" ] && [ "$REPLACE_CONFIG" != "true" ]; then
  echo "Keeping existing automatic-release configuration: ${CONFIG_FILE}"
else
  config_temp="$(mktemp)"
  umask 077
  {
    printf '%s\n' '# Managed by deploy/install-autodeploy.sh'
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_REMOTE=%s\n' 'fork'
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL=%s\n' "$PRODUCTION_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_BRANCH=%s\n' "$PRODUCTION_BRANCH"
    printf 'SUB2API_AUTODEPLOY_MERGE_MAIN=%s\n' 'true'
    printf 'SUB2API_AUTODEPLOY_MAIN_REMOTE=%s\n' 'fork'
    printf 'SUB2API_AUTODEPLOY_MAIN_REPO_URL=%s\n' "$PRODUCTION_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_MAIN_BRANCH=%s\n' 'main'
    # Official upstream updates are merged into fork/main deliberately before
    # this service is triggered; never merge them from the production server.
    printf 'SUB2API_AUTODEPLOY_MERGE_UPSTREAM=%s\n' 'false'
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_REMOTE=%s\n' 'origin'
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL=%s\n' "$UPSTREAM_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_BRANCH=%s\n' 'main'
    printf 'SUB2API_PUBLIC_HEALTH_URL=%s\n' "$HEALTH_URL"
    printf 'SUB2API_AUTODEPLOY_FAILURE_RETRY_SECONDS=%s\n' '1800'
  } >"$config_temp"
  install -D -m 600 "$config_temp" "$CONFIG_FILE"
  rm -f "$config_temp"
  echo "Installed automatic-release configuration: ${CONFIG_FILE}"
fi

install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.sh" \
  "${SCRIPT_DIR}/sub2api-autodeploy.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-server-release.sh" \
  "${SCRIPT_DIR}/sub2api-server-release.sh"
install -D -m 755 "${SOURCE_ROOT}/deploy/sub2api-github-deploy-trigger.sh" \
  "${SCRIPT_DIR}/sub2api-github-deploy-trigger.sh"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.service" \
  "${UNIT_DIR}/sub2api-autodeploy.service"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.timer" \
  "${UNIT_DIR}/sub2api-autodeploy.timer"

systemctl daemon-reload
if [ "$ENABLE_TIMER" = "true" ]; then
  systemctl enable --now sub2api-autodeploy.timer
  echo "Enabled sub2api-autodeploy.timer (checks every five minutes)."
else
  systemctl disable --now sub2api-autodeploy.timer >/dev/null 2>&1 || true
  echo "Installed event-driven release service; polling timer is disabled."
fi

echo "Validate without releasing: ${SCRIPT_DIR}/sub2api-autodeploy.sh --check"
echo "Show timer: systemctl list-timers sub2api-autodeploy.timer"
