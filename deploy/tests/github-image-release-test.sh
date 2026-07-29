#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
RECEIVER="${DEPLOY_DIR}/sub2api-github-image-release.sh"
TRIGGER="${DEPLOY_DIR}/sub2api-github-deploy-trigger.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-github-image-test.XXXXXX")"
FAKE_BIN="${TEST_ROOT}/bin"
APP_DIR="${TEST_ROOT}/app"
ARCHIVE_ROOT="${TEST_ROOT}/archive"
ARCHIVE="${TEST_ROOT}/image.tar"
WRONG_ARCHIVE="${TEST_ROOT}/wrong-image.tar"
CONFIG_FILE="${TEST_ROOT}/autodeploy.env"
RELEASE_HELPER="${TEST_ROOT}/release-helper.sh"
RELEASE_CALLS="${TEST_ROOT}/release-calls.log"
DOCKER_CALLS="${TEST_ROOT}/docker-calls.log"
SUDO_CALLS="${TEST_ROOT}/sudo-calls.log"

COMMIT="0123456789abcdef0123456789abcdef01234567"
VERSION="0.1.test+github"
LOADED_IMAGE_ID="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
WRONG_DIGEST="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
INCOMING_IMAGE="sub2api:github-${COMMIT}"
EXPECTED_SOURCE="https://github.com/Turtle-Li/sub2api"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local expected="$2"
  if ! grep -Fq -- "$expected" "$file"; then
    sed -n '1,160p' "$file" >&2
    fail "expected '${expected}' in ${file}"
  fi
}

line_count() {
  local file="$1"
  if [ -f "$file" ]; then
    wc -l <"$file" | tr -d '[:space:]'
  else
    printf '0\n'
  fi
}

mkdir -p "$FAKE_BIN" "$APP_DIR/scripts" "$ARCHIVE_ROOT"

cat >"$CONFIG_FILE" <<EOF
SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL=https://github.com/Turtle-Li/sub2api.git
SUB2API_GITHUB_IMAGE_SOURCE=${EXPECTED_SOURCE}
SUB2API_PUBLIC_HEALTH_URL=https://example.invalid/health
EOF

cat >"${ARCHIVE_ROOT}/manifest.json" <<EOF
[{"Config":"config.json","RepoTags":["${INCOMING_IMAGE}"],"Layers":[]}]
EOF
printf '{}\n' >"${ARCHIVE_ROOT}/config.json"
tar -cf "$ARCHIVE" -C "$ARCHIVE_ROOT" manifest.json config.json
ARCHIVE_DIGEST="sha256:$(sha256sum "$ARCHIVE" | awk '{print $1}')"

cat >"${ARCHIVE_ROOT}/manifest.json" <<EOF
[{"Config":"config.json","RepoTags":["sub2api:github-deadbeef"],"Layers":[]}]
EOF
tar -cf "$WRONG_ARCHIVE" -C "$ARCHIVE_ROOT" manifest.json config.json
WRONG_ARCHIVE_DIGEST="sha256:$(sha256sum "$WRONG_ARCHIVE" | awk '{print $1}')"

cat >"${FAKE_BIN}/zstd" <<'EOF'
#!/usr/bin/env bash
set -eu
archive=""
for argument in "$@"; do
  archive="$argument"
done
case "${1:-}" in
  -t)
    [ -s "$archive" ]
    ;;
  -dc)
    /bin/cat "$archive"
    ;;
  *)
    exit 2
    ;;
esac
EOF
chmod +x "${FAKE_BIN}/zstd"

cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${FAKE_BIN}/flock"

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_DOCKER_CALLS"
case "${1:-}" in
  image)
    case "${2:-}" in
      load)
        /bin/cat >/dev/null
        printf 'Loaded image: %s\n' "$FAKE_INCOMING_IMAGE"
        ;;
      inspect)
        format="${5:-}"
        case "$format" in
          '{{.Id}}') printf '%s\n' "$FAKE_IMAGE_ID" ;;
          '{{.Architecture}}') printf 'amd64\n' ;;
          '{{.Os}}') printf 'linux\n' ;;
          *org.opencontainers.image.source*) printf '%s\n' "$FAKE_EXPECTED_SOURCE" ;;
          *org.opencontainers.image.revision*) printf '%s\n' "$FAKE_COMMIT" ;;
          *org.opencontainers.image.version*) printf '%s\n' "$FAKE_VERSION" ;;
          *) exit 3 ;;
        esac
        ;;
      rm)
        exit 0
        ;;
      *)
        exit 2
        ;;
    esac
    ;;
  tag)
    exit 0
    ;;
  build|buildx)
    exit 91
    ;;
  *)
    exit 2
    ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

cat >"$RELEASE_HELPER" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_RELEASE_CALLS"
exit 0
EOF
chmod +x "$RELEASE_HELPER"

run_receiver() {
  local input_archive="$1"
  shift
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_COMMIT="$COMMIT" \
    FAKE_DOCKER_CALLS="$DOCKER_CALLS" \
    FAKE_EXPECTED_SOURCE="$EXPECTED_SOURCE" \
    FAKE_IMAGE_ID="$LOADED_IMAGE_ID" \
    FAKE_INCOMING_IMAGE="$INCOMING_IMAGE" \
    FAKE_RELEASE_CALLS="$RELEASE_CALLS" \
    FAKE_VERSION="$VERSION" \
    SUB2API_APP_DIR="$APP_DIR" \
    SUB2API_AUTODEPLOY_CONFIG_FILE="$CONFIG_FILE" \
    SUB2API_GITHUB_IMAGE_LOCK_FILE="${TEST_ROOT}/image.lock" \
    SUB2API_GITHUB_IMAGE_MAX_BYTES=1048576 \
    SUB2API_GITHUB_IMAGE_UPLOAD_ROOT="${TEST_ROOT}/incoming" \
    SUB2API_RELEASE_LOG_DIR="${TEST_ROOT}/logs" \
    SUB2API_SERVER_RELEASE_HELPER="$RELEASE_HELPER" \
    /bin/bash "$RECEIVER" "$@" <"$input_archive"
}

success_output="${TEST_ROOT}/success.log"
run_receiver "$ARCHIVE" "$COMMIT" "$VERSION" "$ARCHIVE_DIGEST" \
  >"$success_output" 2>&1
assert_contains "$success_output" \
  'GitHub-built release completed without production-side compilation'
assert_contains "$DOCKER_CALLS" 'image load'
assert_contains "$DOCKER_CALLS" "image inspect ${INCOMING_IMAGE} --format {{.Id}}"
assert_contains "$DOCKER_CALLS" "tag ${INCOMING_IMAGE} sub2api:auto-"
assert_contains "$RELEASE_CALLS" '--prebuilt sub2api:auto-'
assert_contains "$RELEASE_CALLS" "$COMMIT $VERSION https://example.invalid/health gha-"
if grep -Eq '(^| )build(x)?( |$)' "$DOCKER_CALLS"; then
  fail 'image receiver attempted to build on the production host'
fi
candidate_count="$(find "${TEST_ROOT}/logs" -name candidate.env -type f | wc -l | tr -d '[:space:]')"
[ "$candidate_count" = "1" ] || fail "expected one candidate record, got ${candidate_count}"
candidate_file="$(find "${TEST_ROOT}/logs" -name candidate.env -type f)"
assert_contains "$candidate_file" "archive_digest=${ARCHIVE_DIGEST}"
assert_contains "$candidate_file" "loaded_image_id=${LOADED_IMAGE_ID}"

release_count_before="$(line_count "$RELEASE_CALLS")"
loads_before="$(grep -c '^image load$' "$DOCKER_CALLS" || true)"
wrong_digest_output="${TEST_ROOT}/wrong-digest.log"
if run_receiver "$ARCHIVE" "$COMMIT" "$VERSION" "$WRONG_DIGEST" \
  >"$wrong_digest_output" 2>&1; then
  fail 'receiver accepted a mismatched archive digest'
fi
assert_contains "$wrong_digest_output" 'archive digest mismatch'
[ "$(line_count "$RELEASE_CALLS")" = "$release_count_before" ] \
  || fail 'release helper ran after an archive digest mismatch'
loads_after="$(grep -c '^image load$' "$DOCKER_CALLS" || true)"
[ "$loads_after" = "$loads_before" ] \
  || fail 'receiver loaded an archive whose digest was invalid'

wrong_manifest_output="${TEST_ROOT}/wrong-manifest.log"
if run_receiver "$WRONG_ARCHIVE" "$COMMIT" "$VERSION" "$WRONG_ARCHIVE_DIGEST" \
  >"$wrong_manifest_output" 2>&1; then
  fail 'receiver accepted a Docker archive with an unexpected tag'
fi
assert_contains "$wrong_manifest_output" 'Docker archive manifest validation failed'
loads_after="$(grep -c '^image load$' "$DOCKER_CALLS" || true)"
[ "$loads_after" = "$loads_before" ] \
  || fail 'receiver loaded an archive whose manifest tag was invalid'

cat >"${APP_DIR}/scripts/sub2api-github-image-release.sh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
# The forced-command user only needs to see the root-owned receiver path; sudo
# performs the actual execute permission check as root.
chmod 644 "${APP_DIR}/scripts/sub2api-github-image-release.sh"

cat >"${FAKE_BIN}/sudo" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_SUDO_CALLS"
exit 0
EOF
chmod +x "${FAKE_BIN}/sudo"

trigger_receiver="${APP_DIR}/scripts/sub2api-github-image-release.sh"
SSH_ORIGINAL_COMMAND="deploy-image ${COMMIT} ${VERSION} ${ARCHIVE_DIGEST}" \
  FAKE_SUDO_CALLS="$SUDO_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_SUDO_BIN="${FAKE_BIN}/sudo" \
  /bin/bash "$TRIGGER"
assert_contains "$SUDO_CALLS" \
  "-n ${trigger_receiver} ${COMMIT} ${VERSION} ${ARCHIVE_DIGEST}"

sudo_count_before="$(line_count "$SUDO_CALLS")"
if SSH_ORIGINAL_COMMAND="deploy-image ${COMMIT} ${VERSION} ${ARCHIVE_DIGEST} extra" \
  FAKE_SUDO_CALLS="$SUDO_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_SUDO_BIN="${FAKE_BIN}/sudo" \
  /bin/bash "$TRIGGER" >/dev/null 2>&1; then
  fail 'forced-command handler accepted an extra argument'
fi
[ "$(line_count "$SUDO_CALLS")" = "$sudo_count_before" ] \
  || fail 'invalid forced command reached sudo'

if SSH_ORIGINAL_COMMAND=deploy \
  FAKE_SUDO_CALLS="$SUDO_CALLS" \
  SUB2API_APP_DIR="$APP_DIR" \
  SUB2API_SUDO_BIN="${FAKE_BIN}/sudo" \
  /bin/bash "$TRIGGER" >/dev/null 2>&1; then
  fail 'forced-command handler retained the legacy server-build trigger'
fi
[ "$(line_count "$SUDO_CALLS")" = "$sudo_count_before" ] \
  || fail 'legacy server-build command reached sudo'

printf 'GitHub-built image receiver and forced-command tests passed.\n'
