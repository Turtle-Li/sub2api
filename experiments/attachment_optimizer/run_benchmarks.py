#!/usr/bin/env python3
"""Run reproducible local Attachment Gateway benchmarks.

The script uses only local files and a loopback mock upstream.  It never calls
the production Sub2 endpoint or OpenAI.
"""

from __future__ import annotations

import argparse
import base64
import difflib
import io
import json
import os
import platform
import shutil
import statistics
import subprocess
import tempfile
import threading
import time
import urllib.request
from dataclasses import asdict
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import numpy as np
from PIL import Image, ImageDraw, ImageFont
from skimage.metrics import structural_similarity

from attachment_optimizer import AttachmentOptimizer, OptimizerConfig


class SinkHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        length = int(self.headers.get("Content-Length", "0"))
        remaining = length
        while remaining:
            chunk = self.rfile.read(min(remaining, 1024 * 1024))
            if not chunk:
                break
            remaining -= len(chunk)
        response = b'{"ok":true}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        self.wfile.write(response)

    def log_message(self, _format: str, *args: Any) -> None:
        return


def run_command(command: list[str]) -> tuple[float, subprocess.CompletedProcess[str]]:
    started = time.perf_counter()
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    return (time.perf_counter() - started) * 1000, result


def command_version(command: list[str]) -> str:
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=False)
    except OSError:
        return "missing"
    text = (result.stdout + "\n" + result.stderr).strip().splitlines()
    return text[0].strip() if text else f"exit-{result.returncode}"


def encode_data_url(path: Path) -> str:
    mime = "image/jpeg" if path.suffix.lower() in {".jpg", ".jpeg"} else "image/png"
    return f"data:{mime};base64," + base64.b64encode(path.read_bytes()).decode("ascii")


def create_code_screenshot(path: Path) -> None:
    lines = [
        "func optimizeAttachment(body []byte) ([]byte, error) {",
        "    sum := sha256.Sum256(body)",
        "    if cached, ok := imageCache[sum]; ok {",
        "        return cached, nil",
        "    }",
        "    optimized, err := encodeWebP(body, 85)",
        "    if err != nil { return body, err }",
        "    imageCache[sum] = optimized",
        "    return optimized, nil",
        "}",
        "",
        "// Expected: 14 MB -> about 2 MB, with readable source text.",
    ]
    image = Image.new("RGB", (1800, 1100), "#10141c")
    draw = ImageDraw.Draw(image)
    font_path = Path("/System/Library/Fonts/SFNSMono.ttf")
    if not font_path.exists():
        font_path = Path("/System/Library/Fonts/Menlo.ttc")
    font = ImageFont.truetype(str(font_path), 38)
    draw.rounded_rectangle((55, 50, 1745, 1045), radius=24, fill="#18202b", outline="#35445a", width=3)
    y = 105
    for line_number, line in enumerate(lines, 1):
        draw.text((90, y), f"{line_number:>2}", font=font, fill="#607088")
        draw.text((180, y), line, font=font, fill="#e5edf5")
        y += 68
    image.save(path, "PNG", compress_level=6)


def create_large_visual(source: Path, output: Path) -> None:
    """Create a deterministic ~10.6 MB PNG / ~14.1 MB JSON fixture."""
    with Image.open(source) as original:
        image = original.convert("RGB").resize((2500, 2100), Image.Resampling.LANCZOS)
    pixels = np.asarray(image).astype(np.int16)
    noise = np.random.default_rng(20260720).integers(-12, 13, size=pixels.shape, dtype=np.int16)
    noisy = np.clip(pixels + noise, 0, 255).astype(np.uint8)
    Image.fromarray(noisy).save(output, "PNG", compress_level=6)


def image_ssim(original_path: Path, candidate_path: Path) -> float:
    with Image.open(original_path) as original:
        left = np.asarray(original.convert("RGB"), dtype=np.uint8)
    with Image.open(candidate_path) as candidate:
        right = np.asarray(candidate.convert("RGB").resize((left.shape[1], left.shape[0])), dtype=np.uint8)
    return float(structural_similarity(left, right, channel_axis=2, data_range=255))


def normalized_ocr(path: Path) -> str:
    result = subprocess.run(
        ["tesseract", str(path), "stdout", "--psm", "6", "-l", "eng"],
        capture_output=True,
        text=True,
        check=False,
    )
    return " ".join(result.stdout.lower().split())


def ocr_similarity(baseline: str, candidate_path: Path) -> float:
    candidate = normalized_ocr(candidate_path)
    return difflib.SequenceMatcher(a=baseline, b=candidate).ratio()


def run_png_tools(fixtures: dict[str, Path], output_dir: Path) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    for category, source in fixtures.items():
        original_size = source.stat().st_size
        variants = [
            (
                "oxipng_o4",
                output_dir / f"{category}-oxipng.png",
                lambda target: ["oxipng", "-q", "-o", "4", "--strip", "safe", "--out", str(target), str(source)],
                True,
            ),
            (
                "zopflipng",
                output_dir / f"{category}-zopflipng.png",
                lambda target: ["zopflipng", "-y", str(source), str(target)],
                True,
            ),
            (
                "pngquant_q80_95",
                output_dir / f"{category}-pngquant.png",
                lambda target: [
                    "pngquant", "--force", "--quality", "80-95", "--speed", "1",
                    "--output", str(target), str(source),
                ],
                False,
            ),
        ]
        for tool, target, command_builder, lossless in variants:
            elapsed_ms, completed = run_command(command_builder(target))
            row: dict[str, Any] = {
                "category": category,
                "tool": tool,
                "lossless": lossless,
                "original_bytes": original_size,
                "elapsed_ms": round(elapsed_ms, 3),
                "exit_code": completed.returncode,
            }
            if completed.returncode == 0 and target.exists():
                optimized_size = target.stat().st_size
                row.update({
                    "optimized_bytes": optimized_size,
                    "saved_percent": round((1 - optimized_size / original_size) * 100, 3),
                    "ssim": round(image_ssim(source, target), 6),
                })
            else:
                row["error"] = (completed.stderr or completed.stdout).strip()[-500:]
            results.append(row)
    return results


def run_visual_tools(fixtures: dict[str, Path], output_dir: Path) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    code_ocr = normalized_ocr(fixtures["code"])
    for category, source in fixtures.items():
        original_size = source.stat().st_size
        variants = [
            ("jpeg_q80", "jpg", 80),
            ("jpeg_q90", "jpg", 90),
            ("webp_q80", "webp", 80),
            ("webp_q85", "webp", 85),
            ("webp_q90", "webp", 90),
        ]
        for label, extension, quality in variants:
            target = output_dir / f"{category}-{label}.{extension}"
            started = time.perf_counter()
            if extension == "webp":
                elapsed_ms, completed = run_command([
                    "cwebp", "-quiet", "-m", "6", "-q", str(quality), str(source), "-o", str(target)
                ])
            else:
                with Image.open(source) as image:
                    image.convert("RGB").save(target, "JPEG", quality=quality, optimize=True, progressive=True)
                elapsed_ms = (time.perf_counter() - started) * 1000
                completed = subprocess.CompletedProcess([], 0, "", "")
            row: dict[str, Any] = {
                "category": category,
                "tool": label,
                "quality": quality,
                "original_bytes": original_size,
                "elapsed_ms": round(elapsed_ms, 3),
                "exit_code": completed.returncode,
            }
            if completed.returncode == 0 and target.exists():
                optimized_size = target.stat().st_size
                row.update({
                    "optimized_bytes": optimized_size,
                    "saved_percent": round((1 - optimized_size / original_size) * 100, 3),
                    "ssim": round(image_ssim(source, target), 6),
                })
                if category == "code":
                    row["ocr_similarity"] = round(ocr_similarity(code_ocr, target), 6)
            else:
                row["error"] = (completed.stderr or completed.stdout).strip()[-500:]
            results.append(row)
    return results


def request_payload(images: list[Path], long_context_bytes: int = 0) -> bytes:
    content: list[dict[str, Any]] = [{"type": "input_text", "text": "Describe the images and preserve small text."}]
    content.extend({"type": "input_image", "image_url": encode_data_url(path), "detail": "high"} for path in images)
    context = ""
    if long_context_bytes:
        unit = "Attachment context line: keep this repeated context for transport testing.\n"
        context = (unit * (long_context_bytes // len(unit) + 1))[:long_context_bytes]
    request = {
        "model": "gpt-5.6-terra",
        "stream": True,
        "instructions": context,
        "input": [{"type": "message", "role": "user", "content": content}],
    }
    return json.dumps(request, separators=(",", ":")).encode("utf-8")


def loopback_forward(url: str, payload: bytes, samples: int = 5) -> dict[str, Any]:
    timings: list[float] = []
    for _ in range(samples):
        request = urllib.request.Request(url, data=payload, method="POST", headers={"Content-Type": "application/json"})
        started = time.perf_counter()
        with urllib.request.urlopen(request, timeout=30) as response:
            response.read()
        timings.append((time.perf_counter() - started) * 1000)
    return {
        "samples": samples,
        "median_ms": round(statistics.median(timings), 3),
        "min_ms": round(min(timings), 3),
        "max_ms": round(max(timings), 3),
    }


def run_payload_scenarios(
    large: Path,
    five_images: list[Path],
    work_dir: Path,
) -> list[dict[str, Any]]:
    server = ThreadingHTTPServer(("127.0.0.1", 0), SinkHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    url = f"http://127.0.0.1:{server.server_address[1]}/v1/responses"
    scenarios = [
        ("one_large_image", [large], 0),
        ("five_images", five_images, 0),
        ("large_image_plus_1mb_context", [large], 1024 * 1024),
    ]
    results: list[dict[str, Any]] = []
    try:
        for name, images, context_bytes in scenarios:
            payload = request_payload(images, context_bytes)
            cache_dir = work_dir / f"cache-{name}"
            optimizer = AttachmentOptimizer(OptimizerConfig(
                attachment_optimizer_enabled=True,
                threshold_bytes=128 * 1024,
                quality=85,
                cache_dir=str(cache_dir),
            ))
            optimized, cold = optimizer.optimize_payload(payload)
            warm_output, warm = optimizer.optimize_payload(payload)
            assert optimized == warm_output

            url_optimizer = AttachmentOptimizer(OptimizerConfig(
                attachment_optimizer_enabled=True,
                threshold_bytes=128 * 1024,
                quality=85,
                cache_dir=str(cache_dir),
                replacement_mode="url",
                public_base_url="https://attachments.example.test/images/",
            ))
            url_payload, url_stats = url_optimizer.optimize_payload(payload)
            original_bytes = len(payload)
            optimized_bytes = len(optimized)
            results.append({
                "scenario": name,
                "image_count": len(images),
                "long_context_bytes": context_bytes,
                "original_body_bytes": original_bytes,
                "optimized_data_url_body_bytes": optimized_bytes,
                "optimized_url_body_bytes": len(url_payload),
                "data_url_saved_percent": round((1 - optimized_bytes / original_bytes) * 100, 3),
                "url_saved_percent": round((1 - len(url_payload) / original_bytes) * 100, 3),
                "cold_optimizer": asdict(cold),
                "warm_optimizer": asdict(warm),
                "url_optimizer": asdict(url_stats),
                "loopback_original": loopback_forward(url, payload),
                "loopback_optimized": loopback_forward(url, optimized),
                "theoretical_upload_ms_at_5mbps": round(original_bytes * 8 / 5_000_000 * 1000, 3),
                "theoretical_optimized_upload_ms_at_5mbps": round(optimized_bytes * 8 / 5_000_000 * 1000, 3),
                "caddy_10mb_status_without_pre_ingress_optimizer": 413 if original_bytes > 10_000_000 else 200,
                "caddy_10mb_status_if_pre_ingress_optimized": 413 if optimized_bytes > 10_000_000 else 200,
            })
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    return results


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--asset-root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()

    required_commands = ["pngquant", "zopflipng", "oxipng", "cwebp", "tesseract"]
    missing = [name for name in required_commands if shutil.which(name) is None]
    if missing:
        raise SystemExit(f"missing benchmark commands: {', '.join(missing)}")

    asset_root = args.asset_root.resolve()
    photo_source = asset_root / "ChatGPT Image 2026年6月14日 02_04_36.png"
    illustration_source = asset_root / "ChatGPT Image 2026年6月14日 01_46_56.png"
    screenshot_source = asset_root / "admin-network-screenshot.png"
    small_screenshot_source = asset_root / "Weixin Image_20260717023702_2593_177.png"
    for path in (photo_source, illustration_source, screenshot_source, small_screenshot_source):
        if not path.is_file():
            raise SystemExit(f"missing local fixture: {path}")

    with tempfile.TemporaryDirectory(prefix="sub2-attachment-benchmark-") as temporary:
        work_dir = Path(temporary)
        code_source = work_dir / "code.png"
        large_source = work_dir / "large.png"
        create_code_screenshot(code_source)
        create_large_visual(photo_source, large_source)
        representative = {
            "photo": photo_source,
            "screenshot": screenshot_source,
            "code": code_source,
        }
        png_output = work_dir / "png-tools"
        visual_output = work_dir / "visual-tools"
        png_output.mkdir()
        visual_output.mkdir()

        report = {
            "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "environment": {
                "platform": platform.platform(),
                "machine": platform.machine(),
                "python": platform.python_version(),
                "pillow": Image.__version__,
                "numpy": np.__version__,
                "tool_versions": {
                    "pngquant": command_version(["pngquant", "--version"]),
                    "zopflipng": command_version(["zopflipng"]),
                    "oxipng": command_version(["oxipng", "--version"]),
                    "cwebp": command_version(["cwebp", "-version"]),
                    "tesseract": command_version(["tesseract", "--version"]),
                },
            },
            "fixtures": {
                key: {
                    "filename": path.name,
                    "bytes": path.stat().st_size,
                    "dimensions": list(Image.open(path).size),
                }
                for key, path in {**representative, "large": large_source}.items()
            },
            "png_tools": run_png_tools(representative, png_output),
            "visual_tools": run_visual_tools(representative, visual_output),
            "payload_scenarios": run_payload_scenarios(
                large_source,
                [photo_source, illustration_source, screenshot_source, small_screenshot_source, code_source],
                work_dir,
            ),
            "safety": {
                "network_targets": ["127.0.0.1 loopback mock only"],
                "production_requests": 0,
                "openai_requests": 0,
            },
        }

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True), encoding="utf-8")
    print(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
