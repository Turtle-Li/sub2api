# Azure Caddy runtime evidence

Captured read-only from `srv-azure-sub2api-relay-jp` after the reviewed
listener policy was re-staged. This is evidence for the 2026-09-02 review
snapshot, not a reusable Caddyfile template.

| Property | Observed value |
| --- | --- |
| Caddy version | `v2.11.4` |
| Host path / container path | `/opt/sub2api/Caddyfile` / `/etc/caddy/Caddyfile` |
| Host and container device:inode | `66305:265615` / `66305:265615` |
| Host and container SHA-256 | `ee59c226f5679464828a07eea013bc8f58054258af1da40bbf8f2cd89d8d4715` |
| Adapted startup contract fingerprint | `8a9e08798e183dc4566fb7a72bd357ca4ad54a13d84d7fb5d20fd3336d75dd4a` |
| Live admin-API contract fingerprint | `8a9e08798e183dc4566fb7a72bd357ca4ad54a13d84d7fb5d20fd3336d75dd4a` |

The versioned JSON verifier requires exactly one `:443` server with h1/h2,
the exact GCP `/32` PROXY wrapper before TLS, `fallback_policy skip`, exactly
one terminal production API route, and one supported Sub2API generation. Any
earlier route must be provably host-exclusive of the production API hostname,
so an earlier catch-all cannot bypass the protected route. Exactly two
production reverse proxies are required. Each must replace
`X-Forwarded-For` with the connection peer restored by PROXY v2 and delete
`X-Real-IP` plus `CF-Connecting-IP`; standalone header handlers are rejected.
Caddy v2.11.4 already protects its default XFF handling, while this explicit
policy also removes the two legacy headers that the application prioritizes.
The reviewed semantics are documented in Caddy's
[reverse_proxy header defaults](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#defaults)
and [global server options](https://caddyserver.com/docs/caddyfile/options#servers).

The complete production site block observed in the host startup file was:

```caddyfile
api.turtleligpt.com {
	tls /etc/sub2api-certs/current/fullchain.pem /etc/sub2api-certs/current/privkey.pem
	encode zstd gzip

	@responses_request_body path /v1/responses /v1/responses/* /responses /responses/* /backend-api/codex/responses /backend-api/codex/responses/* /v1/images/batches
	@too_large_responses_body {
		path /v1/responses /v1/responses/* /responses /responses/* /backend-api/codex/responses /backend-api/codex/responses/* /v1/images/batches
		expression `{http.request.header.Content-Length} != '' && int({http.request.header.Content-Length}) > 100000000`
	}
	handle @too_large_responses_body {
		respond "请求体过大：本次请求超过 100MB，服务器已提前拦截。" 413
	}
	handle @responses_request_body {
		request_body {
			max_size 100MB
		}
		reverse_proxy sub2api-green:8080 {
			header_up X-Forwarded-For {remote_host}
			header_up -X-Real-IP
			header_up -CF-Connecting-IP
			flush_interval -1
			transport http {
				dial_timeout 30s
				response_header_timeout 1800s
				read_timeout 1800s
				write_timeout 1800s
			}
		}
	}

	@too_large_default_body expression `{http.request.header.Content-Length} != '' && int({http.request.header.Content-Length}) > 100000000`
	handle @too_large_default_body {
		respond "请求体过大：本次请求超过 100MB，服务器已提前拦截。" 413
	}
	handle {
		request_body {
			max_size 100MB
		}
		reverse_proxy sub2api-green:8080 {
			header_up X-Forwarded-For {remote_host}
			header_up -X-Real-IP
			header_up -CF-Connecting-IP
			flush_interval -1
			transport http {
				dial_timeout 30s
				response_header_timeout 1800s
				read_timeout 1800s
				write_timeout 1800s
			}
		}
	}
}
```

There is no HTTP-header `trusted_proxies` directive in this complete site
block/global server contract. The explicit request-header policy is present in
both production reverse proxies and is part of the adapted/live fingerprint.
A later blue-green release is expected to change only the supported upstream
generation; that change invalidates this historical file hash and requires the
effective JSON gate to run again.
