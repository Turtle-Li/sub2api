# syntax=docker/dockerfile:1.7
# =============================================================================
# Sub2API Multi-Stage Dockerfile
# =============================================================================
# Stage 1: Build frontend
# Stage 2: Build Go backend with embedded frontend
# Stage 3: Final minimal image
# =============================================================================

ARG NODE_IMAGE=node:24-alpine
ARG GOLANG_IMAGE=golang:1.27.3-alpine
ARG ALPINE_IMAGE=alpine:3.21
ARG POSTGRES_IMAGE=postgres:18-alpine
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ARG NPM_CONFIG_REGISTRY=
# Production releases run on a small shared host. Keep Go compilation
# deliberately serial and apply a Go runtime memory target so a candidate
# build cannot starve the live instance.
ARG BUILD_GOMAXPROCS=1
ARG BUILD_GO_PARALLELISM=1
ARG BUILD_GO_MEMORY_LIMIT=768MiB

# -----------------------------------------------------------------------------
# Stage 1: Frontend Builder
# -----------------------------------------------------------------------------
# --platform=$BUILDPLATFORM: the frontend output is JS (arch-neutral), so build
# it on the native host arch instead of under QEMU emulation for the target.
FROM --platform=${BUILDPLATFORM} ${NODE_IMAGE} AS frontend-builder
ARG NPM_CONFIG_REGISTRY

WORKDIR /app/frontend

# Install pnpm (pinned to v9 to match CI and keep builds reproducible)
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate

# Install dependencies first (better caching)
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN --mount=type=cache,id=sub2api-pnpm-store,target=/root/.local/share/pnpm/store,sharing=locked \
    if [ -n "${NPM_CONFIG_REGISTRY}" ]; then pnpm config set registry "${NPM_CONFIG_REGISTRY}"; fi && \
    pnpm install --frozen-lockfile --prefer-offline

# Copy frontend source and build.
# LegalDocumentView.vue (admin-compliance gate) build-time imports
# ../../../../docs/legal/*.md?raw, so docs/legal/ must sit beside frontend/
# in the image (WORKDIR /app/frontend -> resolves to /app/docs/legal/*.md).
# Copy only that subtree to keep the build dependency minimal.
COPY frontend/ ./
COPY docs/legal/ /app/docs/legal/
RUN nice -n 10 pnpm run build

# -----------------------------------------------------------------------------
# Stage 2: Backend Builder
# -----------------------------------------------------------------------------
# --platform=$BUILDPLATFORM: run the Go toolchain on the native host arch and
# cross-compile to the target arch below. The binary is CGO_ENABLED=0, so this
# is a clean pure-Go cross-compile — no QEMU emulation of go mod download / go
# build (emulated networking here was dropping module fetches with EOF).
FROM --platform=${BUILDPLATFORM} ${GOLANG_IMAGE} AS backend-builder

ARG VERSION=
ARG COMMIT=docker
ARG DATE
ARG GOPROXY
ARG GOSUMDB
ARG BUILD_GOMAXPROCS
ARG BUILD_GO_PARALLELISM
ARG BUILD_GO_MEMORY_LIMIT
# Populated by buildx from the --platform target (e.g. linux/amd64).
ARG TARGETOS
ARG TARGETARCH

ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app/backend

# Copy go mod files first (better caching)
COPY backend/go.mod backend/go.sum ./
# Cache mount keeps the module cache across builds so a transient CDN blip on
# retry resumes instead of re-fetching every zip from scratch.
RUN --mount=type=cache,id=sub2api-go-mod,target=/go/pkg/mod,sharing=locked \
    go mod download

# Copy backend source first
COPY backend/ ./

# Copy frontend dist from previous stage (must be after backend copy to avoid being overwritten)
COPY --from=frontend-builder /app/backend/internal/web/dist ./internal/web/dist

# Build the binary (BuildType=release for CI builds, embed frontend)
# Version precedence: build arg VERSION > exact git tag > cmd/server/VERSION
RUN --mount=type=cache,id=sub2api-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=sub2api-go-build,target=/root/.cache/go-build,sharing=locked \
    VERSION_VALUE="${VERSION}" && \
    if [ -z "${VERSION_VALUE}" ]; then VERSION_VALUE="$(./scripts/resolve-version.sh)"; fi && \
    DATE_VALUE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} GOMAXPROCS="${BUILD_GOMAXPROCS}" GOMEMLIMIT="${BUILD_GO_MEMORY_LIMIT}" nice -n 10 go build \
    -p "${BUILD_GO_PARALLELISM}" \
    -tags "embed nodynamic" \
    -ldflags="-s -w -X main.Version=${VERSION_VALUE} -X main.Commit=${COMMIT} -X main.Date=${DATE_VALUE} -X main.BuildType=release" \
    -trimpath \
    -o /app/sub2api \
    ./cmd/server

# The optional unified-payment credential agent is shipped in the same
# immutable image but runs as a separate, networkless container. It retains
# the Sub2 request key only in memory and exposes it through a Unix socket.
RUN --mount=type=cache,id=sub2api-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=sub2api-go-build,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} GOMAXPROCS="${BUILD_GOMAXPROCS}" GOMEMLIMIT="${BUILD_GO_MEMORY_LIMIT}" nice -n 10 go build \
    -p "${BUILD_GO_PARALLELISM}" \
    -trimpath \
    -o /app/sub2api-vault-agent \
    ./cmd/sub2api-vault-agent && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} GOMAXPROCS="${BUILD_GOMAXPROCS}" GOMEMLIMIT="${BUILD_GO_MEMORY_LIMIT}" nice -n 10 go build \
    -p "${BUILD_GO_PARALLELISM}" -trimpath -o /app/sub2api-feishu-notify ./cmd/sub2api-feishu-notify

# -----------------------------------------------------------------------------
# Stage 3: PostgreSQL Client (version-matched with docker-compose)
# -----------------------------------------------------------------------------
FROM ${POSTGRES_IMAGE} AS pg-client

# Install the native bridge once at build time using its committed lockfile.
FROM ghcr.io/astral-sh/uv:0.9.3 AS image-bridge-uv
FROM ${ALPINE_IMAGE} AS image-bridge-builder
RUN apk add --no-cache python3
COPY --from=image-bridge-uv /uv /usr/local/bin/uv
WORKDIR /opt/sub2-image-bridge
COPY image-provider-bridge/pyproject.toml image-provider-bridge/uv.lock image-provider-bridge/README.md image-provider-bridge/SOURCE_MANIFEST.json ./
COPY image-provider-bridge/sub2_image_bridge ./sub2_image_bridge
RUN UV_PYTHON_DOWNLOADS=never uv sync --locked --no-dev --no-editable --python /usr/bin/python3 \
    && rm -rf /root/.cache/uv

# -----------------------------------------------------------------------------
# Stage 4: Final Runtime Image
# -----------------------------------------------------------------------------
FROM ${ALPINE_IMAGE} AS image-bridge-runtime

# Labels
LABEL maintainer="Wei-Shaw <github.com/Wei-Shaw>"
LABEL description="Sub2API - AI API Gateway Platform"
LABEL org.opencontainers.image.source="https://github.com/Wei-Shaw/sub2api"

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    su-exec \
    libpq \
    zstd-libs \
    lz4-libs \
    krb5-libs \
    libldap \
    libedit \
    python3 \
    && rm -rf /var/cache/apk/*

# Copy pg_dump and psql from the same postgres image used in docker-compose
# This ensures version consistency between backup tools and the database server
COPY --from=pg-client /usr/local/bin/pg_dump /usr/local/bin/pg_dump
COPY --from=pg-client /usr/local/bin/psql /usr/local/bin/psql
COPY --from=pg-client /usr/local/lib/libpq.so.5* /usr/local/lib/

# Create non-root user
RUN addgroup -g 1000 sub2api && \
    adduser -u 1000 -G sub2api -s /bin/sh -D sub2api

# Set working directory
WORKDIR /app

COPY --from=image-bridge-builder /opt/sub2-image-bridge /opt/sub2-image-bridge
# Retain the adopted UI's attribution alongside the installed bridge provenance.
COPY frontend/src/features/image-studio/upstream/LICENSE /app/licenses/image-studio/LICENSE

# Exercise the installed module as the application UID without network or secrets.
RUN --network=none su-exec 1000:1000 /opt/sub2-image-bridge/.venv/bin/python - <<'PY'
import json
import os
import subprocess
import sys
import requests
assert os.getuid() == 1000
assert sys.version_info >= (3, 11)
assert requests.__version__ == "2.34.2"
for provider in ("wan", "seedream"):
    result = subprocess.run(
        [sys.executable, "-m", "sub2_image_bridge"],
        input=json.dumps({"provider": provider, "operation": "capabilities"}),
        text=True, capture_output=True, check=True, timeout=10,
    )
    catalog = json.loads(result.stdout)
    assert catalog["state"] == "completed", catalog
    assert len(catalog["models"]) == 2, catalog
    assert all(model["sizes"] for model in catalog["models"]), catalog
    print(f"bridge smoke PASS: uid=1000 provider={provider} models=2 network=none")
PY

FROM image-bridge-runtime

# Copy binary/resources with ownership to avoid extra full-layer chown copy
COPY --from=backend-builder --chown=sub2api:sub2api /app/sub2api /app/sub2api
COPY --from=backend-builder --chown=sub2api:sub2api /app/sub2api-vault-agent /app/sub2api-vault-agent
COPY --from=backend-builder --chown=sub2api:sub2api /app/sub2api-feishu-notify /app/sub2api-feishu-notify
COPY --from=backend-builder --chown=sub2api:sub2api /app/backend/resources /app/resources

# Create data directory
RUN mkdir -p /app/data && chown sub2api:sub2api /app/data

# Copy entrypoint script (fixes volume permissions then drops to sub2api).
# Set its mode while copying so strict disposable build worktrees never need
# a separate chmod read of the source file.
COPY --chmod=755 deploy/docker-entrypoint.sh /app/docker-entrypoint.sh

# Expose port (can be overridden by SERVER_PORT env var)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -q -T 5 -O /dev/null http://localhost:${SERVER_PORT:-8080}/health || exit 1

# Run the application (entrypoint fixes /app/data ownership then execs as sub2api)
ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/sub2api"]
