#!/usr/bin/env bash

# Fetch the configured Git refs, build a disposable merge candidate on the
# production host, and hand the verified image to the existing blue-green
# release mechanism. It is normally started by an authenticated GitHub Actions
# push event. This deliberately never pushes merge commits or merges official
# upstream changes unless an operator explicitly opts in.

set -Eeuo pipefail

# systemd supplies this file through EnvironmentFile, while operators may run
# `--check` directly over SSH. Load the same trusted root-owned file here so
# both entry points resolve the identical release configuration.
CONFIG_FILE="${SUB2API_AUTODEPLOY_CONFIG_FILE:-/etc/sub2api-autodeploy.env}"
if [ -r "$CONFIG_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  set +a
fi

APP_DIR="${SUB2API_APP_DIR:-/opt/sub2api}"
STATE_ROOT="${SUB2API_AUTODEPLOY_STATE_DIR:-/var/lib/sub2api-autodeploy}"
REPO_DIR="${SUB2API_AUTODEPLOY_REPO_DIR:-${STATE_ROOT}/repo}"
WORK_ROOT="${SUB2API_AUTODEPLOY_WORK_ROOT:-${STATE_ROOT}/worktrees}"
LOCK_FILE="${SUB2API_AUTODEPLOY_LOCK_FILE:-/var/lock/sub2api-autodeploy.lock}"
RELEASE_HELPER="${SUB2API_SERVER_RELEASE_HELPER:-${APP_DIR}/scripts/sub2api-server-release.sh}"

PRODUCTION_REMOTE="${SUB2API_AUTODEPLOY_PRODUCTION_REMOTE:-fork}"
PRODUCTION_REPO_URL="${SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL:-}"
PRODUCTION_BRANCH="${SUB2API_AUTODEPLOY_PRODUCTION_BRANCH:-main}"

MAIN_REMOTE="${SUB2API_AUTODEPLOY_MAIN_REMOTE:-${PRODUCTION_REMOTE}}"
MAIN_REPO_URL="${SUB2API_AUTODEPLOY_MAIN_REPO_URL:-${PRODUCTION_REPO_URL}}"
MAIN_BRANCH="${SUB2API_AUTODEPLOY_MAIN_BRANCH:-main}"
MERGE_MAIN="${SUB2API_AUTODEPLOY_MERGE_MAIN:-true}"

UPSTREAM_REMOTE="${SUB2API_AUTODEPLOY_UPSTREAM_REMOTE:-origin}"
UPSTREAM_REPO_URL="${SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL:-}"
UPSTREAM_BRANCH="${SUB2API_AUTODEPLOY_UPSTREAM_BRANCH:-main}"
MERGE_UPSTREAM="${SUB2API_AUTODEPLOY_MERGE_UPSTREAM:-false}"

PUBLIC_HEALTH_URL="${SUB2API_PUBLIC_HEALTH_URL:-https://www.turtleligpt.com/health}"
FAILURE_RETRY_SECONDS="${SUB2API_AUTODEPLOY_FAILURE_RETRY_SECONDS:-1800}"
RELEASE_LOG_ROOT="${SUB2API_RELEASE_LOG_DIR:-/var/log/sub2api-release}"
GIT_FETCH_FILTER="${SUB2API_AUTODEPLOY_GIT_FETCH_FILTER:-blob:none}"
GIT_AUTHOR_NAME="${SUB2API_AUTODEPLOY_GIT_AUTHOR_NAME:-Sub2API Auto Deploy}"
GIT_AUTHOR_EMAIL="${SUB2API_AUTODEPLOY_GIT_AUTHOR_EMAIL:-sub2api-autodeploy@localhost}"

LAST_SUCCESS_FILE="${STATE_ROOT}/last-successful.env"
LAST_FAILURE_FILE="${STATE_ROOT}/last-failed.env"

CHECK_ONLY=false
FORCE=false
WORKTREE=""

usage() {
  cat <<'EOF'
Usage: sub2api-autodeploy.sh [--check] [--force]

  --check  Fetch refs and validate the merge candidate without building,
           switching traffic, or recording release state.
  --force  Build even when the same source fingerprint was released before.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --check)
      CHECK_ONLY=true
      ;;
    --force)
      FORCE=true
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

timestamp() {
  date '+%Y-%m-%d %H:%M:%S'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

die() {
  log "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

require_bool() {
  case "$2" in
    true|false) ;;
    *) die "$1 must be true or false" ;;
  esac
}

state_value() {
  local file="$1"
  local key="$2"
  [ -r "$file" ] || return 0
  sed -n "s/^${key}=//p" "$file" | head -n 1
}

write_state() {
  local file="$1"
  shift
  local temp_file
  temp_file="$(mktemp "${STATE_ROOT}/.state.XXXXXX")"
  umask 077
  printf '%s\n' "$@" >"$temp_file"
  mv -f "$temp_file" "$file"
}

record_failure() {
  local reason="$1"
  [ "$CHECK_ONLY" = "true" ] && return 0
  write_state "$LAST_FAILURE_FILE" \
    "fingerprint=${FINGERPRINT}" \
    "failed_at_epoch=$(date +%s)" \
    "reason=${reason}" \
    "production_commit=${PRODUCTION_COMMIT}" \
    "main_commit=${MAIN_COMMIT}" \
    "upstream_commit=${UPSTREAM_COMMIT}"
}

cleanup() {
  local status=$?
  set +e
  if [ -n "$WORKTREE" ] && [ -d "$WORKTREE" ]; then
    git -C "$REPO_DIR" worktree remove --force "$WORKTREE" >/dev/null 2>&1 || rm -rf "$WORKTREE"
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

ensure_remote() {
  local remote="$1"
  local url="$2"
  local current_url

  current_url="$(git -C "$REPO_DIR" remote get-url "$remote" 2>/dev/null || true)"
  if [ -z "$current_url" ]; then
    git -C "$REPO_DIR" remote add "$remote" "$url"
  elif [ "$current_url" != "$url" ]; then
    git -C "$REPO_DIR" remote set-url "$remote" "$url"
  fi

  # Keep the full commit graph for reliable merge-base checks while avoiding a
  # one-time download of every historical source blob. Git lazily retrieves
  # the candidate's current-tree blobs when the worktree is created.
  git -C "$REPO_DIR" config "remote.${remote}.promisor" true
  git -C "$REPO_DIR" config "remote.${remote}.partialclonefilter" "$GIT_FETCH_FILTER"
}

fetch_branch() {
  local remote="$1"
  local branch="$2"
  local refspec="refs/heads/${branch}:refs/remotes/${remote}/${branch}"

  if ! git -C "$REPO_DIR" fetch --prune --no-tags --filter="$GIT_FETCH_FILTER" "$remote" "$refspec"; then
    log "Filtered fetch failed for ${remote}/${branch}; retrying without a filter"
    git -C "$REPO_DIR" fetch --prune --no-tags "$remote" "$refspec"
  fi
}

merge_ref_if_needed() {
  local ref="$1"

  if git -C "$WORKTREE" merge-base --is-ancestor "$ref" HEAD; then
    log "Candidate already contains ${ref}"
    return 0
  fi

  log "Merging ${ref} into the disposable release candidate"
  if ! git -C "$WORKTREE" \
    -c user.name="$GIT_AUTHOR_NAME" \
    -c user.email="$GIT_AUTHOR_EMAIL" \
    merge --no-edit "$ref"; then
    git -C "$WORKTREE" diff --name-only --diff-filter=U >&2 || true
    git -C "$WORKTREE" merge --abort >/dev/null 2>&1 || true
    return 1
  fi
}

for command_name in git flock sha256sum awk sed mktemp; do
  require_cmd "$command_name"
done

require_bool SUB2API_AUTODEPLOY_MERGE_MAIN "$MERGE_MAIN"
require_bool SUB2API_AUTODEPLOY_MERGE_UPSTREAM "$MERGE_UPSTREAM"
case "$FAILURE_RETRY_SECONDS" in
  ''|*[!0-9]*) die "SUB2API_AUTODEPLOY_FAILURE_RETRY_SECONDS must be a non-negative integer" ;;
esac
[ -n "$PRODUCTION_REPO_URL" ] || die "SUB2API_AUTODEPLOY_PRODUCTION_REPO_URL is required"
[ -x "$RELEASE_HELPER" ] || die "release helper is missing or not executable: ${RELEASE_HELPER}"

if [ "$MERGE_MAIN" = "true" ]; then
  [ -n "$MAIN_REPO_URL" ] || die "SUB2API_AUTODEPLOY_MAIN_REPO_URL is required when main merging is enabled"
fi
if [ "$MERGE_UPSTREAM" = "true" ]; then
  [ -n "$UPSTREAM_REPO_URL" ] || die "SUB2API_AUTODEPLOY_UPSTREAM_REPO_URL is required when upstream merging is enabled"
fi

mkdir -p "$STATE_ROOT" "$WORK_ROOT"
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  log "Another automatic release check is already running; skipping"
  exit 0
fi

if [ ! -d "${REPO_DIR}/.git" ]; then
  log "Initializing server-side Git cache: ${REPO_DIR}"
  git init "$REPO_DIR" >/dev/null
fi

git -C "$REPO_DIR" check-ref-format --branch "$PRODUCTION_BRANCH" >/dev/null
git -C "$REPO_DIR" check-ref-format --branch "$MAIN_BRANCH" >/dev/null
git -C "$REPO_DIR" check-ref-format --branch "$UPSTREAM_BRANCH" >/dev/null

ensure_remote "$PRODUCTION_REMOTE" "$PRODUCTION_REPO_URL"
if [ "$MERGE_MAIN" = "true" ]; then
  ensure_remote "$MAIN_REMOTE" "$MAIN_REPO_URL"
fi
if [ "$MERGE_UPSTREAM" = "true" ]; then
  ensure_remote "$UPSTREAM_REMOTE" "$UPSTREAM_REPO_URL"
fi

log "Fetching ${PRODUCTION_REMOTE}/${PRODUCTION_BRANCH}"
fetch_branch "$PRODUCTION_REMOTE" "$PRODUCTION_BRANCH"

if [ "$MERGE_MAIN" = "true" ]; then
  log "Fetching ${MAIN_REMOTE}/${MAIN_BRANCH}"
  fetch_branch "$MAIN_REMOTE" "$MAIN_BRANCH"
fi
if [ "$MERGE_UPSTREAM" = "true" ]; then
  log "Fetching ${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}"
  fetch_branch "$UPSTREAM_REMOTE" "$UPSTREAM_BRANCH"
fi

PRODUCTION_REF="${PRODUCTION_REMOTE}/${PRODUCTION_BRANCH}"
PRODUCTION_COMMIT="$(git -C "$REPO_DIR" rev-parse "${PRODUCTION_REF}^{commit}")"
MAIN_COMMIT="-"
UPSTREAM_COMMIT="-"

if [ "$MERGE_MAIN" = "true" ]; then
  MAIN_COMMIT="$(git -C "$REPO_DIR" rev-parse "${MAIN_REMOTE}/${MAIN_BRANCH}^{commit}")"
fi
if [ "$MERGE_UPSTREAM" = "true" ]; then
  UPSTREAM_COMMIT="$(git -C "$REPO_DIR" rev-parse "${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}^{commit}")"
fi

FINGERPRINT="$(
  printf 'production=%s\nmain=%s\nupstream=%s\npolicy=server-build-v1\n' \
    "$PRODUCTION_COMMIT" "$MAIN_COMMIT" "$UPSTREAM_COMMIT" \
    | sha256sum | awk '{print $1}'
)"

if [ "$FORCE" != "true" ] && [ "$(state_value "$LAST_SUCCESS_FILE" fingerprint)" = "$FINGERPRINT" ]; then
  log "No source change since the successful release (${FINGERPRINT:0:12}); skipping"
  exit 0
fi

if [ "$FORCE" != "true" ]; then
  failed_fingerprint="$(state_value "$LAST_FAILURE_FILE" fingerprint)"
  failed_at="$(state_value "$LAST_FAILURE_FILE" failed_at_epoch)"
  now_epoch="$(date +%s)"
  if [ "$failed_fingerprint" = "$FINGERPRINT" ] \
    && [[ "$failed_at" =~ ^[0-9]+$ ]] \
    && [ $((now_epoch - failed_at)) -lt "$FAILURE_RETRY_SECONDS" ]; then
    log "The same candidate failed recently; retrying after ${FAILURE_RETRY_SECONDS}s or with --force"
    exit 0
  fi
fi

RUN_ID="auto-$(date '+%Y%m%d-%H%M%S')-$$"
WORKTREE="$(mktemp -d "${WORK_ROOT}/release.XXXXXX")"
rmdir "$WORKTREE"
log "Creating release candidate from ${PRODUCTION_REF}@${PRODUCTION_COMMIT:0:12}"
git -C "$REPO_DIR" worktree add --quiet --detach "$WORKTREE" "$PRODUCTION_COMMIT" >/dev/null

if [ "$MERGE_MAIN" = "true" ]; then
  if ! merge_ref_if_needed "${MAIN_REMOTE}/${MAIN_BRANCH}"; then
    record_failure "main-merge-conflict"
    die "main merge conflict; production was not changed"
  fi
fi
if [ "$MERGE_UPSTREAM" = "true" ]; then
  if ! merge_ref_if_needed "${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}"; then
    record_failure "upstream-merge-conflict"
    die "upstream merge conflict; production was not changed"
  fi
fi

CANDIDATE_COMMIT="$(git -C "$WORKTREE" rev-parse HEAD)"
CANDIDATE_TREE="$(git -C "$WORKTREE" rev-parse HEAD^{tree})"
VERSION="$(tr -d '[:space:]' < "${WORKTREE}/backend/cmd/server/VERSION")"
[ -n "$VERSION" ] || die "release version is empty"
SHORT_COMMIT="$(git -C "$WORKTREE" rev-parse --short=8 HEAD)"
IMAGE_TAG="sub2api:auto-$(date '+%Y%m%d-%H%M')-${SHORT_COMMIT}"

log "Candidate: ${CANDIDATE_COMMIT} (tree ${CANDIDATE_TREE:0:12}, version ${VERSION})"
if [ "$CHECK_ONLY" = "true" ]; then
  log "Check passed; a normal run would build ${IMAGE_TAG} and then use blue-green release"
  exit 0
fi

mkdir -p "${RELEASE_LOG_ROOT}/${RUN_ID}"
write_state "${RELEASE_LOG_ROOT}/${RUN_ID}/candidate.env" \
  "run_id=${RUN_ID}" \
  "fingerprint=${FINGERPRINT}" \
  "production_ref=${PRODUCTION_REF}" \
  "production_commit=${PRODUCTION_COMMIT}" \
  "main_commit=${MAIN_COMMIT}" \
  "upstream_commit=${UPSTREAM_COMMIT}" \
  "candidate_commit=${CANDIDATE_COMMIT}" \
  "candidate_tree=${CANDIDATE_TREE}" \
  "version=${VERSION}" \
  "image=${IMAGE_TAG}"

log "Building on this server; no local frontend build or source upload is involved"
if ! SUB2API_AUTODEPLOY_WORK_ROOT="$WORK_ROOT" \
  "$RELEASE_HELPER" "$WORKTREE" "$IMAGE_TAG" "$CANDIDATE_COMMIT" "$VERSION" \
  "$PUBLIC_HEALTH_URL" "$RUN_ID"; then
  record_failure "server-build-or-release"
  die "candidate failed before a verified release; production was not changed or was rolled back"
fi

write_state "$LAST_SUCCESS_FILE" \
  "fingerprint=${FINGERPRINT}" \
  "released_at=$(date -Is)" \
  "production_ref=${PRODUCTION_REF}" \
  "production_commit=${PRODUCTION_COMMIT}" \
  "main_commit=${MAIN_COMMIT}" \
  "upstream_commit=${UPSTREAM_COMMIT}" \
  "candidate_commit=${CANDIDATE_COMMIT}" \
  "candidate_tree=${CANDIDATE_TREE}" \
  "version=${VERSION}" \
  "image=${IMAGE_TAG}"
rm -f "$LAST_FAILURE_FILE"
log "Automatic release completed: ${IMAGE_TAG}"
