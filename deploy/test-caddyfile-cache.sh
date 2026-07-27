#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
caddyfile="$repo_root/deploy/Caddyfile"
active_config=$(sed 's/[[:space:]]*#.*$//' "$caddyfile")

if printf '%s\n' "$active_config" | grep -Eiq 'Cache-Control.*immutable'; then
	echo "Caddyfile must not force immutable caching; the backend owns asset cache policy" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*reverse_proxy[[:space:]]+localhost:8080'; then
	echo "Caddyfile must continue proxying all application routes to localhost:8080" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*@large_multimodal_request_body[[:space:]]+path[[:space:]].*/v1/images/batches([[:space:]]|$)'; then
	echo "Caddyfile must give the exact Batch Image submit path the multimodal body budget" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*@standard_request_body[[:space:]]+not[[:space:]]+path[[:space:]].*/v1/images/batches([[:space:]]|$)'; then
	echo "Caddyfile standard-body matcher must exclude the exact Batch Image submit path" >&2
	exit 1
fi

echo "Caddyfile preserves backend cache policy, routing, and scoped multimodal body limits"
