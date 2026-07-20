# Attachment Optimizer experiment

This is a local-only proof of concept. It is not imported by, registered in, or
wired to the Sub2 server. The exact default is:

```json
"attachment_optimizer_enabled": false
```

The optimizer scans JSON for Responses `input_image.image_url` data URLs,
decodes images above a threshold, converts them to WebP, caches the result by
SHA-256 of the decoded source bytes, and rewrites the payload. It can emit a
smaller data URL or an HTTPS URL. URL mode only demonstrates payload rewriting;
the configured URL must be reachable by OpenAI in a real deployment.

Run tests:

```bash
cd experiments/attachment_optimizer
python3 -m unittest -v
```

Run the no-op default:

```bash
python3 attachment_optimizer.py \
  --input request.json \
  --output unchanged.json \
  --config config.example.json
```

Explicitly enable the experiment:

```bash
python3 attachment_optimizer.py \
  --input request.json \
  --output optimized.json \
  --attachment-optimizer-enabled \
  --threshold-bytes 524288 \
  --quality 85 \
  --cache-dir ./image_cache
```

Cache entries use this shape:

```text
image_cache/
  <source-sha256>.webp
  <source-sha256>.metadata.json
```

The metadata records both hashes, byte sizes, dimensions, quality, creation
time, and expiry time. Cache files are written atomically with owner-only
permissions.

Run the reproducible local benchmark (never contacts production or OpenAI):

```bash
python3 run_benchmarks.py \
  --asset-root ../../.. \
  --output ../../docs/reports/data/attachment_optimizer_benchmark.json
```

The benchmark requires `pngquant`, `zopflipng`, `oxipng`, `cwebp`, `tesseract`,
Pillow, NumPy, and scikit-image. Its OpenAI forward target is a loopback HTTP
sink, so timing is useful for relative payload/CPU comparison only.

Run a deliberately small real-client WebSocket transport canary from an
authorized machine that already has Codex configured:

```bash
python3 ws_transport_canary.py --turns 2
```

For a known-answer PNG/JPEG/WebP first-frame check, use one turn and require a
marker that the prompt permits only when the expected visual facts are present:

```bash
python3 ws_transport_canary.py \
  --turns 1 \
  --image ./known-answer.jpg \
  --expected-marker IMAGE_WS_OK \
  --image-check-prompt "Inspect the image and reply exactly IMAGE_WS_OK only when the known facts match."
```

The canary reads that machine's existing Codex provider and API-key files. It
never prints either value, the prompt, image contents, URL, or model output. It
emits only byte count, turn number, terminal event, timing, marker match, and
close status. Keep it inside the dedicated canary directory; do not copy
credential files with it.
