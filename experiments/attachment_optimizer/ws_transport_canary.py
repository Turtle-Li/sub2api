#!/usr/bin/env python3
"""Privacy-safe Responses WebSocket transport canary.

The script reads an existing Codex provider configuration and API key, but
never prints either value. Output contains only turn numbers, event types,
timings, close status, and whether the deliberately unique response marker was
observed.
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import json
import ssl
import time
import tomllib
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit

import websockets


TERMINAL_EVENTS = {"response.completed", "response.done", "response.failed"}
IMAGE_MIME_TYPES = {
    ".jpg": "image/jpeg",
    ".jpeg": "image/jpeg",
    ".png": "image/png",
    ".webp": "image/webp",
}


def load_connection(config_path: Path, auth_path: Path, model_override: str | None) -> tuple[str, str, str]:
    config = tomllib.loads(config_path.read_text(encoding="utf-8"))
    provider_name = str(config.get("model_provider", "openai"))
    providers = config.get("model_providers", {})
    provider = providers.get(provider_name, {}) if isinstance(providers, dict) else {}
    base_url = str(provider.get("base_url", "https://api.openai.com/v1")).rstrip("/")
    model = model_override or str(config.get("model", "gpt-5.4"))

    auth = json.loads(auth_path.read_text(encoding="utf-8"))
    api_key = auth.get("OPENAI_API_KEY") or auth.get("openai_api_key")
    if not isinstance(api_key, str) or not api_key.strip():
        raise RuntimeError("existing Codex API-key credential was not available")

    parsed = urlsplit(base_url)
    if parsed.scheme not in {"https", "http"} or not parsed.netloc:
        raise RuntimeError("configured provider base URL is not HTTP(S)")
    path = parsed.path.rstrip("/")
    if not path.endswith("/v1"):
        path += "/v1"
    websocket_url = urlunsplit(("wss" if parsed.scheme == "https" else "ws", parsed.netloc, path + "/responses", "", ""))
    return websocket_url, api_key.strip(), model


def response_id(event: dict[str, object]) -> str | None:
    response = event.get("response")
    if not isinstance(response, dict):
        return None
    value = response.get("id")
    return value if isinstance(value, str) and value else None


def text_delta(event: dict[str, object]) -> str:
    value = event.get("delta")
    if isinstance(value, str):
        return value
    return ""


async def run(args: argparse.Namespace) -> int:
    websocket_url, api_key, model = load_connection(args.config, args.auth, args.model)
    headers = {
        "Authorization": f"Bearer {api_key}",
        "User-Agent": "sub2-attachment-ws-canary/1.0",
    }
    summaries: list[dict[str, object]] = []
    previous_response_id: str | None = None
    close_code: int | None = None
    close_reason = ""
    image_content: dict[str, str] | None = None
    image_bytes = 0
    if args.image:
        raw_image = args.image.read_bytes()
        image_bytes = len(raw_image)
        mime_type = IMAGE_MIME_TYPES[args.image.suffix.lower()]
        image_content = {
            "type": "input_image",
            "image_url": f"data:{mime_type};base64,{base64.b64encode(raw_image).decode('ascii')}",
            "detail": "high",
        }

    ssl_context = ssl.create_default_context() if websocket_url.startswith("wss://") else None
    try:
        async with websockets.connect(
            websocket_url,
            additional_headers=headers,
            open_timeout=args.timeout,
            close_timeout=5,
            max_size=args.max_message_bytes,
            ssl=ssl_context,
        ) as websocket:
            for turn in range(1, args.turns + 1):
                marker = args.expected_marker or f"BRIDGE_WS_OK_{turn}"
                content: list[dict[str, str]] = [
                    {
                        "type": "input_text",
                        "text": args.image_check_prompt
                        if image_content
                        else f"Reply with exactly {marker} and nothing else.",
                    }
                ]
                if image_content:
                    content.append(image_content)
                payload: dict[str, object] = {
                    "type": "response.create",
                    "model": model,
                    "stream": True,
                    "store": False,
                    "input": [
                        {
                            "type": "message",
                            "role": "user",
                            "content": content,
                        }
                    ],
                }
                if previous_response_id:
                    payload["previous_response_id"] = previous_response_id

                started = time.perf_counter()
                await websocket.send(json.dumps(payload, separators=(",", ":")))
                first_event_ms: float | None = None
                terminal_type = ""
                output_parts: list[str] = []
                while True:
                    raw = await asyncio.wait_for(websocket.recv(), timeout=args.timeout)
                    if first_event_ms is None:
                        first_event_ms = (time.perf_counter() - started) * 1000
                    event = json.loads(raw)
                    if not isinstance(event, dict):
                        continue
                    event_type = str(event.get("type", ""))
                    delta = text_delta(event)
                    if delta:
                        output_parts.append(delta)
                    candidate_id = response_id(event)
                    if candidate_id:
                        previous_response_id = candidate_id
                    if event_type in TERMINAL_EVENTS:
                        terminal_type = event_type
                        break

                summaries.append(
                    {
                        "turn": turn,
                        "terminal": terminal_type,
                        "first_event_ms": round(first_event_ms or 0.0, 2),
                        "duration_ms": round((time.perf_counter() - started) * 1000, 2),
                        "marker_observed": marker in "".join(output_parts),
                    }
                )
                if terminal_type == "response.failed":
                    break
            await websocket.close(code=1000, reason="canary complete")
            close_code = websocket.close_code
            close_reason = websocket.close_reason or ""
    except websockets.ConnectionClosed as exc:
        close_code = exc.code
        close_reason = exc.reason or ""
    except Exception as exc:  # output is deliberately scrubbed of URLs/headers
        print(json.dumps({"ok": False, "error_type": type(exc).__name__}, separators=(",", ":")))
        return 1

    ok = (
        len(summaries) == args.turns
        and all(item["terminal"] in {"response.completed", "response.done"} for item in summaries)
        and all(item["marker_observed"] is True for item in summaries)
        and close_code in {None, 1000}
    )
    print(
        json.dumps(
            {
                "ok": ok,
                "image_bytes": image_bytes,
                "turns": summaries,
                "close_code": close_code,
                "close_reason": close_reason,
            },
            separators=(",", ":"),
        )
    )
    return 0 if ok else 1


def parse_args() -> argparse.Namespace:
    home = Path.home()
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, default=home / ".codex" / "config.toml")
    parser.add_argument("--auth", type=Path, default=home / ".codex" / "auth.json")
    parser.add_argument("--model")
    parser.add_argument("--turns", type=int, default=2)
    parser.add_argument("--timeout", type=float, default=90.0)
    parser.add_argument("--max-message-bytes", type=int, default=16 * 1024 * 1024)
    parser.add_argument("--image", type=Path)
    parser.add_argument("--expected-marker")
    parser.add_argument("--image-check-prompt")
    args = parser.parse_args()
    if args.turns < 1 or args.turns > 5:
        parser.error("--turns must be between 1 and 5")
    if args.image:
        if args.turns != 1:
            parser.error("--image requires --turns 1 so every image exercises the first-frame hook")
        if not args.image.is_file():
            parser.error("--image must point to an existing file")
        if args.image.suffix.lower() not in IMAGE_MIME_TYPES:
            parser.error("--image must be PNG, JPEG, or WebP")
        if not args.expected_marker or not args.image_check_prompt:
            parser.error("--image requires --expected-marker and --image-check-prompt")
    elif args.expected_marker or args.image_check_prompt:
        parser.error("--expected-marker and --image-check-prompt require --image")
    return args


if __name__ == "__main__":
    raise SystemExit(asyncio.run(run(parse_args())))
