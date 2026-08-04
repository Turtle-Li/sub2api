#!/usr/bin/env bash
# 本地构建镜像的快速脚本，避免在命令行反复输入构建参数。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

docker build -t sub2api:latest \
    --build-arg GOPROXY=https://goproxy.cn,direct \
    --build-arg GOSUMDB=sum.golang.google.cn \
    -f "${REPO_ROOT}/Dockerfile" \
    "${REPO_ROOT}"

# Keep incremental build caches useful without allowing them to consume the
# whole system disk. Runtime images and container data are not affected.
if [ "${PRUNE_BUILD_CACHE:-true}" = "true" ]; then
    if docker buildx prune --help 2>&1 | grep -q -- '--max-used-space'; then
        docker buildx prune --all --force \
            --filter "until=${BUILD_CACHE_MAX_AGE:-72h}"
        docker buildx prune --all --force \
            --max-used-space "${BUILD_CACHE_MAX_USED_SPACE:-8GB}"
    else
        docker builder prune --all --force \
            --filter "until=${BUILD_CACHE_MAX_AGE:-168h}"
    fi
fi
