#!/usr/bin/env python3
"""Render the narrowly scoped Azure Caddy global listener options.

The frozen Azure Caddyfile starts with an empty global-options block. This
renderer refuses any non-empty block rather than trying to merge unknown global
policy, and it leaves every byte after that closing brace unchanged.
"""

from __future__ import annotations

import argparse
import os
import pathlib
import re
import sys


GCP_INGRESS_IP = "130.211.243.139/32"


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


def render(source: str) -> str:
    """Insert only the accepted global listener policy into an empty block."""

    start, end, line_ending = global_options_span(source)
    assert start == 0
    if source[1 : end - 1].strip():
        raise ValueError("leading global-options block is not empty; refusing to merge policy")
    return staged_global_options(line_ending) + source[end:]


def verify_staged(source: str) -> None:
    """Raise ValueError unless the Caddyfile has the exact staged global block."""

    _start, end, line_ending = global_options_span(source)
    if source[:end] != staged_global_options(line_ending):
        raise ValueError("leading global-options block does not match the frozen listener policy")


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
