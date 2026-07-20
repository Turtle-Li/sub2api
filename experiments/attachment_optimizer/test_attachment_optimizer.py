from __future__ import annotations

import base64
import io
import json
import tempfile
import unittest
from pathlib import Path

from PIL import Image

from attachment_optimizer import AttachmentOptimizer, OptimizerConfig


def png_data_url(width: int = 512, height: int = 512) -> str:
    image = Image.new("RGB", (width, height), (20, 100, 180))
    data = io.BytesIO()
    image.save(data, "PNG")
    return "data:image/png;base64," + base64.b64encode(data.getvalue()).decode("ascii")


class AttachmentOptimizerTest(unittest.TestCase):
    def test_disabled_is_exact_noop(self) -> None:
        payload = b'{ "model": "gpt-test", "input": "unchanged" }\n'
        output, stats = AttachmentOptimizer(OptimizerConfig()).optimize_payload(payload)
        self.assertEqual(output, payload)
        self.assertFalse(stats.enabled)

    def test_optimizes_responses_image_and_preserves_detail(self) -> None:
        with tempfile.TemporaryDirectory() as cache:
            request = {
                "model": "gpt-test",
                "input": [{
                    "role": "user",
                    "content": [{
                        "type": "input_image",
                        "image_url": png_data_url(),
                        "detail": "high",
                    }],
                }],
            }
            payload = json.dumps(request).encode()
            optimizer = AttachmentOptimizer(OptimizerConfig(
                attachment_optimizer_enabled=True,
                threshold_bytes=0,
                cache_dir=cache,
            ))
            output, stats = optimizer.optimize_payload(payload)
            rewritten = json.loads(output)
            part = rewritten["input"][0]["content"][0]
            self.assertTrue(part["image_url"].startswith("data:image/webp;base64,"))
            self.assertEqual(part["detail"], "high")
            self.assertEqual(stats.images_optimized, 1)

    def test_reuses_content_addressed_cache(self) -> None:
        with tempfile.TemporaryDirectory() as cache:
            request = {
                "model": "gpt-test",
                "input": [{"type": "input_image", "image_url": png_data_url()}],
            }
            payload = json.dumps(request).encode()
            optimizer = AttachmentOptimizer(OptimizerConfig(
                attachment_optimizer_enabled=True,
                threshold_bytes=0,
                cache_dir=cache,
            ))
            first, first_stats = optimizer.optimize_payload(payload)
            second, second_stats = optimizer.optimize_payload(payload)
            self.assertEqual(first, second)
            self.assertEqual(first_stats.cache_hits, 0)
            self.assertEqual(second_stats.cache_hits, 1)
            self.assertEqual(len(list(Path(cache).glob("*.webp"))), 1)
            self.assertEqual(len(list(Path(cache).glob("*.metadata.json"))), 1)

    def test_url_mode_replaces_with_hash_url(self) -> None:
        with tempfile.TemporaryDirectory() as cache:
            request = {
                "model": "gpt-test",
                "input": [{"type": "input_image", "image_url": png_data_url()}],
            }
            output, stats = AttachmentOptimizer(OptimizerConfig(
                attachment_optimizer_enabled=True,
                threshold_bytes=0,
                cache_dir=cache,
                replacement_mode="url",
                public_base_url="https://attachments.example.test/images/",
            )).optimize_payload(json.dumps(request).encode())
            image_url = json.loads(output)["input"][0]["image_url"]
            self.assertRegex(image_url, r"^https://attachments\.example\.test/images/[a-f0-9]{64}\.webp$")
            self.assertEqual(stats.images_optimized, 1)

    def test_file_id_and_remote_url_are_untouched(self) -> None:
        with tempfile.TemporaryDirectory() as cache:
            request = {
                "model": "gpt-test",
                "input": [
                    {"type": "input_image", "file_id": "file_123"},
                    {"type": "input_image", "image_url": "https://example.test/a.png"},
                ],
            }
            payload = json.dumps(request).encode()
            output, stats = AttachmentOptimizer(OptimizerConfig(
                attachment_optimizer_enabled=True,
                threshold_bytes=0,
                cache_dir=cache,
            )).optimize_payload(payload)
            rewritten = json.loads(output)
            self.assertEqual(rewritten, request)
            self.assertEqual(stats.images_detected, 0)

    def test_invalid_base64_fails_open(self) -> None:
        with tempfile.TemporaryDirectory() as cache:
            request = {
                "model": "gpt-test",
                "input": [{
                    "type": "input_image",
                    "image_url": "data:image/png;base64,not-valid%%",
                }],
            }
            original_url = request["input"][0]["image_url"]
            output, stats = AttachmentOptimizer(OptimizerConfig(
                attachment_optimizer_enabled=True,
                threshold_bytes=0,
                cache_dir=cache,
            )).optimize_payload(json.dumps(request).encode())
            self.assertEqual(json.loads(output)["input"][0]["image_url"], original_url)
            self.assertEqual(stats.errors, 1)


if __name__ == "__main__":
    unittest.main()
