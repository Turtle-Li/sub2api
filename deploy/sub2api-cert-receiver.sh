#!/usr/bin/env bash

# Root-owned receiver for centrally issued Sub2API certificates. A dedicated
# SSH forced command is the only intended caller. Certificate activation is
# local and atomic; it never changes DNS or Cloudflare state.

set -Eeuo pipefail
umask 077

CONFIG_FILE="${SUB2API_CERT_RECEIVER_CONFIG_FILE:-/etc/sub2api-cert-receiver.env}"
if [ -r "$CONFIG_FILE" ]; then
  # The production file is installed root:root 0600 and contains paths and
  # non-secret runtime settings only. sudo env_reset prevents the deploy user
  # from overriding this path.
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
fi

CERT_ROOT="${SUB2API_CERT_ROOT:-/opt/sub2api/certs/api.turtleligpt.com}"
DOMAIN="${SUB2API_CERT_DOMAIN:-api.turtleligpt.com}"
CADDY_CONTAINER="${SUB2API_CERT_CADDY_CONTAINER:-sub2api-caddy}"
CADDYFILE_HOST_PATH="${SUB2API_CERT_CADDYFILE_HOST_PATH:-/opt/sub2api/Caddyfile}"
CADDYFILE_CONTAINER_PATH="${SUB2API_CERT_CADDYFILE_CONTAINER_PATH:-/etc/caddy/Caddyfile}"
CADDY_CERT_ROOT="${SUB2API_CERT_CADDY_CERT_ROOT:-/etc/sub2api-certs}"
TLS_VERIFY_IP="${SUB2API_CERT_TLS_VERIFY_IP:-127.0.0.1}"
TLS_VERIFY_PORT="${SUB2API_CERT_TLS_VERIFY_PORT:-443}"
HEALTH_PATH="${SUB2API_CERT_HEALTH_PATH:-/health}"
MAX_BYTES="${SUB2API_CERT_MAX_BYTES:-262144}"
MIN_VALIDITY_SECONDS="${SUB2API_CERT_MIN_VALIDITY_SECONDS:-604800}"
MAX_REQUESTED_VALIDITY_SECONDS="${SUB2API_CERT_MAX_REQUESTED_VALIDITY_SECONDS:-31536000}"
LOCK_FILE="${SUB2API_CERT_LOCK_FILE:-/run/lock/sub2api-cert-receiver.lock}"

GENERATIONS_DIR="${CERT_ROOT}/generations"
CURRENT_LINK="${CERT_ROOT}/current"
PREVIOUS_LINK="${CERT_ROOT}/previous"
TRANSACTION_FILE="${CERT_ROOT}/transaction.env"
TEMP_PAYLOAD=""
TEMP_EXTRACT=""

cleanup_temporary_files() {
  [ -z "$TEMP_PAYLOAD" ] || rm -f -- "$TEMP_PAYLOAD"
  [ -z "$TEMP_EXTRACT" ] || rm -rf -- "$TEMP_EXTRACT"
}
trap cleanup_temporary_files EXIT

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

CERT_RECEIVER_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAINTENANCE_LOCK_HELPER="${CERT_RECEIVER_SCRIPT_DIR}/sub2api-maintenance-lock.sh"
[ -r "$MAINTENANCE_LOCK_HELPER" ] && [ ! -L "$MAINTENANCE_LOCK_HELPER" ] \
  || die "maintenance lock helper is missing or unsafe: ${MAINTENANCE_LOCK_HELPER}"
# shellcheck disable=SC1090,SC1091 # Installed alongside this root-owned executable.
. "$MAINTENANCE_LOCK_HELPER"
MAINTENANCE_LOCK_FILE="${SUB2API_MAINTENANCE_LOCK_FILE:-$SUB2API_MAINTENANCE_LOCK_DEFAULT_FILE}"

validate_generation() {
  [[ "$1" =~ ^[0-9a-f]{20}$ ]] || die "invalid certificate generation"
}

validate_hash() {
  [[ "$2" =~ ^[0-9a-f]{64}$ ]] || die "$1 must be 64 lowercase hexadecimal characters"
}

validate_positive_integer() {
  case "$2" in
    ''|*[!0-9]*) die "$1 must be a positive integer" ;;
  esac
  [ "$2" -gt 0 ] || die "$1 must be a positive integer"
}

validate_absolute_path() {
  case "$2" in
    /*) ;;
    *) die "$1 must be an absolute path" ;;
  esac
  case "$2" in
    *$'\n'*|*$'\r'*|*'#'*|*'&'*|*'\'*) die "$1 contains unsupported characters" ;;
  esac
}

validate_domain_argument() {
  [ "$1" = "$DOMAIN" ] || die "domain does not match the configured certificate domain"
}

validate_config() {
  case "$DOMAIN" in
    ''|.*|*..*|*[!0-9A-Za-z.-]*) die "invalid certificate domain" ;;
  esac
  case "$CADDY_CONTAINER" in
    ''|-*|*[!0-9A-Za-z_.-]*) die "invalid Caddy container name" ;;
  esac
  case "$TLS_VERIFY_IP" in
    ''|*[!0-9A-Fa-f:.]*) die "invalid TLS verification IP" ;;
  esac
  case "$HEALTH_PATH" in
    /*) ;;
    *) die "health path must start with /" ;;
  esac
  validate_positive_integer SUB2API_CERT_TLS_VERIFY_PORT "$TLS_VERIFY_PORT"
  [ "$TLS_VERIFY_PORT" -le 65535 ] || die "TLS verification port is invalid"
  validate_positive_integer SUB2API_CERT_MAX_BYTES "$MAX_BYTES"
  validate_positive_integer SUB2API_CERT_MIN_VALIDITY_SECONDS "$MIN_VALIDITY_SECONDS"
  validate_positive_integer SUB2API_CERT_MAX_REQUESTED_VALIDITY_SECONDS "$MAX_REQUESTED_VALIDITY_SECONDS"
  [ "$MIN_VALIDITY_SECONDS" -le "$MAX_REQUESTED_VALIDITY_SECONDS" ] \
    || die "minimum validity exceeds the requested validity upper bound"
  validate_absolute_path SUB2API_CERT_ROOT "$CERT_ROOT"
  validate_absolute_path SUB2API_CERT_CADDYFILE_HOST_PATH "$CADDYFILE_HOST_PATH"
  validate_absolute_path SUB2API_CERT_CADDYFILE_CONTAINER_PATH "$CADDYFILE_CONTAINER_PATH"
  validate_absolute_path SUB2API_CERT_CADDY_CERT_ROOT "$CADDY_CERT_ROOT"
  validate_absolute_path SUB2API_CERT_LOCK_FILE "$LOCK_FILE"
  validate_absolute_path SUB2API_MAINTENANCE_LOCK_FILE "$MAINTENANCE_LOCK_FILE"
}

sha256_file() {
  openssl dgst -sha256 -r "$1" | awk '{print $1}'
}

sha256_stream() {
  openssl dgst -sha256 -r | awk '{print $1}'
}

file_size() {
  stat -c '%s' "$1" 2>/dev/null || stat -f '%z' "$1" 2>/dev/null
}

certificate_sha256() {
  openssl x509 -in "$1" -outform DER 2>/dev/null | sha256_stream
}

link_generation() {
  local link_path="$1"
  if [ ! -L "$link_path" ]; then
    printf '%s\n' ""
    return 0
  fi
  basename -- "$(readlink "$link_path")"
}

atomic_link() {
  local target="$1"
  local link_path="$2"
  local temp_link="${link_path}.tmp.$$"
  local generation
  case "$target" in
    "${GENERATIONS_DIR}/"*) generation="${target#"${GENERATIONS_DIR}/"}" ;;
    *) die "certificate link target is outside the managed generations directory" ;;
  esac
  validate_generation "$generation"
  # The certificate root is bind-mounted at a different absolute path inside
  # Caddy. Keep generation links relative so the same link resolves on both
  # the host and in the container.
  ln -s -- "generations/${generation}" "$temp_link"
  mv -Tf -- "$temp_link" "$link_path"
}

metadata_value() {
  local file="$1"
  local key="$2"
  awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$file"
}

validate_certificate_files() {
  local directory="$1"
  local requested_minimum="$2"
  local fullchain="${directory}/fullchain.pem"
  local private_key="${directory}/privkey.pem"
  local san_output san_entries cert_public_key key_public_key cert_fingerprint not_after

  [ -f "$fullchain" ] && [ ! -L "$fullchain" ] || die "fullchain.pem is missing or unsafe"
  [ -f "$private_key" ] && [ ! -L "$private_key" ] || die "privkey.pem is missing or unsafe"
  openssl x509 -in "$fullchain" -noout >/dev/null 2>&1 || die "certificate is invalid"
  openssl pkey -in "$private_key" -noout >/dev/null 2>&1 || die "private key is invalid"
  [ "$requested_minimum" -ge "$MIN_VALIDITY_SECONDS" ] \
    || requested_minimum="$MIN_VALIDITY_SECONDS"
  openssl x509 -in "$fullchain" -checkend "$requested_minimum" -noout >/dev/null 2>&1 \
    || die "certificate validity is below the required minimum"

  san_output="$(openssl x509 -in "$fullchain" -noout -ext subjectAltName 2>/dev/null \
    | tr ',' '\n' | sed 's/[[:space:]]//g')"
  san_entries="$(printf '%s\n' "$san_output" | grep -E '^(DNS:|IPAddress:)' || true)"
  [ "$san_entries" = "DNS:${DOMAIN}" ] \
    || die "certificate SAN must contain exactly the configured domain"

  openssl x509 -in "$fullchain" -noout -text 2>/dev/null \
    | grep -F 'Public Key Algorithm: id-ecPublicKey' >/dev/null \
    || die "certificate must use an EC public key"
  openssl x509 -in "$fullchain" -noout -text 2>/dev/null \
    | grep -F 'ASN1 OID: prime256v1' >/dev/null \
    || die "certificate must use ECDSA P-256"

  cert_public_key="$(openssl x509 -in "$fullchain" -pubkey -noout 2>/dev/null \
    | openssl pkey -pubin -outform DER 2>/dev/null | sha256_stream)"
  key_public_key="$(openssl pkey -in "$private_key" -pubout -outform DER 2>/dev/null \
    | sha256_stream)"
  [ -n "$cert_public_key" ] && [ "$cert_public_key" = "$key_public_key" ] \
    || die "certificate and private key do not match"

  cert_fingerprint="$(certificate_sha256 "$fullchain")"
  not_after="$(openssl x509 -in "$fullchain" -noout -enddate 2>/dev/null | cut -d= -f2-)"
  [ -n "$cert_fingerprint" ] && [ -n "$not_after" ] || die "certificate metadata is unavailable"

  printf '%s\n' "$cert_fingerprint" "$cert_public_key" "$not_after"
}

caddy_validate_generation() {
  local generation="$1"
  local current_prefix="${CADDY_CERT_ROOT}/current/"
  local generation_prefix="${CADDY_CERT_ROOT}/generations/${generation}/"
  local rendered

  [ -r "$CADDYFILE_HOST_PATH" ] || die "Caddyfile is not readable"
  grep -F "$current_prefix" "$CADDYFILE_HOST_PATH" >/dev/null \
    || die "Caddyfile does not use the managed external certificate path"
  rendered="$(sed "s#${current_prefix}#${generation_prefix}#g" "$CADDYFILE_HOST_PATH")"
  printf '%s\n' "$rendered" \
    | docker exec -i "$CADDY_CONTAINER" caddy validate \
        --config - --adapter caddyfile >/dev/null
}

caddy_validate_current() {
  docker exec "$CADDY_CONTAINER" caddy validate \
    --config "$CADDYFILE_CONTAINER_PATH" --adapter caddyfile >/dev/null
}

caddy_reload() {
  docker exec "$CADDY_CONTAINER" caddy reload \
    --force --config "$CADDYFILE_CONTAINER_PATH" --adapter caddyfile >/dev/null
}

verify_served_generation() {
  local generation="$1"
  local metadata="${GENERATIONS_DIR}/${generation}/metadata.env"
  local expected_fingerprint served_fingerprint
  expected_fingerprint="$(metadata_value "$metadata" certificate_sha256)"
  if [ -z "$expected_fingerprint" ]; then
    printf 'Prepared certificate metadata is incomplete.\n' >&2
    return 1
  fi

  served_fingerprint="$(openssl s_client \
      -connect "${TLS_VERIFY_IP}:${TLS_VERIFY_PORT}" \
      -servername "$DOMAIN" </dev/null 2>/dev/null \
    | openssl x509 -noout -fingerprint -sha256 2>/dev/null \
    | awk -F= '{gsub(":", "", $2); print tolower($2)}')"
  if [ "$served_fingerprint" != "$expected_fingerprint" ]; then
    printf 'Served certificate fingerprint does not match the activated generation.\n' >&2
    return 1
  fi

  curl --fail --silent --show-error --connect-timeout 5 --max-time 10 \
    --resolve "${DOMAIN}:${TLS_VERIFY_PORT}:${TLS_VERIFY_IP}" \
    "https://${DOMAIN}:${TLS_VERIFY_PORT}${HEALTH_PATH}" >/dev/null \
    || {
      printf 'Post-reload HTTPS verification failed.\n' >&2
      return 1
    }
}

restore_link_and_reload() {
  local generation="$1"
  if [ -n "$generation" ]; then
    atomic_link "${GENERATIONS_DIR}/${generation}" "$CURRENT_LINK"
    caddy_validate_current && caddy_reload && verify_served_generation "$generation"
    return
  fi
  rm -f -- "$CURRENT_LINK"
}

write_transaction() {
  local generation="$1"
  local previous_generation="$2"
  local state="$3"
  local temporary="${TRANSACTION_FILE}.tmp.$$"
  {
    printf 'generation=%s\n' "$generation"
    printf 'previous_generation=%s\n' "$previous_generation"
    printf 'state=%s\n' "$state"
  } >"$temporary"
  chmod 600 "$temporary"
  mv -f -- "$temporary" "$TRANSACTION_FILE"
}

transaction_value() {
  local key="$1"
  [ -f "$TRANSACTION_FILE" ] || return 1
  metadata_value "$TRANSACTION_FILE" "$key"
}

prune_committed_generations() {
  local current_generation="$1"
  local rollback_generation="$2"
  local candidate candidate_generation

  for candidate in "${GENERATIONS_DIR}"/*; do
    [ -e "$candidate" ] || continue
    [ -d "$candidate" ] && [ ! -L "$candidate" ] || continue
    candidate_generation="$(basename -- "$candidate")"
    [[ "$candidate_generation" =~ ^[0-9a-f]{20}$ ]] || continue
    [ -f "${candidate}/metadata.env" ] || continue
    if [ "$candidate_generation" != "$current_generation" ] \
      && [ "$candidate_generation" != "$rollback_generation" ]; then
      rm -rf -- "$candidate"
    fi
  done
}

prepare_generation() {
  local generation="$1"
  local expected_cert_sha="$2"
  local expected_key_sha="$3"
  local requested_minimum="$4"
  local payload_size payload_file extract_dir target_dir
  local cert_metadata cert_fingerprint public_key_fingerprint not_after
  local listing entry_types extracted_file extracted_size

  validate_generation "$generation"
  validate_hash certificate_sha256 "$expected_cert_sha"
  validate_hash public_key_sha256 "$expected_key_sha"
  [ "$generation" = "${expected_cert_sha:0:20}" ] \
    || die "certificate generation must equal the certificate hash prefix"
  validate_positive_integer minimum_remaining_seconds "$requested_minimum"
  [ "$requested_minimum" -le "$MAX_REQUESTED_VALIDITY_SECONDS" ] \
    || die "requested minimum certificate lifetime exceeds the configured upper bound"

  target_dir="${GENERATIONS_DIR}/${generation}"

  payload_file="$(mktemp "${CERT_ROOT}/.payload.XXXXXX")"
  extract_dir="$(mktemp -d "${CERT_ROOT}/.prepare.XXXXXX")"
  TEMP_PAYLOAD="$payload_file"
  TEMP_EXTRACT="$extract_dir"
  head -c "$((MAX_BYTES + 1))" >"$payload_file"
  payload_size="$(wc -c <"$payload_file" | tr -d '[:space:]')"
  [ "$payload_size" -gt 0 ] || die "certificate payload is empty"
  [ "$payload_size" -le "$MAX_BYTES" ] || die "certificate payload exceeds the configured limit"

  listing="$(tar -tf "$payload_file")" || die "certificate payload is not a readable tar archive"
  [ "$(printf '%s\n' "$listing" | grep -Ec '^(fullchain|privkey)\.pem$')" -eq 2 ] \
    || die "certificate archive must contain only fullchain.pem and privkey.pem"
  [ "$(printf '%s\n' "$listing" | wc -l | tr -d '[:space:]')" -eq 2 ] \
    || die "certificate archive contains unexpected or duplicate entries"
  entry_types="$(tar -tvf "$payload_file" | awk '{print substr($1, 1, 1)}')" \
    || die "certificate archive metadata is unreadable"
  [ "$(printf '%s\n' "$entry_types" | grep -c '^-$')" -eq 2 ] \
    || die "certificate archive entries must be regular files"
  tar -xf "$payload_file" -C "$extract_dir" --no-same-owner --no-same-permissions \
    fullchain.pem privkey.pem
  for extracted_file in fullchain.pem privkey.pem; do
    extracted_size="$(file_size "${extract_dir}/${extracted_file}")" \
      || die "extracted certificate file size is unavailable"
    validate_positive_integer "${extracted_file} size" "$extracted_size"
    [ "$extracted_size" -le "$MAX_BYTES" ] \
      || die "extracted certificate file exceeds the configured limit"
  done

  cert_metadata="$(validate_certificate_files "$extract_dir" "$requested_minimum")"
  cert_fingerprint="$(printf '%s\n' "$cert_metadata" | sed -n '1p')"
  public_key_fingerprint="$(printf '%s\n' "$cert_metadata" | sed -n '2p')"
  not_after="$(printf '%s\n' "$cert_metadata" | sed -n '3p')"
  [ "$cert_fingerprint" = "$expected_cert_sha" ] || die "certificate hash does not match prepare request"
  [ "$public_key_fingerprint" = "$expected_key_sha" ] || die "public key hash does not match prepare request"

  if [ -f "${target_dir}/metadata.env" ]; then
    [ "$(metadata_value "${target_dir}/metadata.env" certificate_sha256)" = "$expected_cert_sha" ] \
      || die "generation already exists with a different certificate"
    [ "$(metadata_value "${target_dir}/metadata.env" public_key_sha256)" = "$expected_key_sha" ] \
      || die "generation already exists with a different public key"
    caddy_validate_generation "$generation" || die "staged Caddy validation failed"
    printf 'PREPARED %s %s\n' "$generation" "$expected_cert_sha"
    return
  fi
  [ ! -e "$target_dir" ] || die "generation path already exists without valid metadata"

  install -o root -g root -m 600 "${extract_dir}/fullchain.pem" "${extract_dir}/fullchain.pem.secure" \
    2>/dev/null || install -m 600 "${extract_dir}/fullchain.pem" "${extract_dir}/fullchain.pem.secure"
  install -o root -g root -m 600 "${extract_dir}/privkey.pem" "${extract_dir}/privkey.pem.secure" \
    2>/dev/null || install -m 600 "${extract_dir}/privkey.pem" "${extract_dir}/privkey.pem.secure"
  mv -f -- "${extract_dir}/fullchain.pem.secure" "${extract_dir}/fullchain.pem"
  mv -f -- "${extract_dir}/privkey.pem.secure" "${extract_dir}/privkey.pem"
  {
    printf 'generation=%s\n' "$generation"
    printf 'domain=%s\n' "$DOMAIN"
    printf 'certificate_sha256=%s\n' "$cert_fingerprint"
    printf 'public_key_sha256=%s\n' "$public_key_fingerprint"
    printf 'not_after=%s\n' "$not_after"
  } >"${extract_dir}/metadata.env"
  chmod 600 "${extract_dir}/metadata.env"
  chmod 700 "$extract_dir"
  mv -- "$extract_dir" "$target_dir"
  extract_dir=""
  TEMP_EXTRACT=""
  caddy_validate_generation "$generation" || {
    rm -rf -- "$target_dir"
    die "staged Caddy validation failed"
  }
  printf 'PREPARED %s %s\n' "$generation" "$cert_fingerprint"
  rm -f -- "$payload_file"
  TEMP_PAYLOAD=""
}

activate_generation() {
  local generation="$1"
  local old_generation transaction_generation transaction_state
  validate_generation "$generation"
  [ -f "${GENERATIONS_DIR}/${generation}/metadata.env" ] || die "generation is not prepared"
  old_generation="$(link_generation "$CURRENT_LINK")"
  if [ "$old_generation" = "$generation" ]; then
    transaction_generation="$(transaction_value generation || true)"
    transaction_state="$(transaction_value state || true)"
    [ "$transaction_generation" = "$generation" ] || die "current generation has no matching activation transaction"
    case "$transaction_state" in
      active|committed)
        caddy_validate_current
        verify_served_generation "$generation"
        ;;
      activating)
        old_generation="$(transaction_value previous_generation || true)"
        [ -n "$old_generation" ] || die "interrupted activation has no rollback generation"
        if ! { caddy_validate_current && caddy_reload && verify_served_generation "$generation"; }; then
          restore_link_and_reload "$old_generation" || die "interrupted activation rollback was incomplete"
          rm -f -- "$PREVIOUS_LINK"
          write_transaction "$generation" "$old_generation" rolled_back
          die "interrupted activation failed; previous certificate generation was restored"
        fi
        write_transaction "$generation" "$old_generation" active
        ;;
      *) die "current generation has no valid activation transaction" ;;
    esac
    printf 'ACTIVATED %s\n' "$generation"
    return
  fi

  # The external-certificate Caddy contract must be bootstrapped with a known
  # current generation before remote activation. This makes every activation,
  # including the first coordinated one, locally reversible.
  [ -n "$old_generation" ] || die "no bootstrap certificate generation is active"
  [ -f "${GENERATIONS_DIR}/${old_generation}/metadata.env" ] \
    || die "active bootstrap certificate metadata is unavailable"

  caddy_validate_generation "$generation"
  atomic_link "${GENERATIONS_DIR}/${old_generation}" "$PREVIOUS_LINK"
  write_transaction "$generation" "$old_generation" activating
  atomic_link "${GENERATIONS_DIR}/${generation}" "$CURRENT_LINK"

  if ! { caddy_validate_current && caddy_reload && verify_served_generation "$generation"; }; then
    printf 'Activation failed; restoring previous certificate generation.\n' >&2
    if ! restore_link_and_reload "$old_generation"; then
      die "activation failed and automatic certificate rollback was incomplete"
    fi
    rm -f -- "$PREVIOUS_LINK"
    write_transaction "$generation" "$old_generation" rolled_back
    die "activation failed; previous certificate generation was restored"
  fi
  write_transaction "$generation" "$old_generation" active
  printf 'ACTIVATED %s\n' "$generation"
}

rollback_generation() {
  local generation="$1"
  local current_generation previous_generation transaction_generation transaction_state
  validate_generation "$generation"
  current_generation="$(link_generation "$CURRENT_LINK")"
  transaction_generation="$(transaction_value generation || true)"
  transaction_state="$(transaction_value state || true)"
  previous_generation="$(transaction_value previous_generation || true)"
  if [ "$transaction_generation" != "$generation" ]; then
    # The coordinator deliberately issues rollback after every uncertain or
    # failed activation. If activation failed before the transaction was
    # created, the named generation is still inactive and rollback is a safe
    # idempotent no-op.
    [ "$current_generation" != "$generation" ] \
      || die "current generation has no matching rollback transaction"
    printf 'ROLLED_BACK %s\n' "$generation"
    return
  fi

  if [ "$transaction_state" = rolled_back ]; then
    [ -n "$previous_generation" ] || die "rolled-back transaction has no previous generation"
    [ "$current_generation" = "$previous_generation" ] \
      || die "rolled-back transaction does not match the current generation"
    printf 'ROLLED_BACK %s\n' "$generation"
    return
  fi

  [ -n "$previous_generation" ] || die "no previous certificate generation is available"
  [ -f "${GENERATIONS_DIR}/${previous_generation}/metadata.env" ] \
    || die "previous certificate generation is unavailable"
  case "$transaction_state" in
    activating)
      if [ "$current_generation" = "$previous_generation" ]; then
        restore_link_and_reload "$previous_generation" \
          || die "interrupted activation rollback verification failed"
        rm -f -- "$PREVIOUS_LINK"
        write_transaction "$generation" "$previous_generation" rolled_back
        printf 'ROLLED_BACK %s\n' "$generation"
        return
      fi
      [ "$current_generation" = "$generation" ] \
        || die "interrupted activation does not match the current generation"
      ;;
    active|committed)
      [ "$current_generation" = "$generation" ] \
        || die "current generation does not match the rollback request"
      ;;
    *) die "activation transaction is not rollbackable" ;;
  esac

  atomic_link "${GENERATIONS_DIR}/${previous_generation}" "$CURRENT_LINK"
  if ! { caddy_validate_current && caddy_reload && verify_served_generation "$previous_generation"; }; then
    atomic_link "${GENERATIONS_DIR}/${current_generation}" "$CURRENT_LINK"
    if ! { caddy_validate_current && caddy_reload && verify_served_generation "$current_generation"; } >/dev/null 2>&1; then
      die "rollback failed and the original current generation could not be restored"
    fi
    die "rollback failed; the original current generation was restored and verified"
  fi
  rm -f -- "$PREVIOUS_LINK"
  write_transaction "$generation" "$previous_generation" rolled_back
  printf 'ROLLED_BACK %s\n' "$generation"
}

status_generation() {
  local current_generation metadata cert_sha
  current_generation="$(link_generation "$CURRENT_LINK")"
  [ -n "$current_generation" ] || die "no certificate generation is active"
  metadata="${GENERATIONS_DIR}/${current_generation}/metadata.env"
  [ -f "$metadata" ] || die "current generation metadata is unavailable"
  cert_sha="$(metadata_value "$metadata" certificate_sha256)"
  validate_hash certificate_sha256 "$cert_sha"
  printf 'CURRENT %s %s\n' "$current_generation" "$cert_sha"
}

commit_generation() {
  local generation="$1"
  local current_generation transaction_generation transaction_state previous_generation
  validate_generation "$generation"
  current_generation="$(link_generation "$CURRENT_LINK")"
  transaction_generation="$(transaction_value generation || true)"
  transaction_state="$(transaction_value state || true)"
  previous_generation="$(transaction_value previous_generation || true)"
  [ "$transaction_generation" = "$generation" ] || die "commit transaction is unavailable"
  [ "$current_generation" = "$generation" ] || die "only the current generation can be committed"
  case "$transaction_state" in
    active) write_transaction "$generation" "$previous_generation" committed ;;
    committed) ;;
    *) die "activation transaction is not committable" ;;
  esac
  prune_committed_generations "$generation" "$previous_generation"
  printf 'COMMITTED %s\n' "$generation"
}

discard_generation() {
  local generation="$1"
  local current_generation previous_generation transaction_generation transaction_state
  local target_dir
  validate_generation "$generation"
  current_generation="$(link_generation "$CURRENT_LINK")"
  previous_generation="$(link_generation "$PREVIOUS_LINK")"
  transaction_generation="$(transaction_value generation || true)"
  transaction_state="$(transaction_value state || true)"
  [ "$current_generation" != "$generation" ] || die "current generation cannot be discarded"
  [ "$previous_generation" != "$generation" ] || die "bounded rollback generation cannot be discarded"
  if [ "$transaction_generation" = "$generation" ] && [ "$transaction_state" != rolled_back ]; then
    die "active certificate transaction cannot be discarded"
  fi
  target_dir="${GENERATIONS_DIR}/${generation}"
  if [ -e "$target_dir" ]; then
    [ -d "$target_dir" ] && [ ! -L "$target_dir" ] || die "generation path is unsafe"
    rm -rf -- "$target_dir"
  fi
  printf 'DISCARDED %s\n' "$generation"
}

main() {
  local action="${1:-}" test_tmp_root lock_parent
  validate_config
  if [ "${SUB2API_CERT_RECEIVER_ALLOW_NON_ROOT_FOR_TESTS:-0}" = 1 ]; then
    test_tmp_root="${TMPDIR:-/tmp}"
    test_tmp_root="${test_tmp_root%/}"
    [ -n "$test_tmp_root" ] && [ "$test_tmp_root" != / ] \
      || die "test mode requires a bounded temporary directory"
    [ -d "$test_tmp_root" ] \
      || die "test mode temporary directory does not exist: ${test_tmp_root}"
    test_tmp_root="$(cd "$test_tmp_root" && pwd -P)"
    [ "$CONFIG_FILE" != /etc/sub2api-cert-receiver.env ] \
      || die "test mode requires an explicit non-production config file"
    case "$CONFIG_FILE" in "$test_tmp_root"/*|/tmp/*) ;; *) die "test config must be inside the temporary directory" ;; esac
    case "$CERT_ROOT" in "$test_tmp_root"/*|/tmp/*) ;; *) die "test certificate root must be inside the temporary directory" ;; esac
    # shellcheck disable=SC2034 # Read by the sourced maintenance-lock helper.
    SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS=1
  else
    [ "$(id -u)" -eq 0 ] || die "certificate receiver must run as root"
  fi
  if ! sub2api_maintenance_lock_validate_configured_path "$MAINTENANCE_LOCK_FILE"; then
    die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
  fi
  for command_name in awk basename curl docker find flock grep head id install ln mkdir mv openssl readlink sed stat tar tr wc; do
    require_cmd "$command_name"
  done
  install -d -m 700 "$CERT_ROOT" "$GENERATIONS_DIR"
  lock_parent="$(dirname "$LOCK_FILE")"
  [ -d "$lock_parent" ] || die "certificate lock directory does not exist: $lock_parent"
  if ! sub2api_maintenance_lock_open "$MAINTENANCE_LOCK_FILE"; then
    die "unsafe maintenance lock: ${SUB2API_MAINTENANCE_LOCK_ERROR}"
  fi
  flock -w 120 -x 8 || die "timed out waiting for the maintenance lock"
  exec 9>"$LOCK_FILE"
  flock -w 30 -x 9 || die "timed out waiting for the certificate lock"
  find "$CERT_ROOT" -maxdepth 1 -type f -name '.payload.*' -mmin +60 -delete
  find "$CERT_ROOT" -maxdepth 1 -type d -name '.prepare.*' -mmin +60 \
    -exec rm -rf -- {} +
  find "$CERT_ROOT" -maxdepth 1 \( -type f -o -type l \) -name '*.tmp.*' -mmin +60 -delete

  case "$action" in
    prepare)
      [ "$#" -eq 6 ] || die "prepare requires GENERATION CERT_SHA KEY_SHA MIN_SECONDS DOMAIN"
      validate_domain_argument "$6"
      prepare_generation "$2" "$3" "$4" "$5"
      ;;
    activate)
      [ "$#" -eq 3 ] || die "activate requires GENERATION DOMAIN"
      validate_domain_argument "$3"
      activate_generation "$2"
      ;;
    rollback)
      [ "$#" -eq 3 ] || die "rollback requires GENERATION DOMAIN"
      validate_domain_argument "$3"
      rollback_generation "$2"
      ;;
    status)
      [ "$#" -eq 2 ] || die "status requires DOMAIN"
      validate_domain_argument "$2"
      status_generation
      ;;
    commit)
      [ "$#" -eq 3 ] || die "commit requires GENERATION DOMAIN"
      validate_domain_argument "$3"
      commit_generation "$2"
      ;;
    discard)
      [ "$#" -eq 3 ] || die "discard requires GENERATION DOMAIN"
      validate_domain_argument "$3"
      discard_generation "$2"
      ;;
    *)
      die "only prepare, activate, status, rollback, commit, and discard are supported"
      ;;
  esac
}

main "$@"
