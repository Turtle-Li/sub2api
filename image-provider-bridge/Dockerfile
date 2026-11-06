# Optional extension of a locally built Sub2 image. Does not enable the runtime.
ARG SUB2_IMAGE=sub2api:local
FROM ghcr.io/astral-sh/uv:0.9.3 AS uv
FROM ${SUB2_IMAGE}
USER root
RUN apk add --no-cache python3
COPY --from=uv /uv /usr/local/bin/uv
COPY image-provider-bridge/pyproject.toml image-provider-bridge/uv.lock image-provider-bridge/README.md image-provider-bridge/SOURCE_MANIFEST.json /opt/sub2-image-bridge/
COPY image-provider-bridge/sub2_image_bridge /opt/sub2-image-bridge/sub2_image_bridge
RUN cd /opt/sub2-image-bridge && UV_PYTHON_DOWNLOADS=never uv sync --locked --no-dev --no-editable --python /usr/bin/python3 \
    && rm -rf /root/.cache/uv
# Existing Sub2 entrypoint drops privileges. Configuration remains opt-in.
