#!/usr/bin/env bash

# Install the Sub2API release controller on the dedicated Sub2API host. The
# normal path receives a GitHub-built image and only performs blue-green
# release work. The source-based service and polling timer remain available as
# an explicit recovery fallback. This installer does not release an image.

set -Eeuo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
SCRIPT_DIR="${APP_DIR}/scripts"
CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
UNIT_DIR="${SUB2API_AUTODEPLOY_UNIT_DIR:-/etc/systemd/system}"
RUNTIME_GUARD_EXECUTABLE="${SUB2API_RUNTIME_GUARD_EXECUTABLE:-/usr/local/libexec/sub2api-runtime-guard.sh}"

PRODUCTION_BRANCH="${SUB2API_AUTODEPLOY_PRODUCTION_BRANCH:-}"
PRODUCTION_REPO_URL="${SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL:-}"
UPSTREAM_REPO_URL="${SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL:-}"
HEALTH_URL="${SUB2API_PUBLIC_HEALTH_URL:-https://www.turtleligpt.com/health}"
HEALTH_RESOLVE="${SUB2API_PUBLIC_HEALTH_RESOLVE:-}"
GITHUB_IMAGE_SOURCE="${SUB2API_GITHUB_IMAGE_SOURCE:-}"
RECOVERY_MERGE_MAIN=true
REPLACE_CONFIG=false
ENABLE_TIMER=false
INSTALL_BLUE_GREEN_HELPER=false
ENABLE_RUNTIME_GUARD=true
DEPENDENCY_MODE="${SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE:-local}"
RUNTIME_NETWORK="${SUB2API_RUNTIME_GUARD_NETWORK:-sub2api_default}"
RUNTIME_DATA_VOLUME="${SUB2API_RUNTIME_GUARD_DATA_VOLUME:-sub2api_sub2api_data}"
CADDY_CONTAINER="${SUB2API_CADDY_CONTAINER:-sub2api-caddy}"
EXTERNAL_RUNTIME_ENV_FILE="${SUB2API_EXTERNAL_RUNTIME_ENV_FILE:-}"
EXTERNAL_CA_FILE="${SUB2API_EXTERNAL_CA_FILE:-}"
DUAL_NODE_RUNTIME_ENABLED="${SUB2API_DUAL_NODE_RUNTIME_ENABLED:-false}"
RUNTIME_CONFIG_EXPLICIT=false

usage() {
  cat <<'EOF'
Usage: install-autodeploy.sh [options]

Options:
  --production-branch BRANCH  Branch that holds the site's custom production code.
  --production-repo URL       Git URL of the production fork.
  --upstream-repo URL         Git URL of the official upstream.
  --health-url URL            Public URL checked after the blue-green switch.
  --health-resolve VALUE      Pin that URL to this origin as HOST:PORT:IPV4.
  --replace-config            Replace an existing /etc/sub2api-autodeploy.env.
  --enable-timer              Enable the periodic polling fallback (off by default).
  --install-blue-green-helper Replace the externally managed blue-green helper after backing it up.
  --dependency-mode MODE      Application dependency mode: local or external.
  --runtime-network NAME      Exact Docker network for application slots.
  --runtime-data-volume NAME  Exact Docker data volume for application slots.
  --caddy-container NAME      Exact Caddy container name.
  --external-runtime-env-file PATH
                              Root-owned 0600 external PG/Redis environment file.
  --external-ca-file PATH     Root-owned CA file for external PG/Redis TLS.
  --dual-node-runtime-enabled BOOL
                              Enable exact traffic/background/token runtime contract.
  --no-enable-runtime-guard   Install the runtime guard but leave its timer disabled.
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
    --health-resolve)
      [ "$#" -ge 2 ] || { echo "--health-resolve requires a value" >&2; exit 2; }
      HEALTH_RESOLVE="$2"
      shift
      ;;
    --replace-config)
      REPLACE_CONFIG=true
      ;;
    --enable-timer)
      ENABLE_TIMER=true
      ;;
    --install-blue-green-helper)
      INSTALL_BLUE_GREEN_HELPER=true
      ;;
    --dependency-mode)
      [ "$#" -ge 2 ] || { echo "--dependency-mode requires a value" >&2; exit 2; }
      DEPENDENCY_MODE="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --runtime-network)
      [ "$#" -ge 2 ] || { echo "--runtime-network requires a value" >&2; exit 2; }
      RUNTIME_NETWORK="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --runtime-data-volume)
      [ "$#" -ge 2 ] || { echo "--runtime-data-volume requires a value" >&2; exit 2; }
      RUNTIME_DATA_VOLUME="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --caddy-container)
      [ "$#" -ge 2 ] || { echo "--caddy-container requires a value" >&2; exit 2; }
      CADDY_CONTAINER="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --external-runtime-env-file)
      [ "$#" -ge 2 ] || { echo "--external-runtime-env-file requires a value" >&2; exit 2; }
      EXTERNAL_RUNTIME_ENV_FILE="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --external-ca-file)
      [ "$#" -ge 2 ] || { echo "--external-ca-file requires a value" >&2; exit 2; }
      EXTERNAL_CA_FILE="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --dual-node-runtime-enabled)
      [ "$#" -ge 2 ] || { echo "--dual-node-runtime-enabled requires a value" >&2; exit 2; }
      DUAL_NODE_RUNTIME_ENABLED="$2"
      RUNTIME_CONFIG_EXPLICIT=true
      shift
      ;;
    --no-enable-runtime-guard)
      ENABLE_RUNTIME_GUARD=false
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

require_docker_name() {
  local name="$1" value="$2"
  case "$value" in
    ''|[!A-Za-z0-9]*|*[!A-Za-z0-9_.-]*) die "$name must be a valid Docker object name" ;;
  esac
}

require_absolute_path() {
  local name="$1" value="$2"
  case "$value" in
    /*) ;;
    *) die "$name must be an absolute path" ;;
  esac
  case "$value" in
    *$'\n'*|*$'\r'*|*' '*) die "$name must not contain whitespace" ;;
  esac
}

validate_health_resolve() {
  [ -n "$1" ] || return 0
  python3 - "$1" "$2" <<'PY'
import ipaddress
import re
import sys
import urllib.parse

value = sys.argv[1]
url = sys.argv[2]
parts = value.split(":")
if len(parts) != 3:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE must be HOST:PORT:IPV4")
host, port, address = parts
if not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?", host) or ".." in host:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE has an invalid host")
if not port.isdigit() or not 1 <= int(port) <= 65535:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE has an invalid port")
if not isinstance(ipaddress.ip_address(address), ipaddress.IPv4Address):
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE must use IPv4")
parsed = urllib.parse.urlsplit(url)
if parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username or parsed.password:
    raise SystemExit("SUB2API_PUBLIC_HEALTH_URL has an invalid authority")
url_port = parsed.port or (443 if parsed.scheme == "https" else 80)
if parsed.hostname != host or url_port != int(port):
    raise SystemExit("SUB2API_PUBLIC_HEALTH_RESOLVE host/port must match SUB2API_PUBLIC_HEALTH_URL")
PY
}

derive_remote_url() {
  local preferred_remote="$1"
  local fallback_remote="$2"
  git -C "$SOURCE_ROOT" remote get-url "$preferred_remote" 2>/dev/null \
    || git -C "$SOURCE_ROOT" remote get-url "$fallback_remote" 2>/dev/null \
    || true
}

derive_github_image_source() {
  local repository_url="$1"
  case "$repository_url" in
    https://github.com/*)
      printf '%s\n' "${repository_url%.git}"
      ;;
    git@github.com:*)
      printf 'https://github.com/%s\n' "${repository_url#git@github.com:}" \
        | sed 's/\.git$//'
      ;;
    ssh://git@github.com/*)
      printf 'https://github.com/%s\n' "${repository_url#ssh://git@github.com/}" \
        | sed 's/\.git$//'
      ;;
    *)
      return 1
      ;;
  esac
}

[ "$(id -u)" -eq 0 ] || die "run this installer as root on the Sub2API server"
for command_name in git install systemctl docker curl flock grep head python3 sed tar wc zstd; do
  require_cmd "$command_name"
done
[ -d "$APP_DIR" ] || die "Sub2API application directory does not exist: $APP_DIR"

if [ -z "$PRODUCTION_BRANCH" ]; then
  PRODUCTION_BRANCH="$(git -C "$SOURCE_ROOT" branch --show-current 2>/dev/null || true)"
fi
if [ "$PRODUCTION_BRANCH" = "main" ]; then
  RECOVERY_MERGE_MAIN=false
fi
if [ -z "$PRODUCTION_REPO_URL" ]; then
  PRODUCTION_REPO_URL="$(derive_remote_url fork origin)"
fi
if [ -z "$UPSTREAM_REPO_URL" ]; then
  UPSTREAM_REPO_URL="$(derive_remote_url origin fork)"
fi
if [ -z "$GITHUB_IMAGE_SOURCE" ]; then
  GITHUB_IMAGE_SOURCE="$(derive_github_image_source "$PRODUCTION_REPO_URL")" \
    || die "set SUB2API_GITHUB_IMAGE_SOURCE to the canonical https://github.com/OWNER/REPO URL"
fi

require_simple_value SUB2API_AUTODEPLOY_PRODUCTION_BRANCH "$PRODUCTION_BRANCH"
require_simple_value SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL "$PRODUCTION_REPO_URL"
if [ -n "$UPSTREAM_REPO_URL" ]; then
  require_simple_value SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL "$UPSTREAM_REPO_URL"
fi
require_simple_value SUB2API_PUBLIC_HEALTH_URL "$HEALTH_URL"
validate_health_resolve "$HEALTH_RESOLVE" "$HEALTH_URL"
require_simple_value SUB2API_GITHUB_IMAGE_SOURCE "$GITHUB_IMAGE_SOURCE"
require_simple_value SUB2API_APP_DIR "$APP_DIR"
case "$DEPENDENCY_MODE" in
  local|external) ;;
  *) die "SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE must be local or external" ;;
esac
case "$DUAL_NODE_RUNTIME_ENABLED" in
  true|false) ;;
  *) die "SUB2API_DUAL_NODE_RUNTIME_ENABLED must be true or false" ;;
esac
require_docker_name SUB2API_RUNTIME_GUARD_NETWORK "$RUNTIME_NETWORK"
require_docker_name SUB2API_RUNTIME_GUARD_DATA_VOLUME "$RUNTIME_DATA_VOLUME"
require_docker_name SUB2API_CADDY_CONTAINER "$CADDY_CONTAINER"
if [ "$DEPENDENCY_MODE" = external ]; then
  require_absolute_path SUB2API_EXTERNAL_RUNTIME_ENV_FILE "$EXTERNAL_RUNTIME_ENV_FILE"
  require_absolute_path SUB2API_EXTERNAL_CA_FILE "$EXTERNAL_CA_FILE"
fi
case "$GITHUB_IMAGE_SOURCE" in
  https://github.com/*) ;;
  *) die "SUB2API_GITHUB_IMAGE_SOURCE must be an https://github.com URL" ;;
esac
git -C "$SOURCE_ROOT" check-ref-format --branch "$PRODUCTION_BRANCH" >/dev/null

for file in \
  deploy/sub2api-autodeploy.sh \
  deploy/sub2api-github-image-release.sh \
  deploy/sub2api-server-release.sh \
  deploy/sub2api-drain-monitor.sh \
  deploy/sub2api-runtime-guard.sh \
  deploy/sub2api-github-deploy-trigger.sh \
  deploy/sub2api-cert-receiver.sh \
  deploy/sub2api-cert-deploy-trigger.sh \
  deploy/install-sub2api-cert-receiver.sh \
  deploy/sub2api-node-state.sh \
  deploy/sub2api-autodeploy.service \
  deploy/sub2api-autodeploy.timer \
  deploy/sub2api-runtime-guard.service \
  deploy/sub2api-runtime-guard.timer; do
  [ -r "${SOURCE_ROOT}/${file}" ] || die "installer source is incomplete: ${file}"
done
[ "$INSTALL_BLUE_GREEN_HELPER" != true ] \
  || [ -r "${SOURCE_ROOT}/deploy/sub2api-blue-green-release.sh" ] \
  || die "installer source is incomplete: deploy/sub2api-blue-green-release.sh"

bash -n "${SOURCE_ROOT}/deploy/sub2api-autodeploy.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-github-image-release.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-server-release.sh"
[ "$INSTALL_BLUE_GREEN_HELPER" != true ] \
  || bash -n "${SOURCE_ROOT}/deploy/sub2api-blue-green-release.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-drain-monitor.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-github-deploy-trigger.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-cert-receiver.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-cert-deploy-trigger.sh"
bash -n "${SOURCE_ROOT}/deploy/install-sub2api-cert-receiver.sh"
bash -n "${SOURCE_ROOT}/deploy/sub2api-node-state.sh"

if [ -e "$CONFIG_FILE" ] && [ "$REPLACE_CONFIG" != "true" ]; then
  [ "$RUNTIME_CONFIG_EXPLICIT" != true ] \
    || die "runtime configuration options require --replace-config when ${CONFIG_FILE} already exists"
  configured_app_dir="$(sed -n 's/^SUB2API_APP_DIR=//p' "$CONFIG_FILE" | tail -n 1)"
  if [ -n "$configured_app_dir" ] && [ "$configured_app_dir" != "$APP_DIR" ]; then
    die "existing SUB2API_APP_DIR=${configured_app_dir} does not match installer APP_DIR=${APP_DIR}; use the matching directory or --replace-config"
  fi
  if [ -z "$configured_app_dir" ]; then
    printf 'SUB2API_APP_DIR=%s\n' "$APP_DIR" >>"$CONFIG_FILE"
  fi
  echo "Keeping existing automatic-release configuration: ${CONFIG_FILE}"
else
  config_temp="$(mktemp)"
  umask 077
  {
    printf '%s\n' '# Managed by deploy/install-autodeploy.sh'
    printf 'SUB2API_APP_DIR=%s\n' "$APP_DIR"
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_REMOTE=%s\n' 'fork'
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL=%s\n' "$PRODUCTION_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_PRODUCTION_BRANCH=%s\n' "$PRODUCTION_BRANCH"
    printf 'SUB2API_AUTODEPLOY_MERGE_MAIN=%s\n' "$RECOVERY_MERGE_MAIN"
    printf 'SUB2API_AUTODEPLOY_MAIN_REMOTE=%s\n' 'fork'
    printf 'SUB2API_AUTODEPLOY_MAIN_REPO_URL=%s\n' "$PRODUCTION_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_MAIN_BRANCH=%s\n' 'main'
    # Official upstream updates are merged into fork/main deliberately before
    # this service is triggered; never merge them from the production server.
    printf 'SUB2API_AUTODEPLOY_MERGE_UPSTREAM=%s\n' 'false'
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_REMOTE=%s\n' 'origin'
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL=%s\n' "$UPSTREAM_REPO_URL"
    printf 'SUB2API_AUTODEPLOY_UPSTREAM_BRANCH=%s\n' 'main'
    printf 'SUB2API_GITHUB_IMAGE_SOURCE=%s\n' "$GITHUB_IMAGE_SOURCE"
    printf 'SUB2API_GITHUB_IMAGE_MAX_BYTES=%s\n' '1073741824'
    printf 'SUB2API_PUBLIC_HEALTH_URL=%s\n' "$HEALTH_URL"
    if [ -n "$HEALTH_RESOLVE" ]; then
      printf 'SUB2API_PUBLIC_HEALTH_RESOLVE=%s\n' "$HEALTH_RESOLVE"
    fi
    printf 'SUB2API_MAINTENANCE_LOCK_FILE=%s\n' '/run/lock/sub2api-maintenance.lock'
    printf 'SUB2API_RUNTIME_GUARD_DEPENDENCY_MODE=%s\n' "$DEPENDENCY_MODE"
    printf 'SUB2API_RUNTIME_GUARD_NETWORK=%s\n' "$RUNTIME_NETWORK"
    printf 'SUB2API_RUNTIME_GUARD_DATA_VOLUME=%s\n' "$RUNTIME_DATA_VOLUME"
    printf 'SUB2API_CADDY_CONTAINER=%s\n' "$CADDY_CONTAINER"
    if [ "$DEPENDENCY_MODE" = external ]; then
      printf 'SUB2API_EXTERNAL_RUNTIME_ENV_FILE=%s\n' "$EXTERNAL_RUNTIME_ENV_FILE"
      printf 'SUB2API_EXTERNAL_CA_FILE=%s\n' "$EXTERNAL_CA_FILE"
    fi
    printf 'SUB2API_RUNTIME_GUARD_RETRY_ATTEMPTS=%s\n' '20'
    printf 'SUB2API_RUNTIME_GUARD_RETRY_INTERVAL_SECONDS=%s\n' '3'
    printf 'SUB2API_RUNTIME_GUARD_COOLDOWN_SECONDS=%s\n' '300'
    printf 'SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_ATTEMPTS=%s\n' '3'
    printf 'SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_INTERVAL_SECONDS=%s\n' '3'
    printf 'SUB2API_RUNTIME_GUARD_PUBLIC_HEALTH_MAX_TIME_SECONDS=%s\n' '20'
    printf 'SUB2API_AUTODEPLOY_LOCK_WAIT_SECONDS=%s\n' '900'
    printf 'SUB2API_AUTODEPLOY_FAILURE_RETRY_SECONDS=%s\n' '1800'
    printf 'SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER=%s\n' 'false'
    printf 'SUB2API_RELEASE_BACKGROUND_MODE=%s\n' 'activate'
    printf 'SUB2API_DUAL_NODE_RUNTIME_ENABLED=%s\n' "$DUAL_NODE_RUNTIME_ENABLED"
  } >"$config_temp"
  install -D -m 600 "$config_temp" "$CONFIG_FILE"
  rm -f "$config_temp"
  echo "Installed automatic-release configuration: ${CONFIG_FILE}"
fi

install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.sh" \
  "${SCRIPT_DIR}/sub2api-autodeploy.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-github-image-release.sh" \
  "${SCRIPT_DIR}/sub2api-github-image-release.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-server-release.sh" \
  "${SCRIPT_DIR}/sub2api-server-release.sh"
if [ "$INSTALL_BLUE_GREEN_HELPER" = true ]; then
  helper_backup_dir="${APP_DIR}/backups/blue-green-helper-$(date -u +%Y%m%dT%H%M%SZ)"
  install -d -m 700 "$helper_backup_dir"
  if [ -f "${SCRIPT_DIR}/sub2api-blue-green-release.sh" ] \
    && [ ! -L "${SCRIPT_DIR}/sub2api-blue-green-release.sh" ]; then
    install -m 600 "${SCRIPT_DIR}/sub2api-blue-green-release.sh" \
      "${helper_backup_dir}/sub2api-blue-green-release.sh"
  fi
  install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-blue-green-release.sh" \
    "${SCRIPT_DIR}/sub2api-blue-green-release.sh"
  printf 'Installed blue-green helper; rollback backup: %s\n' "$helper_backup_dir"
fi
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-drain-monitor.sh" \
  "${SCRIPT_DIR}/sub2api-drain-monitor.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.sh" \
  "${SCRIPT_DIR}/sub2api-runtime-guard.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.sh" \
  "$RUNTIME_GUARD_EXECUTABLE"
install -D -m 755 "${SOURCE_ROOT}/deploy/sub2api-github-deploy-trigger.sh" \
  "${SCRIPT_DIR}/sub2api-github-deploy-trigger.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-cert-receiver.sh" \
  "${SCRIPT_DIR}/sub2api-cert-receiver.sh"
install -D -m 755 "${SOURCE_ROOT}/deploy/sub2api-cert-deploy-trigger.sh" \
  "${SCRIPT_DIR}/sub2api-cert-deploy-trigger.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/install-sub2api-cert-receiver.sh" \
  "${SCRIPT_DIR}/install-sub2api-cert-receiver.sh"
install -D -m 750 "${SOURCE_ROOT}/deploy/sub2api-node-state.sh" \
  "${SCRIPT_DIR}/sub2api-node-state.sh"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.service" \
  "${UNIT_DIR}/sub2api-autodeploy.service"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-autodeploy.timer" \
  "${UNIT_DIR}/sub2api-autodeploy.timer"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.service" \
  "${UNIT_DIR}/sub2api-runtime-guard.service"
install -D -m 644 "${SOURCE_ROOT}/deploy/sub2api-runtime-guard.timer" \
  "${UNIT_DIR}/sub2api-runtime-guard.timer"

systemctl daemon-reload
if [ "$ENABLE_RUNTIME_GUARD" = "true" ]; then
  systemctl enable --now sub2api-runtime-guard.timer
  echo "Enabled sub2api-runtime-guard.timer (repairs the active production slot)."
else
  systemctl disable --now sub2api-runtime-guard.timer >/dev/null 2>&1 || true
  echo "Installed sub2api-runtime-guard.timer; it remains disabled until runtime state and mounts are verified."
fi
if [ "$ENABLE_TIMER" = "true" ]; then
  systemctl enable --now sub2api-autodeploy.timer
  echo "Enabled sub2api-autodeploy.timer (checks every five minutes)."
else
  systemctl disable --now sub2api-autodeploy.timer >/dev/null 2>&1 || true
  echo "Installed GitHub image receiver and recovery service; polling timer is disabled."
fi

echo "Validate without releasing: ${SCRIPT_DIR}/sub2api-autodeploy.sh --check"
echo "Show timer: systemctl list-timers sub2api-autodeploy.timer"
echo "Show runtime recovery: systemctl status sub2api-runtime-guard.timer"
