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
WanProviderCredential = Credential
WAN_MODELS = frozenset({"wan2.7-image", "wan2.7-image-pro"})
WAN_SUBMIT_PATH = "/services/aigc/image-generation/generation"
_WORKSPACE_HOST = re.compile(
    r"ws-[a-z0-9]{8,40}\.cn-beijing\.maas\.aliyuncs\.com"
)
_TASK_ID = re.compile(r"[A-Za-z0-9-]{8,100}")
_PENDING = frozenset({"PENDING", "RUNNING"})
_SUCCESS = "SUCCEEDED"
_FAILED = frozenset({"FAILED", "UNKNOWN"})
_CANCELED = "CANCELED"
_MAX_IMAGE_BYTES = 24 * 1024 * 1024
_WAN_ONE_K_SIZES = frozenset({
    "1024*1024", "683*1024", "1024*683", "768*1024",
    "1024*768", "819*1024", "1024*819", "576*1024",
    "1024*576",
})
_WAN_TWO_K_SIZES = frozenset({
    "2048*2048", "1365*2048", "2048*1365", "1536*2048",
    "2048*1536", "1638*2048", "2048*1638", "1152*2048",
    "2048*1152",
})


class WanProviderPhaseError(ProviderPhaseAdapterError):
    def __init__(
        self,
        code: str,
        message: str,
        *,
        retryable: bool,
        outcome_unknown: bool = False,
    ):
        super().__init__(message, retryable=retryable)
        self.code = (
            code if re.fullmatch(r"[A-Z0-9_]{2,80}", code)
            else "WAN_PROVIDER_ERROR"
        )
        self.outcome_unknown = outcome_unknown

def _approved_wan_size(profile_version: object, size: object) -> bool:
    version = str(profile_version or "").strip()
    value = str(size or "").strip()
    if version == "1":
        return value in {"1K", "2K"}
    return version == "2" and (
        value in {"1K", "2K"}
        or value in _WAN_ONE_K_SIZES
        or value in _WAN_TWO_K_SIZES
    )


def _approved_wan_content(value: Any) -> bool:
    if not isinstance(value, list) or not 2 <= len(value) <= 10:
        return False
    images = value[:-1]
    text = value[-1]
    if (
        not 1 <= len(images) <= 9
        or not isinstance(text, dict)
        or set(text) != {"text"}
        or not isinstance(text.get("text"), str)
        or not text["text"].strip()
        or len(text["text"]) > 6_000
    ):
        return False
    for image in images:
        if (
            not isinstance(image, dict)
            or set(image) != {"image"}
            or not isinstance(image.get("image"), str)
            or re.fullmatch(
                r"data:image/(?:png|jpeg|webp);base64,[A-Za-z0-9+/=]+",
                image["image"],
            )
            is None
        ):
            return False
    return True


class WanPhaseHTTPClient:
    def __init__(
        self,
        credential: WanProviderCredential,
        *,
        session: requests.Session | None = None,
    ):
        self.credential = credential
        self.api_base = credential.endpoint_url.rstrip("/")
        self.session = session or requests.Session()
        self._guard: Callable[[], None] = lambda: None

    def bind_action_guard(self, guard: Callable[[], None]) -> None:
        self._guard = guard

    def submit(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._json(
            "POST",
            WAN_SUBMIT_PATH,
            body=body,
            async_header=True,
            outcome_unknown=True,
        )

    def status(self, task_id: str) -> dict[str, Any]:
        return self._json(
            "GET",
            f"/tasks/{quote(task_id, safe='')}",
        )

    def cancel(self, task_id: str) -> dict[str, Any]:
        return self._json(
            "POST",
            f"/tasks/{quote(task_id, safe='')}/cancel",
        )

    def image(self, url: str) -> tuple[str, bytes]:
        parsed = urlsplit(url)
        if (
            parsed.scheme != "https"
            or not (parsed.hostname or "").lower().endswith(
                ".aliyuncs.com"
            )
            or parsed.username is not None
            or parsed.password is not None
        ):
            raise WanProviderPhaseError(
                "WAN_RESULT_URL_INVALID",
                "Wan returned an unapproved result URL",
                retryable=False,
            )
        self._guard()
        try:
            response = self.session.get(
                url,
                headers={
                    "Accept": "image/png,image/jpeg,image/webp",
                    "User-Agent": "vso-provider-phase/0.4.0",
                },
                timeout=self.credential.timeout_seconds,
                stream=True,
                allow_redirects=False,
            )
        except requests.RequestException:
            raise WanProviderPhaseError(
                "WAN_RESULT_TRANSPORT_ERROR",
                "Wan result download failed",
                retryable=True,
            ) from None
        try:
            if response.status_code != 200:
                raise WanProviderPhaseError(
                    "WAN_RESULT_HTTP_ERROR",
                    "Wan result download was rejected",
                    retryable=response.status_code >= 500,
                )
            mime = str(
                response.headers.get("Content-Type") or ""
            ).split(";", 1)[0].lower()
            if mime not in {"image/png", "image/jpeg", "image/webp"}:
                raise WanProviderPhaseError(
                    "WAN_RESULT_MIME_INVALID",
                    "Wan result MIME type is invalid",
                    retryable=False,
                )
            chunks: list[bytes] = []
            size = 0
            for chunk in response.iter_content(1024 * 1024):
                self._guard()
                if not chunk:
                    continue
                size += len(chunk)
                if size > _MAX_IMAGE_BYTES:
                    raise WanProviderPhaseError(
                        "WAN_RESULT_TOO_LARGE",
                        "Wan result exceeds the artifact limit",
                        retryable=False,
                    )
                chunks.append(bytes(chunk))
            content = b"".join(chunks)
            if not content:
                raise WanProviderPhaseError(
                    "WAN_RESULT_EMPTY",
                    "Wan returned an empty image",
                    retryable=True,
                )
            return mime, content
        finally:
            response.close()

    def _json(
        self,
        method: str,
        path: str,
        *,
        body: dict[str, Any] | None = None,
        async_header: bool = False,
        outcome_unknown: bool = False,
    ) -> dict[str, Any]:
        self._guard()
        headers = {
            "Authorization": f"Bearer {self.credential.api_key}",
            "Accept": "application/json",
            "User-Agent": "vso-provider-phase/0.4.0",
        }
        if body is not None:
            headers["Content-Type"] = "application/json"
        if async_header:
            headers["X-DashScope-Async"] = "enable"
        try:
            response = self.session.request(
                method,
                f"{self.api_base}{path}",
                headers=headers,
                json=body,
                timeout=self.credential.timeout_seconds,
                allow_redirects=False,
            )
        except requests.RequestException:
            raise WanProviderPhaseError(
                "WAN_SUBMIT_OUTCOME_UNKNOWN"
                if outcome_unknown else "WAN_TRANSPORT_ERROR",
                "Wan transport failed",
                retryable=True,
                outcome_unknown=outcome_unknown,
            ) from None
        try:
            try:
                value = response.json()
            except ValueError:
                value = {}
            if not 200 <= response.status_code < 300:
                code = str(value.get("code") or "WAN_HTTP_ERROR")
                fixed = re.sub(r"[^A-Z0-9_]", "_", code.upper())[:80]
                raise WanProviderPhaseError(
                    fixed,
                    "Wan request was rejected",
                    retryable=response.status_code >= 500,
                    outcome_unknown=outcome_unknown
                    and response.status_code >= 500,
                )
            if not isinstance(value, dict):
                raise WanProviderPhaseError(
                    "WAN_RESPONSE_INVALID",
                    "Wan returned invalid JSON",
                    retryable=True,
                )
            return value
        finally:
            response.close()


def _status(response: dict[str, Any]) -> str:
    output = response.get("output")
    value = str(
        output.get("task_status") if isinstance(output, dict) else ""
    ).upper()
    return value


def _require_task(response: dict[str, Any], task_id: str) -> None:
    output = response.get("output")
    if (
        not isinstance(output, dict)
        or str(output.get("task_id") or "") != task_id
    ):
        raise WanProviderPhaseError(
            "WAN_TASK_ID_MISMATCH",
            "Wan response task id does not match the frozen task",
            retryable=False,
        )


def _image_urls(response: dict[str, Any]) -> tuple[str, ...]:
    output = response.get("output")
    choices = output.get("choices") if isinstance(output, dict) else None
    urls: list[str] = []
    if isinstance(choices, list):
        for choice in choices:
            content = (
                (choice.get("message") or {}).get("content")
                if isinstance(choice, dict)
                else None
            )
            if not isinstance(content, list):
                continue
            for item in content:
                if (
                    isinstance(item, dict)
                    and item.get("type") == "image"
                    and isinstance(item.get("image"), str)
                ):
                    urls.append(item["image"])
    return tuple(urls)

