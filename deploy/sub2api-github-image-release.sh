#!/usr/bin/env bash

# Receive one zstd-compressed Docker archive from the restricted GitHub Actions
# SSH account, validate its immutable build identity, load it into Docker, and
# hand it to the existing blue-green release helper. No source checkout,
# dependency download, or compilation occurs on the production host.

set -Eeuo pipefail

if [ "$#" -ne 3 ]; then
  echo "Usage: sub2api-github-image-release.sh COMMIT VERSION IMAGE_ID" >&2
  exit 2
fi

COMMIT="$1"
VERSION="$2"
EXPECTED_IMAGE_ID="$3"

CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
if [ -r "$CONFIG_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  set +a
fi

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
RELEASE_HELPER="${SUB2API_SERVER_RELEASE_HELPER:-${APP_DIR}/scripts/sub2api-server-release.sh}"
UPLOAD_ROOT="${SUB2API_GITHUB_IMAGE_UPLOAD_ROOT:-/var/lib/sub2api-autodeploy/incoming}"
LOG_ROOT="${SUB2API_RELEASE_LOG_DIR:-/var/log/sub2api-release}"
LOCK_FILE="${SUB2API_GITHUB_IMAGE_LOCK_FILE:-/var/lock/sub2api-github-image.lock}"
MAX_UPLOAD_BYTES="${SUB2API_GITHUB_IMAGE_MAX_BYTES:-1073741824}"
PUBLIC_HEALTH_URL="${SUB2API_PUBLIC_HEALTH_URL:-https://www.turtleligpt.com/health}"
PRODUCTION_REPO_URL="${SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL:-}"
EXPECTED_SOURCE="${SUB2API_GITHUB_IMAGE_SOURCE:-${PRODUCTION_REPO_URL%.git}}"

SHORT_COMMIT="${COMMIT:0:8}"
RUN_ID="gha-$(date '+%Y%m%d-%H%M%S')-${SHORT_COMMIT}-$$"
LOG_DIR="${LOG_ROOT}/${RUN_ID}"
LOAD_LOG="${LOG_DIR}/image-load.log"
INCOMING_IMAGE="sub2api:github-${COMMIT}"
RELEASE_IMAGE="sub2api:auto-$(date '+%Y%m%d-%H%M%S')-${SHORT_COMMIT}"
ARCHIVE=""
IMAGE_LOAD_ATTEMPTED=false

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

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM
  if [ -n "$ARCHIVE" ] && [ -f "$ARCHIVE" ]; then
    rm -f "$ARCHIVE"
  fi
  if [ "$IMAGE_LOAD_ATTEMPTED" = "true" ]; then
    docker image rm "$INCOMING_IMAGE" >/dev/null 2>&1 || true
  fi
  exit "$exit_code"
}
trap cleanup EXIT INT TERM

case "$COMMIT" in
  *[!0-9a-f]*|'') die "invalid commit" ;;
esac
[ "${#COMMIT}" -eq 40 ] || die "commit must be a full 40-character SHA"
case "$VERSION" in
  ''|*[!0-9A-Za-z._+-]*) die "invalid version" ;;
esac
[ "${#VERSION}" -le 64 ] || die "version is too long"
case "$EXPECTED_IMAGE_ID" in
  sha256:*) ;;
  *) die "image ID must use sha256" ;;
esac
image_id_hex="${EXPECTED_IMAGE_ID#sha256:}"
case "$image_id_hex" in
  *[!0-9a-f]*|'') die "invalid image ID" ;;
esac
[ "${#image_id_hex}" -eq 64 ] || die "image ID must contain 64 hex characters"
case "$MAX_UPLOAD_BYTES" in
  ''|*[!0-9]*) die "SUB2API_GITHUB_IMAGE_MAX_BYTES must be a positive integer" ;;
esac
[ "$MAX_UPLOAD_BYTES" -gt 0 ] || die "SUB2API_GITHUB_IMAGE_MAX_BYTES must be positive"
case "$EXPECTED_SOURCE" in
  https://github.com/*) ;;
  *) die "SUB2API_GITHUB_IMAGE_SOURCE must be an https://github.com URL" ;;
esac
case "$EXPECTED_SOURCE" in
  *$'\n'*|*$'\r'*|*' '*) die "SUB2API_GITHUB_IMAGE_SOURCE must not contain whitespace" ;;
esac

for command_name in docker flock head python3 tar wc zstd; do
  require_cmd "$command_name"
done
[ -x "$RELEASE_HELPER" ] || die "release helper is missing or not executable: ${RELEASE_HELPER}"

mkdir -p "$UPLOAD_ROOT" "$LOG_DIR"
exec 9>"$LOCK_FILE"
flock -n 9 || die "another GitHub image upload or release is already running"

ARCHIVE="$(mktemp "${UPLOAD_ROOT}/image.XXXXXX.tar.zst")"
log "Receiving GitHub-built image for ${COMMIT} (maximum ${MAX_UPLOAD_BYTES} bytes)"
head -c "$((MAX_UPLOAD_BYTES + 1))" >"$ARCHIVE"
archive_bytes="$(wc -c <"$ARCHIVE" | tr -d '[:space:]')"
[ "$archive_bytes" -gt 0 ] || die "image archive is empty"
[ "$archive_bytes" -le "$MAX_UPLOAD_BYTES" ] || die "image archive exceeds the configured size limit"
zstd -t --quiet "$ARCHIVE" || die "image archive failed zstd validation"

if ! zstd -dc -- "$ARCHIVE" \
  | tar -xOf - manifest.json \
  | python3 -c '
import json
import sys

expected = sys.argv[1]
manifest = json.load(sys.stdin)
if len(manifest) != 1:
    raise SystemExit("archive must contain exactly one image")
tags = manifest[0].get("RepoTags") or []
if tags != [expected]:
    raise SystemExit(f"archive tag mismatch: {tags!r}")
' "$INCOMING_IMAGE"; then
  die "Docker archive manifest validation failed"
fi

log "Loading validated Docker archive"
IMAGE_LOAD_ATTEMPTED=true
if ! zstd -dc -- "$ARCHIVE" | docker image load >"$LOAD_LOG" 2>&1; then
  tail -100 "$LOAD_LOG" >&2 || true
  die "docker image load failed"
fi

actual_image_id="$(docker image inspect "$INCOMING_IMAGE" --format '{{.Id}}' 2>/dev/null || true)"
[ "$actual_image_id" = "$EXPECTED_IMAGE_ID" ] \
  || die "image ID mismatch: expected ${EXPECTED_IMAGE_ID}, got ${actual_image_id:-missing}"
[ "$(docker image inspect "$INCOMING_IMAGE" --format '{{.Architecture}}')" = "amd64" ] \
  || die "image architecture is not amd64"
[ "$(docker image inspect "$INCOMING_IMAGE" --format '{{.Os}}')" = "linux" ] \
  || die "image operating system is not linux"
[ "$(docker image inspect "$INCOMING_IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.source"}}')" = "$EXPECTED_SOURCE" ] \
  || die "image source label does not match ${EXPECTED_SOURCE}"
[ "$(docker image inspect "$INCOMING_IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$COMMIT" ] \
  || die "image revision label does not match ${COMMIT}"
[ "$(docker image inspect "$INCOMING_IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" = "$VERSION" ] \
  || die "image version label does not match ${VERSION}"

docker tag "$INCOMING_IMAGE" "$RELEASE_IMAGE"
cat >"${LOG_DIR}/candidate.env" <<EOF
run_id=${RUN_ID}
source_commit=${COMMIT}
version=${VERSION}
image_id=${EXPECTED_IMAGE_ID}
incoming_image=${INCOMING_IMAGE}
release_image=${RELEASE_IMAGE}
archive_bytes=${archive_bytes}
EOF

log "Image identity verified; starting blue-green release with ${RELEASE_IMAGE}"
if ! "$RELEASE_HELPER" \
  --prebuilt "$RELEASE_IMAGE" "$COMMIT" "$VERSION" "$PUBLIC_HEALTH_URL" "$RUN_ID"; then
  die "blue-green release rejected or rolled back the GitHub-built image"
fi

docker image rm "$INCOMING_IMAGE" >>"${LOG_DIR}/image-cleanup.log" 2>&1 || true
IMAGE_LOAD_ATTEMPTED=false
log "GitHub-built release completed without production-side compilation: ${RELEASE_IMAGE}"
