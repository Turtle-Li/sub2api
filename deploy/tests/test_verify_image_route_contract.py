#!/usr/bin/env python3

from __future__ import annotations

import sys
import unittest
from pathlib import Path
from typing import Optional


DEPLOY_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(DEPLOY_DIR))

from verify_image_route_contract import verify_document  # noqa: E402


def proxy() -> dict:
    return {
        "handler": "reverse_proxy",
        "upstreams": [{"dial": "sub2api-blue:8080"}],
    }


def route(path: Optional[str], handler: dict, *, terminal: bool = False, host: Optional[str] = None) -> dict:
    value = {"handle": [handler]}
    if path is not None:
        value["match"] = [{"path": [path]}]
    if host is not None:
        value.setdefault("match", [{}])[0]["host"] = [host]
    if terminal:
        value["terminal"] = True
    return value


def fixture(*, explicit: bool = False) -> dict:
    image_paths = [
        "/images/generations",
        "/images/generations/*",
        "/images/edits",
        "/images/edits/*",
        "/images/tasks/*",
        "/v1/*",
    ]
    if explicit:
        api_routes = [
            route(
                "/images/generations /images/generations/* /images/edits /images/edits/* /images/tasks/*".split()[0],
                proxy(),
            )
        ]
        api_routes[0]["match"][0]["path"] = image_paths[:5]
        api_routes.append(route("/v1/*", proxy()))
        api_routes.append(route(None, {"handler": "static_response", "status_code": 404}, terminal=True))
    else:
        api_routes = [route("/v1/responses/*", proxy()), route(None, proxy())]
    api_route = {
        "match": [{"host": ["api.turtleligpt.com"]}],
        "handle": [{"handler": "subroute", "routes": api_routes}],
        "terminal": True,
    }
    return {
        "apps": {
            "http": {
                "servers": {
                    "srv0": {
                        "listen": [":443"],
                        "routes": [
                            route(
                                "/other",
                                {"handler": "static_response", "status_code": 404},
                                terminal=True,
                                host="other.example",
                            ),
                            api_route,
                        ],
                    }
                }
            }
        }
    }


class ImageRouteContractTest(unittest.TestCase):
    def test_accepts_explicit_image_allowlist(self) -> None:
        result = verify_document(fixture(explicit=True))
        self.assertEqual(result["unsupported_policy"], "explicit-allowlist")

    def test_accepts_candidate_catch_all(self) -> None:
        result = verify_document(fixture())
        self.assertEqual(result["unsupported_policy"], "catch-all")

    def test_rejects_missing_root_alias(self) -> None:
        document = fixture(explicit=True)
        paths = document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"][0]["match"][0]["path"]
        paths.remove("/images/edits")
        with self.assertRaisesRegex(ValueError, "does not resolve"):
            verify_document(document)

    def test_rejects_terminal_404_before_image_proxy(self) -> None:
        document = fixture(explicit=True)
        image_routes = document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"]
        image_routes.insert(0, route(None, {"handler": "static_response", "status_code": 404}, terminal=True))
        with self.assertRaisesRegex(ValueError, "does not resolve"):
            verify_document(document)

    def test_rejects_untrusted_application_upstream(self) -> None:
        document = fixture()
        document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"][1]["handle"][0]["upstreams"][0]["dial"] = "untrusted:8080"
        with self.assertRaisesRegex(ValueError, "does not resolve"):
            verify_document(document)

    def test_candidate_rejects_localhost_upstream(self) -> None:
        document = fixture()
        document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"][1]["handle"][0]["upstreams"][0]["dial"] = "localhost:8080"
        with self.assertRaisesRegex(ValueError, "does not resolve"):
            verify_document(document, allow_localhost=False)

    def test_rejects_unknown_matcher_in_front_of_application(self) -> None:
        document = fixture(explicit=True)
        api_routes = document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"]
        api_routes.insert(
            0,
            {
                "match": [{"header": {"X-Route-Selector": ["enabled"]}}],
                "handle": [{"handler": "static_response", "status_code": 404}],
                "terminal": True,
            },
        )
        with self.assertRaisesRegex(ValueError, "unknown route matcher"):
            verify_document(document)

    def test_rejects_same_group_shadow_route(self) -> None:
        document = fixture(explicit=True)
        api_routes = document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"]
        api_routes[0]["group"] = "image-routes"
        api_routes[0]["handle"] = [{"handler": "headers", "response": {"set": {"X-Checked": ["1"]}}}]
        api_routes[1]["group"] = "image-routes"
        with self.assertRaisesRegex(ValueError, "does not resolve"):
            verify_document(document)

    def test_rejects_rewrite_before_application_proxy(self) -> None:
        document = fixture(explicit=True)
        api_routes = document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"]
        api_routes.insert(
            0,
            {
                "match": [{"path": ["/v1/*"]}],
                "handle": [
                    {"handler": "rewrite", "uri": "/rewritten"},
                    proxy(),
                ],
            },
        )
        with self.assertRaisesRegex(ValueError, "does not resolve"):
            verify_document(document)

    def test_rejects_missing_versioned_batch_list_route(self) -> None:
        document = fixture(explicit=True)
        v1_route = document["apps"]["http"]["servers"]["srv0"]["routes"][1]["handle"][0]["routes"][1]
        v1_route["match"][0]["path"] = [
            "/v1/images/generations",
            "/v1/images/generations/*",
            "/v1/images/edits",
            "/v1/images/edits/*",
            "/v1/images/tasks/*",
        ]
        with self.assertRaisesRegex(ValueError, "/v1/images/batches does not resolve"):
            verify_document(document)

    def test_accepts_explicit_ipv4_https_listener(self) -> None:
        document = fixture(explicit=True)
        document["apps"]["http"]["servers"]["srv0"]["listen"] = ["0.0.0.0:443"]
        result = verify_document(document)
        self.assertEqual(result["unsupported_policy"], "explicit-allowlist")

    def test_rejects_non_https_listener_port(self) -> None:
        document = fixture(explicit=True)
        document["apps"]["http"]["servers"]["srv0"]["listen"] = ["0.0.0.0:1443"]
        with self.assertRaisesRegex(ValueError, "listening on :443"):
            verify_document(document)


if __name__ == "__main__":
    unittest.main()
