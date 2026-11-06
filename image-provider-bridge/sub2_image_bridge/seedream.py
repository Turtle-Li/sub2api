from __future__ import annotations
import base64
import binascii
import hashlib
import json
import re
from typing import Any, Callable
from urllib.parse import quote, urlsplit
import requests
from .types import Credential, ProviderPhaseAdapterError, ProviderPhaseContractError, ProviderSubmissionDiagnosticCategory
SeedreamProviderCredential = Credential
from .seedream_model_policy import *
SEEDREAM_ENDPOINT = "https://ark.cn-beijing.volces.com/api/v3"
SEEDREAM_PATH = "/images/generations"
_SEEDREAM_LEGACY_BODY_FIELDS = frozenset({
    "model",
    "prompt",
    "image",
    "size",
    "response_format",
    "sequential_image_generation",
    "watermark",
})
_SEEDREAM_PRO_BODY_FIELDS = frozenset({
    "model",
    "prompt",
    "image",
    "size",
    "response_format",
    "watermark",
    "output_format",
})
_SEEDREAM_SEQUENTIAL_PRO_BODY_FIELDS = frozenset({
    *_SEEDREAM_LEGACY_BODY_FIELDS,
    "output_format",
})
_SEEDREAM_SEQUENTIAL_MULTI_BODY_FIELDS = frozenset({
    *_SEEDREAM_LEGACY_BODY_FIELDS,
    "sequential_image_generation_options",
})
_SEEDREAM_SEQUENTIAL_PRO_MULTI_BODY_FIELDS = frozenset({
    *_SEEDREAM_SEQUENTIAL_PRO_BODY_FIELDS,
    "sequential_image_generation_options",
})
_MAX_SEEDREAM_GENERATED_IMAGES = 15


class SeedreamProviderPhaseError(ProviderPhaseAdapterError):
    def __init__(
        self,
        code: str,
        message: str,
        *,
        retryable: bool,
        outcome_unknown: bool = False,
        submission_diagnostic_category: (
            ProviderSubmissionDiagnosticCategory | None
        ) = None,
    ):
        super().__init__(message, retryable=retryable)
        self.code = (
            code
            if re.fullmatch(r"[A-Z0-9_]{2,80}", code)
            else "SEEDREAM_PROVIDER_ERROR"
        )
        self.outcome_unknown = outcome_unknown
        self.submission_diagnostic_category = (
            submission_diagnostic_category
        )

def _valid_sequential_options(
    value: Any,
    expected_count: int,
    reference_count: int,
) -> bool:
    return (
        isinstance(value, dict)
        and set(value) == {"max_images"}
        and isinstance(value.get("max_images"), int)
        and not isinstance(value.get("max_images"), bool)
        and value["max_images"] == expected_count
        and 2 <= expected_count <= _MAX_SEEDREAM_GENERATED_IMAGES
        - reference_count
    )


class SeedreamHTTPClient:
    def __init__(
        self,
        credential: SeedreamProviderCredential,
        *,
        session: requests.Session | None = None,
    ):
        self.credential = credential
        self.session = session or requests.Session()

    def generate(
        self,
        body: dict[str, Any],
        request_key: str,
    ) -> tuple[dict[str, Any], str]:
        try:
            response = self.session.post(
                SEEDREAM_ENDPOINT + SEEDREAM_PATH,
                headers={
                    "Authorization":
                        f"Bearer {self.credential.api_key}",
                    "Content-Type": "application/json",
                    "Accept": "application/json",
                    "X-Client-Request-Id": request_key,
                    "User-Agent": "vso-provider-phase/0.4.0",
                },
                json=body,
                timeout=self.credential.timeout_seconds,
                allow_redirects=False,
            )
        except requests.RequestException:
            raise SeedreamProviderPhaseError(
                "SEEDREAM_SUBMIT_OUTCOME_UNKNOWN",
                "Seedream transport outcome is unknown",
                retryable=True,
                outcome_unknown=True,
                submission_diagnostic_category=(
                    ProviderSubmissionDiagnosticCategory
                    .TRANSPORT_EXCEPTION
                ),
            ) from None
        try:
            try:
                value = response.json()
            except ValueError:
                value = {}
            if not 200 <= response.status_code < 300:
                code = "SEEDREAM_HTTP_ERROR"
                if isinstance(value, dict):
                    error = value.get("error")
                    code = str(
                        error.get("code")
                        if isinstance(error, dict)
                        else value.get("code")
                        or code
                    )
                fixed = re.sub(
                    r"[^A-Z0-9_]", "_", code.upper()
                )[:80]
                raise SeedreamProviderPhaseError(
                    fixed,
                    "Seedream request was rejected",
                    retryable=response.status_code >= 500,
                    outcome_unknown=response.status_code >= 500,
                    submission_diagnostic_category=(
                        ProviderSubmissionDiagnosticCategory.HTTP_5XX
                        if response.status_code >= 500
                        else None
                    ),
                )
            if not isinstance(value, dict):
                raise SeedreamProviderPhaseError(
                    "SEEDREAM_RESPONSE_INVALID",
                    "Seedream returned invalid JSON",
                    retryable=False,
                    outcome_unknown=True,
                    submission_diagnostic_category=(
                        ProviderSubmissionDiagnosticCategory
                        .HTTP_2XX_IMAGE_MISSING
                    ),
                )
            request_id = str(
                response.headers.get("x-request-id")
                or value.get("request_id")
                or ""
            )
            if not request_id:
                request_id = "ark-" + hashlib.sha256(
                    json.dumps(
                        value,
                        sort_keys=True,
                        separators=(",", ":"),
                    ).encode("utf-8")
                ).hexdigest()[:32]
            request_id = re.sub(
                r"[^A-Za-z0-9._:-]", "-", request_id
            )[:200]
            return value, request_id
        finally:
            response.close()


def _images(
    response: dict[str, Any],
    expected_count: int,
) -> tuple[tuple[bytes, str], ...]:
    data = response.get("data")
    if (
        not isinstance(data, list)
        or len(data) != expected_count
        or any(
            not isinstance(item, dict)
            or not isinstance(item.get("b64_json"), str)
            for item in data
        )
    ):
        raise SeedreamProviderPhaseError(
            "SEEDREAM_RESULT_INVALID",
            "Seedream returned an unexpected Base64 image count",
            retryable=False,
            submission_diagnostic_category=(
                ProviderSubmissionDiagnosticCategory
                .HTTP_2XX_IMAGE_MISSING
            ),
        )
    return tuple(_decode_image(item["b64_json"]) for item in data)


def _image(response: dict[str, Any]) -> tuple[bytes, str]:
    """Backward-compatible single-result parser for existing diagnostics."""
    return _images(response, 1)[0]


def _decode_image(encoded: str) -> tuple[bytes, str]:
    try:
        content = base64.b64decode(encoded, validate=True)
    except (ValueError, binascii.Error):
        raise SeedreamProviderPhaseError(
            "SEEDREAM_RESULT_BASE64_INVALID",
            "Seedream returned invalid Base64 image data",
            retryable=False,
            submission_diagnostic_category=(
                ProviderSubmissionDiagnosticCategory
                .HTTP_2XX_IMAGE_BASE64_INVALID
            ),
        ) from None
    if content.startswith(b"\x89PNG\r\n\x1a\n"):
        mime = "image/png"
    elif content.startswith(b"\xff\xd8\xff"):
        mime = "image/jpeg"
    elif content.startswith(b"RIFF") and content[8:12] == b"WEBP":
        mime = "image/webp"
    else:
        raise SeedreamProviderPhaseError(
            "SEEDREAM_RESULT_MIME_INVALID",
            "Seedream returned an unsupported image format",
            retryable=False,
            submission_diagnostic_category=(
                ProviderSubmissionDiagnosticCategory
                .HTTP_2XX_IMAGE_MIME_INVALID
            ),
        )
    if not 1 <= len(content) <= 24 * 1024 * 1024:
        raise SeedreamProviderPhaseError(
            "SEEDREAM_RESULT_SIZE_INVALID",
            "Seedream image size is outside the artifact limit",
            retryable=False,
            submission_diagnostic_category=(
                ProviderSubmissionDiagnosticCategory
                .HTTP_2XX_IMAGE_SIZE_INVALID
            ),
        )
    return content, mime
