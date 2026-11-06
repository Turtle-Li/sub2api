from __future__ import annotations
import base64
import json
import socket
import threading
from contextlib import contextmanager
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Iterator
import pytest
from sub2_image_bridge.seedream import SEEDREAM_ENDPOINT, SEEDREAM_PATH
from sub2_image_bridge.contract import Request, execute
from test_contract import payload, PNG

@dataclass
class _ServerState:
    mode: str
    requests: list[dict[str, Any]]


def _handler_for(state: _ServerState):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, _format: str, *_args: Any) -> None:
            return

        def do_POST(self) -> None:  # noqa: N802 - stdlib handler contract
            length = int(self.headers.get("Content-Length", "0"))
            raw = self.rfile.read(length)
            state.requests.append({
                "path": self.path,
                "headers": {key.lower(): value for key, value in self.headers.items()},
                "body": raw,
            })
            if state.mode == "drop":
                # Model a connection that dies after the request has crossed
                # the socket boundary.  The caller cannot infer whether Ark
                # accepted the paid request, so it must checkpoint UNKNOWN.
                self.close_connection = True
                try:
                    self.connection.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                self.connection.close()
                return

            request_body = json.loads(raw)
            count = int(
                request_body.get(
                    "sequential_image_generation_options", {}
                ).get("max_images", 1)
            )
            response = json.dumps({
                "data": [
                    {
                        "b64_json": base64.b64encode(PNG).decode("ascii"),
                    }
                    for _ in range(count)
                ],
            }).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(response)))
            self.send_header("Connection", "close")
            self.send_header("x-request-id", "ark-loopback-request")
            self.end_headers()
            self.wfile.write(response)

    return Handler


@contextmanager
def _fake_ark(mode: str) -> Iterator[tuple[str, _ServerState]]:
    state = _ServerState(mode=mode, requests=[])
    server = ThreadingHTTPServer(("127.0.0.1", 0), _handler_for(state))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        host, port = server.server_address
        yield f"http://{host}:{port}", state
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


class _LoopbackSession:
    """Use requests' real socket stack while preserving the frozen Ark URL."""

    def __init__(self, origin: str) -> None:
        import requests

        self._session = requests.Session()
        self.origin = origin
        self.seen_urls: list[str] = []

    def post(self, url: str, **kwargs: Any):
        self.seen_urls.append(url)
        return self._session.post(
            self.origin + SEEDREAM_PATH,
            **kwargs,
        )

    def close(self) -> None:
        self._session.close()


@pytest.mark.parametrize("mode", ["success", "drop"])
def test_original_socket_fixture_through_bridge(mode):
    with _fake_ark(mode) as (origin, state):
        session = _LoopbackSession(origin)
        try:
            result = execute(Request.parse(payload()), session)
        finally:
            session.close()
    assert len(state.requests) == 1
    assert session.seen_urls == [SEEDREAM_ENDPOINT + SEEDREAM_PATH]
    assert state.requests[0]["path"] == SEEDREAM_PATH
    assert state.requests[0]["headers"]["x-client-request-id"] == "test-123"
    assert result["state"] == ("completed" if mode == "success" else "outcome_unknown")
    if mode == "success":
        assert base64.b64decode(result["images"][0]["b64_json"]) == PNG
