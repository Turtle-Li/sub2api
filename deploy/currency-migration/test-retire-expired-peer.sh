#!/usr/bin/env bash
# Local mock coverage for the UFW invocation used by retire-expired-peer.sh.
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
target_script="$script_dir/retire-expired-peer.sh"
fixture_dir=$(mktemp -d "${TMPDIR:-/tmp}/sub2api-retire-peer-ufw.XXXXXX")

cleanup() {
  rm -rf "$fixture_dir"
}
trap cleanup EXIT

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

mkdir -p "$fixture_dir/bin"
capture="$fixture_dir/ufw.argv"
cat >"$fixture_dir/bin/ufw" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
: "${UFW_CAPTURE:?}"
printf '%s\n' "$@" >"$UFW_CAPTURE"
MOCK
chmod 700 "$fixture_dir/bin/ufw"

function_file="$fixture_dir/apply-ufw.sh"
awk '
  /^apply_ufw\(\) \{/ { capturing = 1; found = 1 }
  capturing { print }
  capturing && /^}/ { exit }
  END { exit(found ? 0 : 1) }
' "$target_script" >"$function_file" || fail 'could not extract apply_ufw from the target script'

die() {
  fail "$*"
}
# shellcheck disable=SC1090
. "$function_file"

# shellcheck disable=SC2034 # read by the extracted apply_ufw function
backup_dir="$fixture_dir"
PATH="$fixture_dir/bin:$PATH"
export PATH
export UFW_CAPTURE="$capture"

assert_exact_invocation() {
  local label=$1
  shift
  local expected=("$@")
  local actual=()
  local argument index

  : >"$capture"
  apply_ufw "${expected[@]}"
  while IFS= read -r argument; do
    actual+=("$argument")
  done <"$capture"
  [[ ${#actual[@]} -eq ${#expected[@]} ]] || fail "$label: unexpected UFW argument count"
  for ((index = 0; index < ${#expected[@]}; index++)); do
    [[ ${actual[index]} == "${expected[index]}" ]] || fail "$label: unexpected UFW argument at index ${index}"
    [[ ${actual[index]} != --force ]] || fail "$label: UFW must not receive --force"
  done
}

assert_exact_invocation allow in on tailscale0 from 100.64.0.20/32 to any port 5432 proto tcp
assert_exact_invocation delete allow in on eth0 from 198.51.100.10/32 to any port 6379 proto tcp

echo 'PASS: mocked UFW allow and complete delete receive no --force flag'
