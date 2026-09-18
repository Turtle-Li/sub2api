#!/usr/bin/env python3
"""Local-only tests for the redacted HTTP probe diagnostic helper."""

import importlib.util
import json
from pathlib import Path
import unittest


HERE = Path(__file__).resolve().parent
HELPER_PATH = HERE / "probe_diagnostics.py"
SPEC = importlib.util.spec_from_file_location("probe_diagnostics_under_test", HELPER_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load helper at {HELPER_PATH}")
diagnostics = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(diagnostics)


class ClassifyResponseTests(unittest.TestCase):
    def test_output_is_fixed_json_safe_and_never_contains_sensitive_input(self) -> None:
        secret = "sk-live-secret; session=super-secret-turn-state"
        result = diagnostics.classify_response(
            403,
            {
                "Authorization": [f"Bearer {secret}"],
                "Set-Cookie": [secret],
                "X-Request-ID": [secret],
            },
            json.dumps(
                {
                    "error": {
                        "code": "untrusted_provider_code",
                        "message": secret,
                    },
                    "state": secret,
                }
            ),
            "upstream",
        )

        self.assertEqual(
            set(result),
            {
                "category",
                "retryable",
                "source",
                "http_status",
                "request_id",
                "error_code",
                "body_kind",
            },
        )
        rendered = json.dumps(result, sort_keys=True)
        self.assertNotIn(secret, rendered)
        self.assertNotIn("Authorization", rendered)
        self.assertNotIn("Set-Cookie", rendered)
        self.assertIsNone(result["request_id"])
        self.assertIsNone(result["error_code"])

    def test_unknown_cloudflare_403_is_not_retryable(self) -> None:
        result = diagnostics.classify_response(
            403,
            {"CF-Ray": ["fe12ab34cd56ef78-HKG"], "Server": ["cloudflare"]},
            "<html><body>challenge</body></html>",
            "upstream",
        )

        self.assertEqual(result["category"], "unknown_forbidden")
        self.assertFalse(result["retryable"])
        self.assertEqual(result["source"], "edge_hint")
        self.assertEqual(result["request_id"], "fe12ab34cd56ef78-HKG")
        self.assertEqual(result["body_kind"], "html")
        self.assertIsNone(result["error_code"])

    def test_confirmed_temporary_403_is_retryable(self) -> None:
        result = diagnostics.classify_response(
            403,
            {},
            '{"error":{"code":"temporarily_unavailable","message":"do not log me"}}',
            "upstream",
        )

        self.assertEqual(result["category"], "temporary_error")
        self.assertTrue(result["retryable"])
        self.assertEqual(result["error_code"], "temporarily_unavailable")

    def test_explicit_permission_403_is_not_retryable(self) -> None:
        result = diagnostics.classify_response(
            403,
            {},
            '{"error":{"type":"insufficient_permissions","message":"secret"}}',
            "upstream",
        )

        self.assertEqual(result["category"], "forbidden")
        self.assertFalse(result["retryable"])
        self.assertEqual(result["error_code"], "insufficient_permissions")

    def test_permission_code_wins_when_json_contains_mixed_codes(self) -> None:
        result = diagnostics.classify_response(
            403,
            {},
            '{"error":{"code":"temporarily_unavailable",'
            '"type":"insufficient_permissions"}}',
            "upstream",
        )

        self.assertEqual(result["error_code"], "insufficient_permissions")
        self.assertEqual(result["category"], "forbidden")
        self.assertFalse(result["retryable"])

    def test_gateway_statuses_are_temporary(self) -> None:
        for status in (502, 503, 504):
            with self.subTest(status=status):
                result = diagnostics.classify_response(status, {}, "", "upstream")
                self.assertEqual(result["category"], "temporary_gateway")
                self.assertTrue(result["retryable"])

    def test_baseline_status_policies_and_proxy_stage(self) -> None:
        expected = {
            200: ("ok", False),
            401: ("authentication_failed", False),
            407: ("proxy_auth", False),
            429: ("rate_limit", False),
        }
        for status, (category, retryable) in expected.items():
            with self.subTest(status=status):
                result = diagnostics.classify_response(status, {}, "", "proxy_connect")
                self.assertEqual(result["category"], category)
                self.assertEqual(result["retryable"], retryable)
                self.assertEqual(result["source"], "proxy")

        upstream = diagnostics.classify_response(200, {}, "", "upstream")
        self.assertEqual(upstream["source"], "upstream")

    def test_request_id_accepts_only_uuid_or_pure_hex(self) -> None:
        uuid_value = "550e8400-e29b-41d4-a716-446655440000"
        hex_value = "ABCDEF0123456789ABCDEF0123456789"
        invalid_values = (
            "session=secret",
            "req-abcdef0123456789",
            "deadbeef",
            "a" * 65,
        )

        accepted_uuid = diagnostics.classify_response(
            200, {"X-Request-ID": [uuid_value]}, "", "upstream"
        )
        accepted_hex = diagnostics.classify_response(
            200, {"Request-ID": [hex_value]}, "", "upstream"
        )
        accepted_cf_ray = diagnostics.classify_response(
            200, {"CF-Ray": ["1234ABCDef567890-IAD"]}, "", "upstream"
        )
        self.assertEqual(accepted_uuid["request_id"], uuid_value)
        self.assertEqual(accepted_hex["request_id"], hex_value.lower())
        self.assertEqual(accepted_cf_ray["request_id"], "1234ABCDef567890-IAD")

        for value in invalid_values:
            with self.subTest(value=value):
                result = diagnostics.classify_response(
                    200, {"X-Request-ID": [value]}, "", "upstream"
                )
                self.assertIsNone(result["request_id"])

    def test_only_supported_json_object_fields_can_supply_error_code(self) -> None:
        nested = diagnostics.classify_response(
            403,
            {},
            '{"details":{"code":"temporarily_unavailable"}}',
            "upstream",
        )
        top_level = diagnostics.classify_response(
            403,
            {},
            '{"code":"service_unavailable","message":"secret"}',
            "upstream",
        )
        oversized_prefix = " " * 8192 + '{"code":"service_unavailable"}'
        truncated = diagnostics.classify_response(403, {}, oversized_prefix, "upstream")

        self.assertEqual(nested["category"], "unknown_forbidden")
        self.assertIsNone(nested["error_code"])
        self.assertEqual(top_level["error_code"], "service_unavailable")
        self.assertTrue(top_level["retryable"])
        self.assertIsNone(truncated["error_code"])
        self.assertFalse(truncated["retryable"])


if __name__ == "__main__":
    unittest.main()
