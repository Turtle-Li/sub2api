#!/usr/bin/env python3
"""Download a Sub2 batch-image result without proxying media through Sub2."""

from __future__ import annotations

import argparse
import base64
import binascii
import json
import os
import re
import shutil
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from pathlib import Path
from typing import Any, Callable


MAX_CONTROL_BYTES = 2 * 1024 * 1024
MAX_ARCHIVE_FILES = 20
MAX_JSONL_LINE_BYTES = 96 * 1024 * 1024
MAX_IMAGE_BYTES = 64 * 1024 * 1024
COS_HOST = "image-1309919944.cos.ap-shanghai.myqcloud.com"


class SafeError(Exception):
    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code


class APIError(SafeError):
    def __init__(self, status: int, code: str, message: str):
        super().__init__(code, message)
        self.status = status


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: ANN001
        return None


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Download and locally decode a private-COS Sub2 batch-image result."
    )
    parser.add_argument("--base-url", default=os.environ.get("SUB2API_BASE_URL", ""))
    parser.add_argument("--batch-id", required=True)
    parser.add_argument(
        "--output-dir",
        required=True,
        help="Parent directory; a new <batch-id> subdirectory is created.",
    )
    return parser.parse_args()


def validate_base_url(raw: str) -> str:
    value = raw.strip().rstrip("/")
    try:
        parsed = urllib.parse.urlsplit(value)
    except ValueError as exc:
        raise SafeError("BASE_URL_INVALID", "Sub2 API base URL is invalid.") from exc
    loopback = parsed.hostname in {"127.0.0.1", "localhost", "::1"}
    if (
        not parsed.hostname
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
        or (parsed.scheme != "https" and not (parsed.scheme == "http" and loopback))
    ):
        raise SafeError("BASE_URL_INVALID", "Sub2 API base URL must be HTTPS.")
    return value


def validate_batch_id(value: str) -> str:
    batch_id = value.strip()
    if not re.fullmatch(r"imgbatch_[a-f0-9]{32}", batch_id):
        raise SafeError("BATCH_ID_INVALID", "Batch ID is invalid.")
    return batch_id


def read_limited(response, maximum: int) -> bytes:  # noqa: ANN001
    body = response.read(maximum + 1)
    if len(body) > maximum:
        raise SafeError("CONTROL_RESPONSE_TOO_LARGE", "Sub2 returned an oversized response.")
    return body


def api_request(
    opener: urllib.request.OpenerDirector,
    base_url: str,
    api_key: str,
    path: str,
    *,
    expect_json: bool = True,
):
    request = urllib.request.Request(
        f"{base_url}{path}",
        headers={
            "Authorization": f"Bearer {api_key}",
            "Accept": "application/json" if expect_json else "application/zip",
            "User-Agent": "sub2api-batch-image-skill/1",
        },
        method="GET",
    )
    try:
        response = opener.open(request, timeout=120)
    except urllib.error.HTTPError as exc:
        raw = read_limited(exc, MAX_CONTROL_BYTES)
        code = f"HTTP_{exc.code}"
        message = f"Sub2 API request failed with HTTP {exc.code}."
        try:
            payload = json.loads(raw)
            code = str(payload.get("error", {}).get("code") or payload.get("code") or code)
            message = str(
                payload.get("error", {}).get("message")
                or payload.get("message")
                or message
            )
        except (ValueError, TypeError):
            pass
        raise APIError(exc.code, code, message) from None
    except (urllib.error.URLError, TimeoutError, OSError):
        raise SafeError("SUB2_NETWORK_FAILED", "Could not reach the Sub2 API.") from None
    if not expect_json:
        return response
    with response:
        raw = read_limited(response, MAX_CONTROL_BYTES)
    try:
        payload = json.loads(raw)
    except (ValueError, TypeError):
        raise SafeError("CONTROL_JSON_INVALID", "Sub2 returned invalid JSON.") from None
    if not isinstance(payload, dict):
        raise SafeError("CONTROL_JSON_INVALID", "Sub2 returned invalid JSON.")
    return payload


def capability_list(payload: dict[str, Any]) -> list[dict[str, Any]]:
    raw_files = payload.get("data")
    if not isinstance(raw_files, list) or not 1 <= len(raw_files) <= MAX_ARCHIVE_FILES:
        raise SafeError("ARCHIVE_CAPABILITIES_INVALID", "Result archive file list is invalid.")
    files: list[dict[str, Any]] = []
    for position, raw in enumerate(sorted(raw_files, key=lambda value: value.get("index", -1))):
        if not isinstance(raw, dict):
            raise SafeError("ARCHIVE_CAPABILITIES_INVALID", "Result archive file list is invalid.")
        try:
            index = int(raw["index"])
            size = int(raw["size"])
            url = str(raw["url"])
        except (KeyError, TypeError, ValueError):
            raise SafeError("ARCHIVE_CAPABILITIES_INVALID", "Result archive file list is invalid.") from None
        try:
            parsed = urllib.parse.urlsplit(url)
        except ValueError:
            raise SafeError("ARCHIVE_CAPABILITIES_INVALID", "Result archive URL is invalid.") from None
        if (
            index != position
            or size <= 0
            or size > 0xFFFFFFFF
            or parsed.scheme != "https"
            or not parsed.hostname
            or parsed.hostname.lower() != COS_HOST
            or parsed.username
            or parsed.password
            or parsed.fragment
            or not parsed.query
        ):
            raise SafeError("ARCHIVE_CAPABILITIES_INVALID", "Result archive capability is invalid.")
        files.append({"index": index, "size": size, "url": url})
    return files


def record(value: Any) -> dict[str, Any] | None:
    return value if isinstance(value, dict) else None


def text(value: Any) -> str:
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return str(value)
    return ""


def first_text(*values: Any) -> str:
    return next((resolved for value in values if (resolved := text(value))), "")


def nested(value: Any, *keys: str) -> Any:
    current = value
    for key in keys:
        if not isinstance(current, dict):
            return None
        current = current.get(key)
    return current


def extension_for_mime(value: Any) -> tuple[str, str]:
    mime = text(value).split(";", 1)[0].lower()
    if mime == "image/jpg":
        mime = "image/jpeg"
    extensions = {
        "image/png": "png",
        "image/jpeg": "jpg",
        "image/webp": "webp",
        "image/gif": "gif",
    }
    return mime, extensions.get(mime, "")


def images_from_candidates(raw: Any) -> list[dict[str, str]]:
    if not isinstance(raw, list):
        return []
    images: list[dict[str, str]] = []
    for candidate in raw:
        parts = nested(candidate, "content", "parts")
        if not isinstance(parts, list):
            continue
        for part in parts:
            if not isinstance(part, dict):
                continue
            inline = record(part.get("inlineData")) or record(part.get("inline_data"))
            if not inline:
                continue
            data = text(inline.get("data"))
            mime, extension = extension_for_mime(
                inline.get("mimeType", inline.get("mime_type"))
            )
            if data and extension:
                images.append(
                    {
                        "data": data,
                        "mime_type": mime,
                        "extension": extension,
                    }
                )
    return images


def provider_failure(obj: dict[str, Any]) -> tuple[str, str] | None:
    failure = (
        record(obj.get("status"))
        or record(obj.get("error"))
        or record(nested(obj, "response", "error"))
    )
    if not failure:
        return None
    raw_code = first_text(failure.get("code"), failure.get("status"))
    message = first_text(failure.get("message"), failure.get("details"))
    message = (message or "provider returned an item error")[:500]
    normalized = f"{raw_code} {message}".lower()
    if any(word in normalized for word in ("safety", "policy", "blocked", "prohibited")):
        code = "SAFETY_BLOCKED"
    elif any(word in normalized for word in ("invalid_argument", "invalid argument", "bad request")):
        code = "INVALID_ARGUMENT"
    elif any(word in normalized for word in ("quota", "rate", "resource_exhausted", "too many requests")):
        code = "PROVIDER_RATE_LIMITED"
    else:
        code = "PROVIDER_ITEM_FAILED"
    return code, message


def parse_result_line(raw: bytes) -> dict[str, Any]:
    try:
        obj = json.loads(raw)
    except (ValueError, TypeError):
        raise SafeError("ARCHIVE_JSONL_INVALID", "Result archive contains invalid JSONL.") from None
    if not isinstance(obj, dict):
        raise SafeError("ARCHIVE_JSONL_INVALID", "Result archive contains invalid JSONL.")
    request = record(obj.get("request")) or {}
    instance = record(obj.get("instance")) or {}
    custom_id = first_text(
        obj.get("key"),
        obj.get("custom_id"),
        obj.get("customId"),
        request.get("key"),
        request.get("custom_id"),
        request.get("customId"),
        instance.get("key"),
        instance.get("custom_id"),
        instance.get("customId"),
    )
    if not custom_id:
        raise SafeError("ARCHIVE_CUSTOM_ID_MISSING", "A result line has no item ID.")
    images = images_from_candidates(nested(obj, "response", "candidates"))
    images.extend(images_from_candidates(obj.get("candidates")))
    if images:
        return {"custom_id": custom_id, "status": "success", "images": images, "error": None}
    failure = provider_failure(obj)
    has_response = "response" in obj or "candidates" in obj
    if failure:
        code, message = failure
    elif has_response:
        code, message = "EMPTY_IMAGE_OUTPUT", "provider response contained no image output"
    else:
        code, message = "PROVIDER_ITEM_FAILED", "provider result line contained no image output"
    return {
        "custom_id": custom_id,
        "status": "failed",
        "images": [],
        "error": {"code": code, "message": message},
    }


def decode_image(image: dict[str, str]) -> bytes:
    compact = re.sub(r"\s+", "", image["data"])
    if not compact or len(compact) > ((MAX_IMAGE_BYTES + 2) // 3) * 4 + 4:
        raise SafeError("ARCHIVE_IMAGE_TOO_LARGE", "A generated image is too large.")
    try:
        data = base64.b64decode(compact, validate=True)
    except (binascii.Error, ValueError):
        raise SafeError("ARCHIVE_IMAGE_BASE64_INVALID", "A generated image has invalid Base64.") from None
    if not 0 < len(data) <= MAX_IMAGE_BYTES:
        raise SafeError("ARCHIVE_IMAGE_TOO_LARGE", "A generated image is too large.")
    mime = image["mime_type"]
    valid = {
        "image/png": data.startswith(b"\x89PNG\r\n\x1a\n"),
        "image/jpeg": data.startswith(b"\xff\xd8\xff"),
        "image/webp": len(data) >= 12 and data[:4] == b"RIFF" and data[8:12] == b"WEBP",
        "image/gif": data.startswith((b"GIF87a", b"GIF89a")),
    }.get(mime, False)
    if not valid:
        raise SafeError("ARCHIVE_IMAGE_SIGNATURE_INVALID", "A generated image has an invalid signature.")
    return data


def safe_filename_base(value: str) -> str:
    cleaned = re.sub(r'[\x00-\x1f\x7f/\\:*?"<>|]+', "_", value).lstrip(".").strip()
    return (cleaned[:120] or "image")


def unique_filename(custom_id: str, image_index: int, extension: str, used: set[str]) -> str:
    suffix = f"_{image_index + 1}" if image_index else ""
    base = f"{safe_filename_base(custom_id)}{suffix}"
    candidate = f"images/{base}.{extension}"
    counter = 2
    while candidate in used:
        candidate = f"images/{base}_{counter}.{extension}"
        counter += 1
    used.add(candidate)
    return candidate


def open_cos_response(
    opener: urllib.request.OpenerDirector,
    capability_provider: Callable[[], list[dict[str, Any]]],
    files: list[dict[str, Any]],
    position: int,
):
    for attempt in range(2):
        file = files[position]
        request = urllib.request.Request(
            file["url"],
            headers={
                "Accept": "application/x-ndjson, application/json",
                "User-Agent": "sub2api-batch-image-skill/1",
            },
            method="GET",
        )
        try:
            return opener.open(request, timeout=120), file, files
        except urllib.error.HTTPError as exc:
            if exc.code in (401, 403) and attempt == 0:
                files = capability_provider()
                continue
            raise SafeError("ARCHIVE_DOWNLOAD_FAILED", "Private COS result download failed.") from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise SafeError("ARCHIVE_NETWORK_FAILED", "Could not download the private COS result.") from None
    raise SafeError("ARCHIVE_DOWNLOAD_FAILED", "Private COS result download failed.")


def extract_archive(
    opener: urllib.request.OpenerDirector,
    capability_provider: Callable[[], list[dict[str, Any]]],
    expected_ids: list[str],
    job: dict[str, Any],
    stage: Path,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    expected = set(expected_ids)
    files = capability_provider()
    seen: set[str] = set()
    used_names: set[str] = set()
    manifest_files: list[dict[str, Any]] = []
    errors: list[dict[str, Any]] = []
    parsed_items: list[dict[str, Any]] = []
    images_dir = stage / "images"
    images_dir.mkdir()

    for position in range(len(files)):
        response, capability, files = open_cos_response(
            opener, capability_provider, files, position
        )
        received = 0
        with response:
            declared = response.headers.get("Content-Length", "")
            if declared.isdigit() and int(declared) != capability["size"]:
                raise SafeError("ARCHIVE_SIZE_MISMATCH", "Private COS result size mismatch.")
            while True:
                raw = response.readline(MAX_JSONL_LINE_BYTES + 1)
                if not raw:
                    break
                received += len(raw)
                if len(raw) > MAX_JSONL_LINE_BYTES or received > capability["size"]:
                    raise SafeError("ARCHIVE_SIZE_MISMATCH", "Private COS result size mismatch.")
                if not raw.strip():
                    continue
                parsed = parse_result_line(raw)
                custom_id = parsed["custom_id"]
                if custom_id in seen:
                    raise SafeError("ARCHIVE_DUPLICATE_ITEM", "Result archive has a duplicate item ID.")
                if custom_id not in expected:
                    raise SafeError("ARCHIVE_UNKNOWN_ITEM", "Result archive has an unexpected item ID.")
                seen.add(custom_id)
                parsed_items.append(parsed)
                if parsed["status"] == "failed":
                    errors.append({"custom_id": custom_id, **parsed["error"]})
                    continue
                for image_index, image in enumerate(parsed["images"]):
                    filename = unique_filename(
                        custom_id, image_index, image["extension"], used_names
                    )
                    data = decode_image(image)
                    (stage / filename).write_bytes(data)
                    manifest_files.append(
                        {
                            "custom_id": custom_id,
                            "filename": filename,
                            "mime_type": image["mime_type"],
                            "image_index": image_index,
                        }
                    )
        if received != capability["size"]:
            raise SafeError("ARCHIVE_SIZE_MISMATCH", "Private COS result size mismatch.")

    for custom_id in expected_ids:
        if custom_id in seen:
            continue
        missing = {
            "custom_id": custom_id,
            "status": "failed",
            "images": [],
            "error": {
                "code": "RESULT_MISSING",
                "message": "provider result was not found for item",
            },
        }
        parsed_items.append(missing)
        errors.append({"custom_id": custom_id, **missing["error"]})
        seen.add(custom_id)

    item_count = int(job.get("item_count", -1))
    success_count = sum(item["status"] == "success" for item in parsed_items)
    fail_count = len(parsed_items) - success_count
    if (
        len(expected) != item_count
        or len(seen) != item_count
        or success_count != int(job.get("success_count", -1))
        or fail_count != int(job.get("fail_count", -1))
    ):
        raise SafeError(
            "ARCHIVE_COMPLETION_MISMATCH",
            "Result archive does not match Vertex completion statistics.",
        )

    manifest = {
        "batch_id": job.get("id"),
        "model": job.get("model"),
        "item_count": item_count,
        "success_count": success_count,
        "fail_count": fail_count,
        "files": manifest_files,
    }
    (stage / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    (stage / "errors.json").write_text(
        json.dumps(errors, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return manifest_files, errors


def write_zip(stage: Path, batch_id: str, manifest_files: list[dict[str, Any]]) -> None:
    zip_path = stage / f"{batch_id}.zip"
    with zipfile.ZipFile(zip_path, "x", compression=zipfile.ZIP_STORED) as archive:
        for item in manifest_files:
            archive.write(stage / item["filename"], item["filename"])
        archive.write(stage / "manifest.json", "manifest.json")
        archive.write(stage / "errors.json", "errors.json")


def download_legacy_zip(response, stage: Path, batch_id: str) -> None:  # noqa: ANN001
    target = stage / f"{batch_id}.zip"
    written = 0
    with response, target.open("xb") as output:
        while chunk := response.read(1024 * 1024):
            written += len(chunk)
            if written > 0xFFFFFFFF:
                raise SafeError("LEGACY_ZIP_TOO_LARGE", "Legacy ZIP is too large.")
            output.write(chunk)
    if written == 0:
        raise SafeError("LEGACY_ZIP_EMPTY", "Legacy ZIP download was empty.")


def run() -> Path:
    args = parse_args()
    base_url = validate_base_url(args.base_url)
    batch_id = validate_batch_id(args.batch_id)
    api_key = os.environ.get("SUB2API_API_KEY", "").strip()
    if not api_key:
        raise SafeError(
            "API_KEY_MISSING",
            "Set SUB2API_API_KEY in the process environment; do not pass it on the command line.",
        )

    root = Path(args.output_dir).expanduser().resolve()
    root.mkdir(parents=True, exist_ok=True)
    target = root / batch_id
    if target.exists():
        raise SafeError(
            "OUTPUT_EXISTS",
            "The batch output directory already exists; choose another parent directory.",
        )

    opener = urllib.request.build_opener(NoRedirect)
    job = api_request(opener, base_url, api_key, f"/v1/images/batches/{batch_id}")
    if job.get("status") != "completed":
        raise SafeError("BATCH_NOT_COMPLETE", "Batch result is not ready to download.")
    items_payload = api_request(
        opener,
        base_url,
        api_key,
        f"/v1/images/batches/{batch_id}/items?limit=500",
    )
    raw_items = items_payload.get("data")
    if not isinstance(raw_items, list):
        raise SafeError("ITEM_LIST_INVALID", "Sub2 returned an invalid item list.")
    expected_ids = [text(item.get("custom_id")) for item in raw_items if isinstance(item, dict)]
    if len(expected_ids) != len(raw_items) or len(set(expected_ids)) != len(expected_ids):
        raise SafeError("ITEM_LIST_INVALID", "Sub2 returned an invalid item list.")

    def capabilities() -> list[dict[str, Any]]:
        payload = api_request(
            opener,
            base_url,
            api_key,
            f"/v1/images/batches/{batch_id}/result-files",
        )
        return capability_list(payload)

    with tempfile.TemporaryDirectory(prefix=f".{batch_id}-", dir=root) as temp_name:
        stage = Path(temp_name)
        try:
            manifest_files, _ = extract_archive(
                opener, capabilities, expected_ids, job, stage
            )
            write_zip(stage, batch_id, manifest_files)
        except APIError as exc:
            if exc.code != "BATCH_IMAGE_RESULT_ARCHIVE_UNAVAILABLE":
                raise
            for child in stage.iterdir():
                if child.is_dir():
                    shutil.rmtree(child)
                else:
                    child.unlink()
            response = api_request(
                opener,
                base_url,
                api_key,
                f"/v1/images/batches/{batch_id}/download",
                expect_json=False,
            )
            download_legacy_zip(response, stage, batch_id)
        os.replace(stage, target)
    return target


def main() -> int:
    try:
        target = run()
    except SafeError as exc:
        print(f"error [{exc.code}]: {exc}", file=sys.stderr)
        return 1
    except Exception:
        print("error [UNEXPECTED]: batch result download failed safely", file=sys.stderr)
        return 1
    print(target)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
