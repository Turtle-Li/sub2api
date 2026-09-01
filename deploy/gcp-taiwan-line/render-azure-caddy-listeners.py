#!/usr/bin/env python3
"""Render the reviewed Azure Caddy listener and client-IP policy.

The frozen Azure Caddyfile starts with an empty global-options block. This
renderer refuses any non-empty block rather than trying to merge unknown global
policy. It also requires the frozen production API site shape and inserts an
explicit client-IP header policy into both application reverse proxies.
"""

from __future__ import annotations

import argparse
import os
import pathlib
import re
import sys


GCP_INGRESS_IP = "130.211.243.139/32"
API_HOST = "api.turtleligpt.com"
UPSTREAM_PATTERN = r"sub2api(?:-(?:blue|green))?:8080"
PROTECTED_HEADERS = ("X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP")
FORWARDED_HEADER_POLICY = (
    "header_up X-Forwarded-For {remote_host}",
    "header_up -X-Real-IP",
    "header_up -CF-Connecting-IP",
)


def global_options_span(source: str) -> tuple[int, int, str]:
    """Return the leading global-options span and its line ending."""

    if not source.startswith("{"):
        raise ValueError("Caddyfile must begin with its global-options block")

    match = re.match(r"^\{(?P<body>.*?)^\}", source, flags=re.DOTALL | re.MULTILINE)
    if match is None:
        raise ValueError("could not find a complete leading global-options block")
    line_ending = "\r\n" if "\r\n" in match.group(0) else "\n"
    return match.start(), match.end(), line_ending


def staged_global_options(line_ending: str) -> str:
    """Return the exact Caddy global-options block accepted for this cutover."""

    lines = (
        "{",
        "\t# Keep Caddy's automatic :80 redirect listener untouched.",
        "\tservers :443 {",
        "\t\t# h3 is intentionally absent: GCP forwards TCP only, never UDP/443.",
        "\t\tprotocols h1 h2",
        "\t\tlistener_wrappers {",
        "\t\t\tproxy_protocol {",
        "\t\t\t\ttimeout 2s",
        f"\t\t\t\tallow {GCP_INGRESS_IP}",
        "\t\t\t\t# Direct Azure traffic skips PROXY parsing; untrusted PROXY bytes",
        "\t\t\t\t# therefore reach the TLS parser and cannot spoof a client address.",
        "\t\t\t\tfallback_policy skip",
        "\t\t\t}",
        "\t\t\ttls",
        "\t\t}",
        "\t}",
        "}",
    )
    return line_ending.join(lines)


def api_site_span(source: str) -> tuple[int, int, str]:
    """Return the exact production API site span and its line ending."""

    starts = list(
        re.finditer(
            rf"(?m)^{re.escape(API_HOST)} \{{(?P<eol>\r?\n)",
            source,
        )
    )
    if len(starts) != 1:
        raise ValueError(f"expected exactly one {API_HOST} site block")
    start = starts[0]
    closing = re.search(r"(?m)^}\r?(?:\n|$)", source[start.end() :])
    if closing is None:
        raise ValueError(f"could not find the end of the {API_HOST} site block")
    end = start.end() + closing.end()
    return start.start(), end, start.group("eol")


def reverse_proxy_matches(site: str) -> list[re.Match[str]]:
    """Return the two frozen application reverse-proxy block openers."""

    any_directives = list(re.finditer(r"(?m)^[ \t]*reverse_proxy\b[^\r\n]*", site))
    matches = list(
        re.finditer(
            rf"(?m)^(?P<indent>[ \t]+)reverse_proxy "
            rf"(?P<upstream>{UPSTREAM_PATTERN}) \{{(?P<eol>\r?\n)",
            site,
        )
    )
    if len(any_directives) != 2 or len(matches) != 2:
        raise ValueError(
            "production API site must contain exactly two supported reverse_proxy blocks"
        )
    return matches


def protected_header_directives(site: str) -> list[str]:
    """Return request-header directives that touch protected client-IP names."""

    names = "|".join(re.escape(name) for name in PROTECTED_HEADERS)
    return [
        match.group(0)
        for match in re.finditer(
            rf"(?mi)^[ \t]*(?:header_up|request_header)[ \t]+[-+]?"
            rf"(?:{names})(?:[ \t]+[^\r\n]*)?$",
            site,
        )
    ]


def staged_api_site(site: str) -> str:
    """Insert the exact client-IP policy in both reviewed proxy blocks."""

    matches = reverse_proxy_matches(site)
    if protected_header_directives(site):
        raise ValueError("production API site already mutates a protected client-IP header")

    pieces: list[str] = []
    cursor = 0
    for match in matches:
        pieces.append(site[cursor : match.end()])
        indent = match.group("indent") + "\t"
        eol = match.group("eol")
        pieces.append("".join(f"{indent}{line}{eol}" for line in FORWARDED_HEADER_POLICY))
        cursor = match.end()
    pieces.append(site[cursor:])
    return "".join(pieces)


def render(source: str) -> str:
    """Insert the exact accepted listener and client-IP policies."""

    start, end, line_ending = global_options_span(source)
    assert start == 0
    if source[1 : end - 1].strip():
        raise ValueError("leading global-options block is not empty; refusing to merge policy")
    site_start, site_end, _site_line_ending = api_site_span(source)
    site = source[site_start:site_end]
    return (
        staged_global_options(line_ending)
        + source[end:site_start]
        + staged_api_site(site)
        + source[site_end:]
    )


def verify_staged(source: str) -> None:
    """Raise unless both security-sensitive policies match exactly."""

    _start, end, line_ending = global_options_span(source)
    if source[:end] != staged_global_options(line_ending):
        raise ValueError("leading global-options block does not match the frozen listener policy")
    site_start, site_end, _site_line_ending = api_site_span(source)
    site = source[site_start:site_end]
    matches = reverse_proxy_matches(site)
    for match in matches:
        indent = match.group("indent") + "\t"
        eol = match.group("eol")
        expected = "".join(f"{indent}{line}{eol}" for line in FORWARDED_HEADER_POLICY)
        if not site.startswith(expected, match.end()):
            raise ValueError("reverse_proxy client-IP header policy is missing or reordered")
    if len(protected_header_directives(site)) != len(matches) * len(FORWARDED_HEADER_POLICY):
        raise ValueError("production API site has an extra protected client-IP mutation")


def assert_regular_non_symlink(path: pathlib.Path, label: str) -> None:
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"{label} must be a regular non-symlink file: {path}")


def write_destination(destination: pathlib.Path, content: str) -> None:
    if destination.exists() and destination.is_symlink():
        raise ValueError(f"destination must not be a symlink: {destination}")
    descriptor = os.open(
        destination,
        os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW,
        0o600,
    )
    try:
        encoded = content.encode("utf-8")
        written = 0
        while written < len(encoded):
            written += os.write(descriptor, encoded[written:])
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    os.chmod(destination, 0o600)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="command", required=True)

    render_parser = subcommands.add_parser("render")
    render_parser.add_argument("source", type=pathlib.Path)
    render_parser.add_argument("destination", type=pathlib.Path)

    verify_parser = subcommands.add_parser("verify")
    verify_parser.add_argument("source", type=pathlib.Path)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        assert_regular_non_symlink(args.source, "source")
        source = args.source.read_text(encoding="utf-8")
        if args.command == "render":
            write_destination(args.destination, render(source))
        else:
            verify_staged(source)
    except (OSError, UnicodeError, ValueError) as exc:
        print(f"azure-caddy-renderer: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
