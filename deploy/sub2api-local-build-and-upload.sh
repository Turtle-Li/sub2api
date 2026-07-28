#!/usr/bin/env bash

# Build the production linux/amd64 image on the operator workstation, then
# stream it to the production Docker daemon.  The matching release helper
# resolves this exact tag from SUB2API_RELEASE_PREBUILT_IMAGE_PREFIX and never
# compiles on the production host.

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
TARGET_HOST="${SUB2API_BUILD_TARGET_HOST:-sub2api-new}"
IMAGE_PREFIX="${SUB2API_RELEASE_PREBUILT_IMAGE_PREFIX:-sub2api:prebuilt-}"

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

case "$IMAGE_PREFIX" in
  ''|*[!A-Za-z0-9./:_-]*) die 'SUB2API_RELEASE_PREBUILT_IMAGE_PREFIX is invalid' ;;
esac

command -v docker >/dev/null 2>&1 || die 'docker is required'
command -v git >/dev/null 2>&1 || die 'git is required'
command -v ssh >/dev/null 2>&1 || die 'ssh is required'

COMMIT="$(git -C "$REPO_DIR" rev-parse HEAD)"
VERSION="$(tr -d '[:space:]' <"${REPO_DIR}/backend/cmd/server/VERSION")"
[ -n "$VERSION" ] || die 'backend/cmd/server/VERSION is empty'
IMAGE="${IMAGE_PREFIX}${COMMIT}"
BUILD_DATE="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

printf 'Building %s for linux/amd64 on this machine...\n' "$IMAGE"
docker buildx build \
  --platform linux/amd64 \
  --load \
  --tag "$IMAGE" \
  --build-arg "COMMIT=${COMMIT}" \
  --build-arg "VERSION=${VERSION}" \
  --build-arg "DATE=${BUILD_DATE}" \
  --file "${REPO_DIR}/Dockerfile" \
  "$REPO_DIR"

docker image inspect "$IMAGE" >/dev/null || die "local image was not created: ${IMAGE}"
printf 'Uploading %s to %s...\n' "$IMAGE" "$TARGET_HOST"
docker image save "$IMAGE" | ssh "$TARGET_HOST" 'docker image load'
ssh "$TARGET_HOST" "docker image inspect '${IMAGE}' >/dev/null"

printf 'Prebuilt image is available on production: %s\n' "$IMAGE"
printf 'Push this commit only after the image upload succeeds.\n'
