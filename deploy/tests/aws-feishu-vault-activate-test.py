#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest

SCRIPT = (
    Path(__file__).resolve().parents[1]
    / "aws-candidate"
    / "feishu-vault-activate.py"
)
SPEC = importlib.util.spec_from_file_location("aws_feishu_activate", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
WEBHOOK = "https://open.feishu.cn/open-apis/bot/v2/hook/11111111-1111-1111-1111-111111111111"


class ActivationTests(unittest.TestCase):
    def test_only_stdin_contains_secret_and_aws_target_is_pinned(self):
        environment = {MODULE.SECRET_ENV: WEBHOOK, "PATH": "/usr/bin:/bin"}
        calls = []

        def run(args, **kwargs):
            calls.append((args, kwargs))
            return SimpleNamespace(returncode=0, stdout=MODULE.ACK + b"\n")

        self.assertTrue(MODULE.activate(environment, run))
        args, kwargs = calls[0]
        self.assertNotIn(MODULE.SECRET_ENV, environment)
        self.assertNotIn(MODULE.SECRET_ENV, kwargs["env"])
        self.assertNotIn(WEBHOOK, repr(args))
        self.assertEqual(kwargs["input"], WEBHOOK.encode())
        self.assertEqual(args[0], "/usr/bin/ssh")
        self.assertEqual(args[-2], "sub2api-aws-candidate")
        self.assertIn(MODULE.REFERENCE, args[-1])

    def test_unsafe_urls_never_reach_ssh(self):
        for url in (
            "",
            WEBHOOK + "?x=1",
            WEBHOOK.replace("https:", "http:"),
            WEBHOOK.replace("open.feishu.cn", "open.feishu.cn.evil"),
            WEBHOOK.replace("open.feishu.cn", "user@open.feishu.cn"),
            WEBHOOK + "\n",
        ):
            with self.subTest(url=url):
                self.assertFalse(
                    MODULE.activate(
                        {MODULE.SECRET_ENV: url},
                        lambda *args, **kwargs: self.fail("invalid URL reached SSH"),
                    )
                )

    def test_timeout_and_bad_ack_fail_closed(self):
        def timeout(*args, **kwargs):
            raise subprocess.TimeoutExpired("redacted", 45)

        self.assertFalse(MODULE.activate({MODULE.SECRET_ENV: WEBHOOK}, timeout))
        self.assertFalse(
            MODULE.activate(
                {MODULE.SECRET_ENV: WEBHOOK},
                lambda *args, **kwargs: SimpleNamespace(
                    returncode=0, stdout=b"unexpected\n"
                ),
            )
        )


if __name__ == "__main__":
    unittest.main()
