# Azure Caddy runtime evidence

Captured read-only from `srv-azure-sub2api-relay-jp` after the reviewed
listener policy was re-staged. This is evidence for the 2026-09-02 review
snapshot, not a reusable Caddyfile template.

| Property | Observed value |
| --- | --- |
| Caddy version | `v2.11.4` |
| Host path / container path | `/opt/sub2api/Caddyfile` / `/etc/caddy/Caddyfile` |
| Host and container device:inode | `66305:265615` / `66305:265615` |
| Host and container SHA-256 | `9dc842ad2ee0ac18d89bb3f680c761170ff5b5b38b43ab6437b3c3637c766356` |
| Adapted startup contract fingerprint | `9b992e3e3f0a4b12a88c1120fa0818a92301629c0427f82b7e1ac0a1813c33ee` |
| Live admin-API contract fingerprint | `9b992e3e3f0a4b12a88c1120fa0818a92301629c0427f82b7e1ac0a1813c33ee` |

The versioned JSON verifier requires exactly one `:443` server with h1/h2,
the exact GCP `/32` PROXY wrapper before TLS, `fallback_policy skip`, exactly
one production API route, and one supported Sub2API generation. It also
requires HTTP-header proxy trust to remain absent and rejects any explicit
`X-Forwarded-For` mutation. With Caddy v2.11.4, that keeps Caddy's safe default:
incoming X-Forwarded values are ignored and the upstream X-Forwarded-For is
derived from the connection peer restored by PROXY v2.
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

There is no `header_up X-Forwarded-For` and no `trusted_proxies` directive in
this complete site block/global server contract. A later blue-green release is
expected to change only the supported upstream generation; that change
invalidates this historical file hash and requires the effective JSON gate to
run again.
