#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
caddyfile="$repo_root/deploy/Caddyfile"
external_caddyfile="$repo_root/deploy/Caddyfile.external-cert.example"
route_verifier="$repo_root/deploy/verify_image_route_contract.py"
if [ ! -x "$route_verifier" ]; then
	echo "image route contract verifier must be an executable source artifact" >&2
	exit 1
fi
active_config=$(sed 's/[[:space:]]*#.*$//' "$caddyfile")
external_config=$(sed 's/[[:space:]]*#.*$//' "$external_caddyfile")
normalized_config=$(printf '%s\n' "$active_config" | awk '
	NF > 0 {
		for (field = 1; field <= NF; field++) {
			token = $field
			sub(/^["`]/, "", token)
			sub(/["`]$/, "", token)
			printf "%s%s", (field == 1 ? "" : " "), token
		}
		print ""
	}
')

# Keep one canonical encode block instead of reimplementing Caddy matcher semantics.
expected_encode_block=$(cat <<'EOF'
encode {
zstd
gzip 6
minimum_length 256
match {
header Content-Type text/css*
header Content-Type text/csv*
header Content-Type text/html*
header Content-Type text/javascript*
header Content-Type text/markdown*
header Content-Type text/plain*
header Content-Type text/xml*
header Content-Type application/json*
header Content-Type application/javascript*
header Content-Type application/xml*
header Content-Type application/rss+xml*
header Content-Type image/svg+xml*
}
}
EOF
)

if printf '%s\n' "$normalized_config" | grep -Eiq 'cache-control.*immutable'; then
	echo "Caddyfile must not force immutable caching; the backend owns asset cache policy" >&2
	exit 1
fi

if ! printf '%s\n' "$normalized_config" | grep -Eq '^reverse_proxy localhost:8080([[:space:]]|$)'; then
	echo "Caddyfile must continue proxying all application routes to localhost:8080" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*@large_multimodal_request_body[[:space:]]+path[[:space:]].*/v1/images/batches([[:space:]]|$)'; then
	echo "Caddyfile must give the exact Batch Image submit path the multimodal body budget" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*@large_multimodal_request_body[[:space:]]+path[[:space:]].*/backend-api/codex/responses([[:space:]]|$)'; then
	echo "Caddyfile must give the Codex Responses alias the multimodal body budget" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*@standard_request_body[[:space:]]+not[[:space:]]+path[[:space:]].*/v1/images/batches([[:space:]]|$)'; then
	echo "Caddyfile standard-body matcher must exclude the exact Batch Image submit path" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*@standard_request_body[[:space:]]+not[[:space:]]+path[[:space:]].*/backend-api/codex/responses([[:space:]]|$)'; then
	echo "Caddyfile standard-body matcher must exclude the Codex Responses alias" >&2
	exit 1
fi

if printf '%s\n' "$normalized_config" | grep -Eq '^import([[:space:]]|$)'; then
	echo "Caddyfile must not import configuration outside this canonical policy check" >&2
	exit 1
fi

if printf '%s\n' "$normalized_config" | grep -Eiq '^flush_interval([[:space:]]|$)'; then
	echo "Caddyfile must leave flush_interval unset so SSE auto-flushing and client cancellation remain intact" >&2
	exit 1
fi

encode_directive_count=$(printf '%s\n' "$normalized_config" | awk '$1 == "encode" { count++ } END { print count + 0 }')
if [ "$encode_directive_count" -ne 1 ]; then
	echo "Caddyfile must contain exactly one explicit encode block" >&2
	exit 1
fi

actual_encode_block=$(printf '%s\n' "$normalized_config" | awk '
	$1 == "encode" { in_block = 1 }
	in_block {
		print
		for (field = 1; field <= NF; field++) {
			if ($field == "{") depth++
			if ($field == "}") depth--
		}
		if (depth == 0) exit
	}
')
if [ "$actual_encode_block" != "$expected_encode_block" ]; then
	echo "Caddyfile encode block must keep the canonical non-SSE compression policy" >&2
	exit 1
fi

# The external-certificate template models the restricted production site. Keep
# every application image alias in that explicit allowlist so a terminal edge
# 404 cannot hide a route that the backend already registers.
external_openai_line=$(printf '%s\n' "$external_config" | grep -E '^[[:space:]]*@openai_api[[:space:]]+path[[:space:]]' || true)
[ -n "$external_openai_line" ] || {
	echo "external-cert Caddy template must define an explicit OpenAI route allowlist" >&2
	exit 1
}
external_has_path() {
	path=$1
	printf '%s\n' "$external_openai_line" | awk -v wanted="$path" '
		{
			for (field = 1; field <= NF; field++) {
				if ($field == wanted) found = 1
			}
		}
		END { exit(found ? 0 : 1) }
	'
}
for required_path in \
	'/v1/*' '/v1beta/*' '/responses' '/responses/*' '/alpha/search' \
	'/models' '/messages/count_tokens' '/backend-api/codex/*' \
	'/chat/completions' '/embeddings' \
	'/images/generations' '/images/generations/*' \
	'/images/edits' '/images/edits/*' '/images/tasks/*' \
	'/videos' '/videos/*' '/tts' '/stt' \
	'/custom-voices' '/custom-voices/*' '/realtime' \
	'/web_search' '/x_search' '/antigravity/*' \
	'/setup/status' '/api/event_logging/batch'; do
	if ! external_has_path "$required_path"; then
		echo "external-cert Caddy template is missing supported gateway path: $required_path" >&2
		exit 1
	fi
done

if ! printf '%s\n' "$external_config" | grep -Eq '^[[:space:]]*respond[[:space:]]+404[[:space:]]*$'; then
	echo "external-cert Caddy template must keep a terminal unsupported-route response" >&2
	exit 1
fi

if ! printf '%s\n' "$external_config" | grep -Eq '^[[:space:]]*@large_multimodal_request_body[[:space:]]+path[[:space:]].*/v1/images/batches([[:space:]]|$)'; then
	echo "external-cert Caddy template must give the versioned Batch Image path the multimodal body budget" >&2
	exit 1
fi

if ! printf '%s\n' "$external_config" | grep -Eq '^[[:space:]]*@standard_request_body[[:space:]]+not[[:space:]]+path[[:space:]].*/v1/images/batches([[:space:]]|$)'; then
	echo "external-cert Caddy template standard-body matcher must exclude the versioned Batch Image path" >&2
	exit 1
fi

echo "Caddyfile preserves backend cache policy, image aliases, routing, SSE streaming, and scoped multimodal body limits"
