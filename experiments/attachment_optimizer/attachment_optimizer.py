#!/usr/bin/env python3
"""Offline Attachment Gateway proof of concept.

This module is intentionally not imported by the Sub2 runtime.  It rewrites
OpenAI Responses-style image data URLs in a JSON payload and stores optimized
images in a content-addressed local cache.
"""

from __future__ import annotations

import argparse
import base64
import binascii
import hashlib
import io
import json
import os
import stat
import tempfile
import time
from dataclasses import asdict, dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any
from urllib.parse import urljoin, urlparse

from PIL import Image, ImageOps, UnidentifiedImageError, __version__ as PILLOW_VERSION


SUPPORTED_INPUT_MIME_TYPES = {
    "image/png",
    "image/jpeg",
    "image/webp",
    "image/gif",
}
OPTIMIZER_ID = f"Pillow-{PILLOW_VERSION}/webp"

# Refuse decompression bombs before allocating a full raster.  The experiment
# limit is deliberately lower than OpenAI's API-wide payload allowance.
Image.MAX_IMAGE_PIXELS = 50_000_000


@dataclass(frozen=True)
class OptimizerConfig:
    attachment_optimizer_enabled: bool = False
    threshold_bytes: int = 512 * 1024
    max_image_bytes: int = 64 * 1024 * 1024
    quality: int = 85
    min_savings_ratio: float = 0.05
    cache_dir: str = "image_cache"
    ttl_seconds: int = 7 * 24 * 60 * 60
    replacement_mode: str = "data_url"  # data_url | url
    public_base_url: str = ""

    def validate(self) -> None:
        if self.threshold_bytes < 0:
            raise ValueError("threshold_bytes must be non-negative")
        if self.max_image_bytes <= 0:
            raise ValueError("max_image_bytes must be positive")
        if not 1 <= self.quality <= 100:
            raise ValueError("quality must be between 1 and 100")
        if not 0 <= self.min_savings_ratio < 1:
            raise ValueError("min_savings_ratio must be in [0, 1)")
        if self.ttl_seconds <= 0:
            raise ValueError("ttl_seconds must be positive")
        if self.replacement_mode not in {"data_url", "url"}:
            raise ValueError("replacement_mode must be data_url or url")
        if self.replacement_mode == "url":
            parsed = urlparse(self.public_base_url)
            if parsed.scheme not in {"http", "https"} or not parsed.netloc:
                raise ValueError("url mode requires an absolute http(s) public_base_url")


@dataclass
class OptimizerStats:
    enabled: bool
    input_body_bytes: int
    output_body_bytes: int = 0
    images_detected: int = 0
    images_optimized: int = 0
    cache_hits: int = 0
    skipped_below_threshold: int = 0
    skipped_not_smaller: int = 0
    skipped_unsupported: int = 0
    errors: int = 0
    original_image_bytes: int = 0
    optimized_image_bytes: int = 0
    elapsed_ms: float = 0.0


@dataclass(frozen=True)
class CacheEntry:
    original_hash: str
    optimized_hash: str
    original_size: int
    optimized_size: int
    original_media_type: str
    optimized_media_type: str
    width: int
    height: int
    quality: int
    optimizer: str
    created_at: str
    expires_at: str


@dataclass(frozen=True)
class OptimizedImage:
    source_hash: str
    optimized_bytes: bytes
    metadata: CacheEntry
    cache_hit: bool


class AttachmentOptimizer:
    """Rewrites eligible image data URLs without touching production code."""

    def __init__(self, config: OptimizerConfig):
        config.validate()
        self.config = config
        self.cache_dir = Path(config.cache_dir).expanduser().resolve()

    def optimize_payload(self, payload: bytes) -> tuple[bytes, OptimizerStats]:
        started = time.perf_counter()
        stats = OptimizerStats(
            enabled=self.config.attachment_optimizer_enabled,
            input_body_bytes=len(payload),
        )
        if not self.config.attachment_optimizer_enabled:
            stats.output_body_bytes = len(payload)
            stats.elapsed_ms = (time.perf_counter() - started) * 1000
            return payload, stats

        try:
            document = json.loads(payload)
        except json.JSONDecodeError as exc:
            raise ValueError(f"invalid JSON payload: {exc}") from exc

        self._walk(document, stats)
        output = json.dumps(document, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        stats.output_body_bytes = len(output)
        stats.elapsed_ms = (time.perf_counter() - started) * 1000
        return output, stats

    def _walk(self, value: Any, stats: OptimizerStats) -> None:
        if isinstance(value, list):
            for item in value:
                self._walk(item, stats)
            return
        if not isinstance(value, dict):
            return

        part_type = str(value.get("type", "")).strip()
        if part_type == "input_image" and isinstance(value.get("image_url"), str):
            value["image_url"] = self._maybe_optimize_url(value["image_url"], stats)
        elif part_type == "image_url":
            image_url = value.get("image_url")
            if isinstance(image_url, str):
                value["image_url"] = self._maybe_optimize_url(image_url, stats)
            elif isinstance(image_url, dict) and isinstance(image_url.get("url"), str):
                image_url["url"] = self._maybe_optimize_url(image_url["url"], stats)

        for child in value.values():
            self._walk(child, stats)

    def _maybe_optimize_url(self, raw_url: str, stats: OptimizerStats) -> str:
        try:
            parsed = parse_image_data_url(raw_url, self.config.max_image_bytes)
        except ValueError:
            stats.images_detected += 1
            stats.errors += 1
            return raw_url
        if parsed is None:
            return raw_url
        stats.images_detected += 1
        media_type, source = parsed
        stats.original_image_bytes += len(source)
        if len(source) < self.config.threshold_bytes:
            stats.skipped_below_threshold += 1
            return raw_url

        try:
            optimized = self._load_or_create(source, media_type)
        except (OSError, ValueError, UnidentifiedImageError, Image.DecompressionBombError):
            # Experimental fail-open behavior: never corrupt or reject the request.
            stats.errors += 1
            return raw_url

        minimum_saved = int(len(source) * self.config.min_savings_ratio)
        if len(optimized.optimized_bytes) + minimum_saved >= len(source):
            stats.skipped_not_smaller += 1
            return raw_url

        stats.images_optimized += 1
        stats.optimized_image_bytes += len(optimized.optimized_bytes)
        if optimized.cache_hit:
            stats.cache_hits += 1

        if self.config.replacement_mode == "url":
            return urljoin(
                self.config.public_base_url.rstrip("/") + "/",
                f"{optimized.source_hash}.webp",
            )
        encoded = base64.b64encode(optimized.optimized_bytes).decode("ascii")
        return f"data:image/webp;base64,{encoded}"

    def _load_or_create(self, source: bytes, media_type: str) -> OptimizedImage:
        source_hash = hashlib.sha256(source).hexdigest()
        image_path = self.cache_dir / f"{source_hash}.webp"
        metadata_path = self.cache_dir / f"{source_hash}.metadata.json"

        cached = self._read_cache(image_path, metadata_path, source_hash)
        if cached is not None:
            return cached

        optimized_bytes, width, height = encode_webp(source, self.config.quality)
        optimized_hash = hashlib.sha256(optimized_bytes).hexdigest()
        now = datetime.now(timezone.utc)
        metadata = CacheEntry(
            original_hash=source_hash,
            optimized_hash=optimized_hash,
            original_size=len(source),
            optimized_size=len(optimized_bytes),
            original_media_type=media_type,
            optimized_media_type="image/webp",
            width=width,
            height=height,
            quality=self.config.quality,
            optimizer=OPTIMIZER_ID,
            created_at=now.isoformat(),
            expires_at=(now + timedelta(seconds=self.config.ttl_seconds)).isoformat(),
        )
        self._write_cache(image_path, metadata_path, optimized_bytes, metadata)
        return OptimizedImage(source_hash, optimized_bytes, metadata, False)

    def _read_cache(
        self,
        image_path: Path,
        metadata_path: Path,
        source_hash: str,
    ) -> OptimizedImage | None:
        try:
            metadata_raw = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata = CacheEntry(**metadata_raw)
            expires_at = datetime.fromisoformat(metadata.expires_at)
            if expires_at <= datetime.now(timezone.utc):
                return None
            if (
                metadata.original_hash != source_hash
                or metadata.quality != self.config.quality
                or metadata.optimizer != OPTIMIZER_ID
            ):
                return None
            optimized_bytes = image_path.read_bytes()
            if len(optimized_bytes) != metadata.optimized_size:
                return None
            if hashlib.sha256(optimized_bytes).hexdigest() != metadata.optimized_hash:
                return None
            return OptimizedImage(source_hash, optimized_bytes, metadata, True)
        except (OSError, ValueError, TypeError, json.JSONDecodeError):
            return None

    def _write_cache(
        self,
        image_path: Path,
        metadata_path: Path,
        optimized_bytes: bytes,
        metadata: CacheEntry,
    ) -> None:
        self.cache_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(self.cache_dir, stat.S_IRWXU)
        atomic_write(image_path, optimized_bytes)
        metadata_bytes = json.dumps(
            asdict(metadata), ensure_ascii=False, sort_keys=True, indent=2
        ).encode("utf-8")
        atomic_write(metadata_path, metadata_bytes)


def parse_image_data_url(raw: str, max_image_bytes: int) -> tuple[str, bytes] | None:
    if not raw.lower().startswith("data:image/"):
        return None
    header, separator, encoded = raw.partition(",")
    if not separator:
        return None
    header_parts = header[5:].split(";")
    media_type = header_parts[0].lower()
    parameters = {part.lower() for part in header_parts[1:]}
    if media_type not in SUPPORTED_INPUT_MIME_TYPES or "base64" not in parameters:
        return None

    compact = "".join(encoded.split())
    if len(compact) > ((max_image_bytes + 2) // 3) * 4 + 4:
        raise ValueError("decoded image exceeds max_image_bytes")
    try:
        decoded = base64.b64decode(compact, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise ValueError("invalid base64 image") from exc
    if len(decoded) > max_image_bytes:
        raise ValueError("decoded image exceeds max_image_bytes")
    return media_type, decoded


def encode_webp(source: bytes, quality: int) -> tuple[bytes, int, int]:
    with Image.open(io.BytesIO(source)) as probe:
        if getattr(probe, "n_frames", 1) != 1:
            raise ValueError("animated images are not supported")
        probe.load()
        oriented = ImageOps.exif_transpose(probe)
        width, height = oriented.size
        if oriented.mode not in {"RGB", "RGBA"}:
            has_alpha = "A" in oriented.getbands() or "transparency" in oriented.info
            target_mode = "RGBA" if has_alpha else "RGB"
            image = oriented.convert(target_mode)
        else:
            image = oriented.copy()

    output = io.BytesIO()
    image.save(
        output,
        format="WEBP",
        quality=quality,
        method=6,
        optimize=True,
        exact=True,
    )
    return output.getvalue(), width, height


def atomic_write(path: Path, content: bytes) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, stat.S_IRUSR | stat.S_IWUSR)
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def config_from_args(args: argparse.Namespace) -> OptimizerConfig:
    values: dict[str, Any] = {}
    if args.config:
        values = json.loads(Path(args.config).read_text(encoding="utf-8"))
    overrides = {
        "attachment_optimizer_enabled": args.attachment_optimizer_enabled,
        "threshold_bytes": args.threshold_bytes,
        "max_image_bytes": args.max_image_bytes,
        "quality": args.quality,
        "cache_dir": args.cache_dir,
        "ttl_seconds": args.ttl_seconds,
        "replacement_mode": args.replacement_mode,
        "public_base_url": args.public_base_url,
    }
    for key, value in overrides.items():
        if value is not None:
            values[key] = value
    return OptimizerConfig(**values)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, help="Responses request JSON")
    parser.add_argument("--output", required=True, help="rewritten request JSON")
    parser.add_argument("--config", help="optional JSON configuration")
    parser.add_argument(
        "--attachment-optimizer-enabled",
        action=argparse.BooleanOptionalAction,
        default=None,
        help="experiment gate; defaults to false",
    )
    parser.add_argument("--threshold-bytes", type=int)
    parser.add_argument("--max-image-bytes", type=int)
    parser.add_argument("--quality", type=int)
    parser.add_argument("--cache-dir")
    parser.add_argument("--ttl-seconds", type=int)
    parser.add_argument("--replacement-mode", choices=("data_url", "url"))
    parser.add_argument("--public-base-url")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    config = config_from_args(args)
    source = Path(args.input).read_bytes()
    output, stats = AttachmentOptimizer(config).optimize_payload(source)
    Path(args.output).write_bytes(output)
    print(json.dumps(asdict(stats), ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
