#!/usr/bin/env bash
# Install the owner's existing ImageEnhan credential into a fixed host-only
# encrypted store. The application and Docker environment never see this file.
set -Eeuo pipefail
set +x
umask 077

die() { printf '%s\n' 'Aliyun host credential installation failed' >&2; exit 1; }

[[ $# = 0 && $(id -u) = 0 ]] || die
directory=/etc/sub2api-credentials
credential=sub2api-aliyun-image-upscale
target="$directory/$credential.cred"
if [[ ! -e "$directory" ]]; then install -d -m 0700 -o root -g root "$directory" || die; fi
[[ -d "$directory" && ! -L "$directory" && $(stat -c '%u:%g:%a' "$directory") = 0:0:700 ]] || die
[[ ! -e "$target" && ! -L "$target" ]] || die
temporary=$(mktemp "$directory/.${credential}.XXXXXXXX") || die
trap 'rm -f -- "$temporary"' EXIT

# Validate bounded stdin and canonicalize it without exposing any values in
# diagnostics. The only filesystem write is systemd-creds ciphertext.
validate='import json,sys
def unique(pairs):
    value=dict(pairs)
    if len(value)!=len(pairs): raise ValueError("duplicate key")
    return value
try:
    raw=sys.stdin.buffer.read(514)
    if not raw or len(raw)>513: sys.exit(1)
    value=json.loads(raw,object_pairs_hook=unique)
    if not isinstance(value,dict) or set(value)!={"access_key_id","access_key_secret"}: sys.exit(1)
    if not all(isinstance(s,str) and s and s.isascii() and s.isprintable() and not any(c.isspace() for c in s) for s in value.values()): sys.exit(1)
    out=json.dumps(value,separators=(",",":"))
    if len(out)>512: sys.exit(1)
    sys.stdout.write(out+"\n")
except (ValueError,UnicodeError): sys.exit(1)'
python3 -c "$validate" 2>/dev/null \
  | systemd-creds encrypt --with-key=host --name="$credential" - - >"$temporary" 2>/dev/null || die
systemd-creds decrypt --name="$credential" "$temporary" - 2>/dev/null \
  | python3 -c "$validate" >/dev/null 2>&1 || die
# No replace semantics: an already-installed credential is never overwritten.
sync -d -- "$temporary" || die
ln -T -- "$temporary" "$target" || die
sync -f -- "$directory" || die
printf '%s\n' 'SUB2API_ALIYUN_HOST_CREDENTIAL_SEALED'
