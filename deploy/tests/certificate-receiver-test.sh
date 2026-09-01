#!/usr/bin/env bash

set -Eeuo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
RECEIVER="${DEPLOY_DIR}/sub2api-cert-receiver.sh"
TRIGGER="${DEPLOY_DIR}/sub2api-cert-deploy-trigger.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-cert-receiver-test.XXXXXX")"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
FAKE_BIN="${TEST_ROOT}/bin"
CERT_ROOT="${TEST_ROOT}/certificates"
CONFIG_FILE="${TEST_ROOT}/receiver.env"
CADDYFILE="${TEST_ROOT}/Caddyfile"
DOCKER_CALLS="${TEST_ROOT}/docker.log"
SUDO_CALLS="${TEST_ROOT}/sudo.log"
SERVED_CERT="${TEST_ROOT}/served-fullchain.pem"
LOCK_DIR="${TEST_ROOT}/locks"
REAL_OPENSSL="$(command -v openssl)"
DOMAIN="api.turtleligpt.com"

cleanup() {
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local expected="$2"
  grep -Fq -- "$expected" "$file" || {
    sed -n '1,200p' "$file" >&2 || true
    fail "expected '${expected}' in ${file}"
  }
}

assert_exact() {
  local file="$1"
  local expected="$2"
  local actual
  actual="$(<"$file")"
  [ "$actual" = "$expected" ] || fail "expected exact response '${expected}', got '${actual}'"
}

assert_link_generation() {
  local link_path="$1"
  local expected="$2"
  [ -L "$link_path" ] || fail "expected symlink ${link_path}"
  [ "$(readlink "$link_path")" = "generations/${expected}" ] \
    || fail "expected container-portable relative generation link for ${link_path}"
}

assert_no_link() {
  local link_path="$1"
  local message="$2"
  [ ! -e "$link_path" ] && [ ! -L "$link_path" ] || fail "$message"
}

file_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

assert_private_lock_directory() {
  local path="$1"
  local mode
  mode="$(file_mode "$path")"
  [ "$mode" = 700 ] || fail "shared lock directory mode changed: ${mode}"
}

mkdir -p "$FAKE_BIN" "$CERT_ROOT" "$LOCK_DIR"
chmod 700 "$LOCK_DIR"

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_DOCKER_CALLS"
if [ "${1:-}" != exec ]; then
  exit 2
fi
shift
if [ "${1:-}" = -i ]; then
  shift
  cat >/dev/null
fi
container="${1:-}"
shift
[ "$container" = sub2api-caddy ] || exit 3
[ "${1:-}" = caddy ] || exit 4
case " ${*} " in
  *' reload '*)
    case " ${*} " in
      *' --force '*) ;;
      *) exit 90 ;;
    esac
    if [ -f "$FAKE_FAIL_RELOAD" ]; then
      rm -f -- "$FAKE_FAIL_RELOAD"
      exit 91
    fi
    if [ -f "$FAKE_FAIL_RELOAD_COUNT" ]; then
      remaining="$(cat "$FAKE_FAIL_RELOAD_COUNT")"
      case "$remaining" in ''|*[!0-9]*) exit 92 ;; esac
      if [ "$remaining" -gt 0 ]; then
        remaining=$((remaining - 1))
        printf '%s\n' "$remaining" >"$FAKE_FAIL_RELOAD_COUNT"
        exit 91
      fi
    fi
    cp "$FAKE_CERT_ROOT/current/fullchain.pem" "$FAKE_SERVED_CERT"
    ;;
  *' validate '*) ;;
  *) exit 5 ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker"

cat >"${FAKE_BIN}/curl" <<'EOF'
#!/usr/bin/env bash
set -eu
[ ! -f "$FAKE_FAIL_CURL" ]
EOF
chmod +x "${FAKE_BIN}/curl"

cat >"${FAKE_BIN}/openssl" <<'EOF'
#!/usr/bin/env bash
set -eu
if [ "${1:-}" = s_client ]; then
  cat "$FAKE_SERVED_CERT"
  exit 0
fi
exec "$FAKE_REAL_OPENSSL" "$@"
EOF
chmod +x "${FAKE_BIN}/openssl"

# GNU mv -T is used on the Linux nodes. This compatibility wrapper keeps the
# repository test runnable on macOS while preserving the receiver call shape.
cat >"${FAKE_BIN}/mv" <<'EOF'
#!/usr/bin/env bash
set -eu
if [ "${1:-}" = -Tf ]; then
  shift
  [ "${1:-}" != -- ] || shift
  source_path="$1"
  destination_path="$2"
  rm -f -- "$destination_path"
  exec /bin/mv -f "$source_path" "$destination_path"
fi
if [ "${1:-}" = -- ]; then
  shift
elif [ "${1:-}" = -f ] && [ "${2:-}" = -- ]; then
  shift 2
  set -- -f "$@"
fi
exec /bin/mv "$@"
EOF
chmod +x "${FAKE_BIN}/mv"

cat >"${FAKE_BIN}/sudo" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$FAKE_SUDO_CALLS"
exit 0
EOF
chmod +x "${FAKE_BIN}/sudo"

cat >"${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${FAKE_BIN}/flock"

cat >"$CADDYFILE" <<EOF
${DOMAIN} {
  tls /etc/sub2api-certs/current/fullchain.pem /etc/sub2api-certs/current/privkey.pem
  reverse_proxy sub2api:8080
}
EOF

cat >"$CONFIG_FILE" <<EOF
SUB2API_CERT_ROOT=${CERT_ROOT}
SUB2API_CERT_DOMAIN=${DOMAIN}
SUB2API_CERT_CADDY_CONTAINER=sub2api-caddy
SUB2API_CERT_CADDYFILE_HOST_PATH=${CADDYFILE}
SUB2API_CERT_CADDYFILE_CONTAINER_PATH=/etc/caddy/Caddyfile
SUB2API_CERT_CADDY_CERT_ROOT=/etc/sub2api-certs
SUB2API_CERT_TLS_VERIFY_IP=127.0.0.1
SUB2API_CERT_TLS_VERIFY_PORT=443
SUB2API_CERT_LOCK_FILE=${LOCK_DIR}/cert.lock
SUB2API_MAINTENANCE_LOCK_FILE=${LOCK_DIR}/maintenance.lock
EOF

make_generation() {
  local generation="$1"
  local directory="${TEST_ROOT}/${generation}"
  mkdir -p "$directory"
  "$REAL_OPENSSL" req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
    -nodes -days 30 -subj "/CN=${DOMAIN}" \
    -addext "subjectAltName=DNS:${DOMAIN}" \
    -keyout "${directory}/privkey.pem" -out "${directory}/fullchain.pem" \
    >/dev/null 2>&1
  tar -cf "${directory}.tar" -C "$directory" fullchain.pem privkey.pem
}

certificate_digest() {
  "$REAL_OPENSSL" x509 -in "$1" -outform DER 2>/dev/null \
    | "$REAL_OPENSSL" dgst -sha256 -r | awk '{print $1}'
}

public_key_digest() {
  "$REAL_OPENSSL" pkey -in "$1" -pubout -outform DER 2>/dev/null \
    | "$REAL_OPENSSL" dgst -sha256 -r | awk '{print $1}'
}

run_receiver() {
  env \
    PATH="${FAKE_BIN}:${PATH}" \
    FAKE_CERT_ROOT="$CERT_ROOT" \
    FAKE_DOCKER_CALLS="$DOCKER_CALLS" \
    FAKE_FAIL_CURL="${TEST_ROOT}/fail-curl" \
    FAKE_FAIL_RELOAD="${TEST_ROOT}/fail-reload" \
    FAKE_FAIL_RELOAD_COUNT="${TEST_ROOT}/fail-reload-count" \
    FAKE_SERVED_CERT="$SERVED_CERT" \
    FAKE_REAL_OPENSSL="$REAL_OPENSSL" \
    SUB2API_CERT_RECEIVER_ALLOW_NON_ROOT_FOR_TESTS=1 \
    SUB2API_CERT_RECEIVER_CONFIG_FILE="$CONFIG_FILE" \
    /bin/bash "$RECEIVER" "$@"
}

make_generation cert-one
GEN_ONE_ARCHIVE="${TEST_ROOT}/cert-one.tar"
GEN_ONE_CERT_SHA="$(certificate_digest "${TEST_ROOT}/cert-one/fullchain.pem")"
GEN_ONE_KEY_SHA="$(public_key_digest "${TEST_ROOT}/cert-one/privkey.pem")"
GEN_ONE="${GEN_ONE_CERT_SHA:0:20}"

prepare_one_output="${TEST_ROOT}/prepare-one.log"
run_receiver prepare "$GEN_ONE" "$GEN_ONE_CERT_SHA" "$GEN_ONE_KEY_SHA" 604800 "$DOMAIN" \
  <"$GEN_ONE_ARCHIVE" >"$prepare_one_output"
assert_exact "$prepare_one_output" "PREPARED ${GEN_ONE} ${GEN_ONE_CERT_SHA}"
assert_private_lock_directory "$LOCK_DIR"
[ ! -e "$CERT_ROOT/current" ] || fail 'prepare changed the active certificate generation'
[ "$(file_mode "$CERT_ROOT/generations/${GEN_ONE}/privkey.pem")" = 600 ] \
  || fail 'private key mode is not 0600'

prepare_idempotent_output="${TEST_ROOT}/prepare-idempotent.log"
run_receiver prepare "$GEN_ONE" "$GEN_ONE_CERT_SHA" "$GEN_ONE_KEY_SHA" 604800 "$DOMAIN" \
  <"$GEN_ONE_ARCHIVE" >"$prepare_idempotent_output"
assert_exact "$prepare_idempotent_output" "PREPARED ${GEN_ONE} ${GEN_ONE_CERT_SHA}"

bad_cert_sha="$(printf '%064d' 0)"
bad_generation="${bad_cert_sha:0:20}"
if run_receiver prepare "$bad_generation" "$bad_cert_sha" "$GEN_ONE_KEY_SHA" 604800 "$DOMAIN" \
  <"$GEN_ONE_ARCHIVE" >"${TEST_ROOT}/bad-digest.log" 2>&1; then
  fail 'receiver accepted a bad certificate digest'
fi
assert_contains "${TEST_ROOT}/bad-digest.log" 'certificate hash does not match prepare request'
[ ! -e "$CERT_ROOT/generations/${bad_generation}" ] || fail 'bad digest left a prepared generation'

if run_receiver prepare "$GEN_ONE" "$GEN_ONE_CERT_SHA" "$GEN_ONE_KEY_SHA" 604800 wrong.example.com \
  <"$GEN_ONE_ARCHIVE" >"${TEST_ROOT}/wrong-domain.log" 2>&1; then
  fail 'receiver accepted a mismatched domain'
fi
assert_contains "${TEST_ROOT}/wrong-domain.log" 'domain does not match'

upper_generation="$(printf '%s' "$GEN_ONE" | tr '[:lower:]' '[:upper:]')"
upper_cert_sha="$(printf '%s' "$GEN_ONE_CERT_SHA" | tr '[:lower:]' '[:upper:]')"
if run_receiver prepare "$upper_generation" "$GEN_ONE_CERT_SHA" "$GEN_ONE_KEY_SHA" 604800 "$DOMAIN" \
  </dev/null >"${TEST_ROOT}/uppercase-generation.log" 2>&1; then
  fail 'receiver accepted an uppercase generation'
fi
assert_contains "${TEST_ROOT}/uppercase-generation.log" 'invalid certificate generation'

if run_receiver prepare "$GEN_ONE" "$upper_cert_sha" "$GEN_ONE_KEY_SHA" 604800 "$DOMAIN" \
  </dev/null >"${TEST_ROOT}/uppercase-hash.log" 2>&1; then
  fail 'receiver accepted an uppercase certificate hash'
fi
assert_contains "${TEST_ROOT}/uppercase-hash.log" 'must be 64 lowercase hexadecimal characters'

malicious_dir="${TEST_ROOT}/malicious"
mkdir -p "$malicious_dir"
cp "${TEST_ROOT}/cert-one/fullchain.pem" "${malicious_dir}/fullchain.pem"
ln "${malicious_dir}/fullchain.pem" "${malicious_dir}/privkey.pem"
tar -cf "${malicious_dir}.tar" -C "$malicious_dir" fullchain.pem privkey.pem
if run_receiver prepare "$GEN_ONE" "$GEN_ONE_CERT_SHA" "$GEN_ONE_KEY_SHA" 604800 "$DOMAIN" \
  <"${malicious_dir}.tar" >"${TEST_ROOT}/hardlink.log" 2>&1; then
  fail 'receiver accepted a hard-linked archive entry'
fi
assert_contains "${TEST_ROOT}/hardlink.log" 'archive entries must be regular files'

# Installation bootstraps the existing live certificate as the first managed
# current generation before the GCP coordinator is allowed to activate a release.
ln -s "generations/$GEN_ONE" "$CERT_ROOT/current"
cp "$CERT_ROOT/current/fullchain.pem" "$SERVED_CERT"

if TMPDIR=/ run_receiver status "$DOMAIN" >"${TEST_ROOT}/unbounded-test-root.log" 2>&1; then
  fail 'receiver accepted an unbounded test-mode temporary root'
fi
assert_contains "${TEST_ROOT}/unbounded-test-root.log" 'test mode requires a bounded temporary directory'

touch "$CERT_ROOT/transaction.env.tmp.stale"
touch -t 200001010000 "$CERT_ROOT/transaction.env.tmp.stale"

status_output="${TEST_ROOT}/status.log"
run_receiver status "$DOMAIN" >"$status_output"
assert_exact "$status_output" "CURRENT ${GEN_ONE} ${GEN_ONE_CERT_SHA}"
[ ! -e "$CERT_ROOT/transaction.env.tmp.stale" ] || fail 'receiver retained a stale transaction temp file'
if grep -Fq 'PRIVATE KEY' "$status_output"; then
  fail 'status output exposed private key material'
fi

make_generation cert-two
GEN_TWO_ARCHIVE="${TEST_ROOT}/cert-two.tar"
GEN_TWO_CERT_SHA="$(certificate_digest "${TEST_ROOT}/cert-two/fullchain.pem")"
GEN_TWO_KEY_SHA="$(public_key_digest "${TEST_ROOT}/cert-two/privkey.pem")"
GEN_TWO="${GEN_TWO_CERT_SHA:0:20}"
run_receiver prepare "$GEN_TWO" "$GEN_TWO_CERT_SHA" "$GEN_TWO_KEY_SHA" 604800 "$DOMAIN" \
  <"$GEN_TWO_ARCHIVE" >"${TEST_ROOT}/prepare-two.log"
assert_exact "${TEST_ROOT}/prepare-two.log" "PREPARED ${GEN_TWO} ${GEN_TWO_CERT_SHA}"

# The coordinator issues rollback for an attempted node even when activation
# failed before a transaction was created. This must be a successful no-op.
run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/prepared-only-rollback.log"
assert_exact "${TEST_ROOT}/prepared-only-rollback.log" "ROLLED_BACK ${GEN_TWO}"
assert_link_generation "$CERT_ROOT/current" "$GEN_ONE"

touch "${TEST_ROOT}/fail-reload"
if run_receiver activate "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/activate-two-failed.log" 2>&1; then
  fail 'receiver reported success after Caddy reload failure'
fi
assert_contains "${TEST_ROOT}/activate-two-failed.log" 'previous certificate generation was restored'
assert_link_generation "$CERT_ROOT/current" "$GEN_ONE"
assert_no_link "$CERT_ROOT/previous" 'failed activation retained previous link'
assert_contains "$DOCKER_CALLS" 'caddy reload --force --config /etc/caddy/Caddyfile --adapter caddyfile'
# The GCP coordinator always follows an uncertain/failed activate with an
# idempotent rollback and then discards the now-inactive candidate.
run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/failed-activate-rollback.log"
assert_exact "${TEST_ROOT}/failed-activate-rollback.log" "ROLLED_BACK ${GEN_TWO}"
run_receiver discard "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/failed-activate-discard.log"
assert_exact "${TEST_ROOT}/failed-activate-discard.log" "DISCARDED ${GEN_TWO}"

run_receiver prepare "$GEN_TWO" "$GEN_TWO_CERT_SHA" "$GEN_TWO_KEY_SHA" 604800 "$DOMAIN" \
  <"$GEN_TWO_ARCHIVE" >"${TEST_ROOT}/prepare-two-again.log"

# Recover both possible interruption points around the atomic current-link
# switch: before it changed, and after it changed but before state became active.
cat >"$CERT_ROOT/transaction.env" <<EOF
generation=${GEN_TWO}
previous_generation=${GEN_ONE}
state=activating
EOF
ln -s "generations/$GEN_ONE" "$CERT_ROOT/previous"
touch "${TEST_ROOT}/fail-reload"
if run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/activating-before-link-failed.log" 2>&1; then
  fail 'interrupted rollback succeeded without reloading the previous generation'
fi
assert_contains "${TEST_ROOT}/activating-before-link-failed.log" 'interrupted activation rollback verification failed'
run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/activating-before-link-rollback.log"
assert_exact "${TEST_ROOT}/activating-before-link-rollback.log" "ROLLED_BACK ${GEN_TWO}"
assert_link_generation "$CERT_ROOT/current" "$GEN_ONE"
assert_no_link "$CERT_ROOT/previous" 'interrupted rollback retained previous link'

cat >"$CERT_ROOT/transaction.env" <<EOF
generation=${GEN_TWO}
previous_generation=${GEN_ONE}
state=activating
EOF
ln -s "generations/$GEN_ONE" "$CERT_ROOT/previous"
rm -f -- "$CERT_ROOT/current"
ln -s "generations/$GEN_TWO" "$CERT_ROOT/current"
run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/activating-after-link-rollback.log"
assert_exact "${TEST_ROOT}/activating-after-link-rollback.log" "ROLLED_BACK ${GEN_TWO}"
assert_link_generation "$CERT_ROOT/current" "$GEN_ONE"
assert_no_link "$CERT_ROOT/previous" 'interrupted post-link rollback retained previous link'

run_receiver activate "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/activate-two.log"
assert_exact "${TEST_ROOT}/activate-two.log" "ACTIVATED ${GEN_TWO}"
assert_link_generation "$CERT_ROOT/current" "$GEN_TWO"
assert_link_generation "$CERT_ROOT/previous" "$GEN_ONE"
run_receiver commit "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/commit.log"
assert_exact "${TEST_ROOT}/commit.log" "COMMITTED ${GEN_TWO}"

# If both the requested rollback and restoration reload fail, the receiver must
# report the stronger bounded-rollback failure instead of claiming recovery.
printf '2\n' >"${TEST_ROOT}/fail-reload-count"
if run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/rollback-restore-failed.log" 2>&1; then
  fail 'receiver reported success after rollback and restore reload both failed'
fi
assert_contains "${TEST_ROOT}/rollback-restore-failed.log" \
  'rollback failed and the original current generation could not be restored'
assert_link_generation "$CERT_ROOT/current" "$GEN_TWO"
rm -f -- "${TEST_ROOT}/fail-reload-count"

run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/rollback.log"
assert_exact "${TEST_ROOT}/rollback.log" "ROLLED_BACK ${GEN_TWO}"
assert_link_generation "$CERT_ROOT/current" "$GEN_ONE"
assert_no_link "$CERT_ROOT/previous" 'rollback retained the inactive candidate as protected history'
run_receiver rollback "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/rollback-idempotent.log"
assert_exact "${TEST_ROOT}/rollback-idempotent.log" "ROLLED_BACK ${GEN_TWO}"
run_receiver discard "$GEN_TWO" "$DOMAIN" >"${TEST_ROOT}/rollback-discard.log"
assert_exact "${TEST_ROOT}/rollback-discard.log" "DISCARDED ${GEN_TWO}"

make_generation cert-three
GEN_THREE_ARCHIVE="${TEST_ROOT}/cert-three.tar"
GEN_THREE_CERT_SHA="$(certificate_digest "${TEST_ROOT}/cert-three/fullchain.pem")"
GEN_THREE_KEY_SHA="$(public_key_digest "${TEST_ROOT}/cert-three/privkey.pem")"
GEN_THREE="${GEN_THREE_CERT_SHA:0:20}"
run_receiver prepare "$GEN_THREE" "$GEN_THREE_CERT_SHA" "$GEN_THREE_KEY_SHA" 604800 "$DOMAIN" \
  <"$GEN_THREE_ARCHIVE" >"${TEST_ROOT}/prepare-three.log"
run_receiver activate "$GEN_THREE" "$DOMAIN" >"${TEST_ROOT}/activate-three.log"
run_receiver commit "$GEN_THREE" "$DOMAIN" >"${TEST_ROOT}/commit-three.log"

make_generation cert-four
GEN_FOUR_ARCHIVE="${TEST_ROOT}/cert-four.tar"
GEN_FOUR_CERT_SHA="$(certificate_digest "${TEST_ROOT}/cert-four/fullchain.pem")"
GEN_FOUR_KEY_SHA="$(public_key_digest "${TEST_ROOT}/cert-four/privkey.pem")"
GEN_FOUR="${GEN_FOUR_CERT_SHA:0:20}"
run_receiver prepare "$GEN_FOUR" "$GEN_FOUR_CERT_SHA" "$GEN_FOUR_KEY_SHA" 604800 "$DOMAIN" \
  <"$GEN_FOUR_ARCHIVE" >"${TEST_ROOT}/prepare-four.log"
run_receiver activate "$GEN_FOUR" "$DOMAIN" >"${TEST_ROOT}/activate-four.log"
run_receiver commit "$GEN_FOUR" "$DOMAIN" >"${TEST_ROOT}/commit-four.log"
generation_count="$(find "$CERT_ROOT/generations" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d '[:space:]')"
[ "$generation_count" -le 2 ] || fail "commit retained ${generation_count} generations"
[ ! -e "$CERT_ROOT/generations/$GEN_ONE" ] || fail 'commit did not prune generation outside bounded history'
assert_link_generation "$CERT_ROOT/current" "$GEN_FOUR"
assert_link_generation "$CERT_ROOT/previous" "$GEN_THREE"
run_receiver rollback "$GEN_FOUR" "$DOMAIN" >"${TEST_ROOT}/rollback-four.log"
assert_exact "${TEST_ROOT}/rollback-four.log" "ROLLED_BACK ${GEN_FOUR}"
run_receiver discard "$GEN_FOUR" "$DOMAIN" >"${TEST_ROOT}/discard-four.log"
assert_exact "${TEST_ROOT}/discard-four.log" "DISCARDED ${GEN_FOUR}"
[ ! -e "$CERT_ROOT/generations/$GEN_FOUR" ] || fail 'discard retained an inactive generation'

FAKE_SUDO_CALLS="$SUDO_CALLS" \
  SUB2API_CERT_RECEIVER="$RECEIVER" \
  SUB2API_SUDO_BIN="${FAKE_BIN}/sudo" \
  SSH_ORIGINAL_COMMAND="prepare ${GEN_TWO} ${GEN_TWO_CERT_SHA} ${GEN_TWO_KEY_SHA} 604800 ${DOMAIN}" \
  /bin/bash "$TRIGGER"
assert_contains "$SUDO_CALLS" "-n ${RECEIVER} prepare ${GEN_TWO} ${GEN_TWO_CERT_SHA} ${GEN_TWO_KEY_SHA} 604800 ${DOMAIN}"
sudo_count="$(wc -l <"$SUDO_CALLS" | tr -d '[:space:]')"
if FAKE_SUDO_CALLS="$SUDO_CALLS" \
  SUB2API_CERT_RECEIVER="$RECEIVER" \
  SUB2API_SUDO_BIN="${FAKE_BIN}/sudo" \
  SSH_ORIGINAL_COMMAND="status ${DOMAIN} extra" /bin/bash "$TRIGGER" >/dev/null 2>&1; then
  fail 'forced command accepted extra arguments'
fi
[ "$(wc -l <"$SUDO_CALLS" | tr -d '[:space:]')" = "$sudo_count" ] \
  || fail 'invalid forced command reached sudo'

printf 'Certificate receiver frozen coordinator protocol tests passed.\n'
