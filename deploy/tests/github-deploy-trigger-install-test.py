#!/usr/bin/env python3
from pathlib import Path
import unittest


DEPLOY_DIR = Path(__file__).resolve().parents[1]
INSTALLER = DEPLOY_DIR / "install-github-deploy-trigger.sh"
TRIGGER = DEPLOY_DIR / "sub2api-github-deploy-trigger.sh"


class GitHubDeployTriggerInstallTest(unittest.TestCase):
    def test_forced_command_is_installed_outside_private_app_root(self) -> None:
        installer = INSTALLER.read_text(encoding="utf-8")
        self.assertIn(
            "SUB2API_GITHUB_DEPLOY_TRIGGER_PATH:-/usr/local/libexec/"
            "sub2api-github-deploy-trigger",
            installer,
        )
        self.assertIn(
            'install -o root -g root -m 755 "$TRIGGER_SOURCE" "$TRIGGER_SCRIPT"',
            installer,
        )
        self.assertIn(
            'sudo -u "$DEPLOY_USER" -- test -x "$TRIGGER_SCRIPT"',
            installer,
        )
        self.assertIn("command=\"%s\"", installer)

    def test_parser_delegates_private_helper_to_sudo(self) -> None:
        trigger = TRIGGER.read_text(encoding="utf-8")
        self.assertNotIn('[ -f "$IMAGE_RELEASE_SCRIPT" ]', trigger)
        self.assertIn(
            'exec "$SUDO_BIN" -n "$IMAGE_RELEASE_SCRIPT"',
            trigger,
        )


if __name__ == "__main__":
    unittest.main()
