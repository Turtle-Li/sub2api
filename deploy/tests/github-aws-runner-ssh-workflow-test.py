#!/usr/bin/env python3
from pathlib import Path
import unittest


WORKFLOW = (
    Path(__file__).resolve().parents[2]
    / ".github"
    / "workflows"
    / "sub2api-production-deploy.yml"
)


class GitHubAwsRunnerSshWorkflowTest(unittest.TestCase):
    def setUp(self) -> None:
        self.workflow = WORKFLOW.read_text(encoding="utf-8")

    def test_oidc_and_exact_runner_rule_are_scoped_to_aws_candidate(self) -> None:
        self.assertIn("  id-token: write\n", self.workflow)
        self.assertIn(
            "aws-actions/configure-aws-credentials@"
            "e1253824e5c10ff9df46874f81ed3ec929e19cfd # v6.3.0",
            self.workflow,
        )
        condition = (
            "if: ${{ !inputs.build_only && "
            "inputs.deployment_target == 'aws-candidate' }}"
        )
        self.assertGreaterEqual(self.workflow.count(condition), 2)
        self.assertIn("https://checkip.amazonaws.com", self.workflow)
        self.assertIn("ipaddress.ip_address", self.workflow)
        self.assertIn('RUNNER_SSH_CIDR="${RUNNER_IPV4}/32"', self.workflow)
        self.assertNotIn("0.0.0.0/0", self.workflow)

    def test_aws_is_the_only_production_deployment_target(self) -> None:
        self.assertIn(
            "description: 'AWS production environment that owns the target host SSH secrets'",
            self.workflow,
        )
        self.assertIn("          - aws-candidate\n", self.workflow)
        self.assertIn("        default: aws-candidate\n", self.workflow)
        self.assertNotIn("azure-production", self.workflow)

    def test_temporary_rule_is_always_closed_after_upload(self) -> None:
        open_index = self.workflow.index("open-instance-public-ports")
        upload_index = self.workflow.index(
            "Upload image and start verified blue-green release"
        )
        close_index = self.workflow.index("close-instance-public-ports")
        self.assertLess(open_index, upload_index)
        self.assertLess(upload_index, close_index)
        self.assertIn(
            "if: ${{ always() && !inputs.build_only && "
            "inputs.deployment_target == 'aws-candidate' }}",
            self.workflow,
        )
        self.assertIn("Temporary runner SSH CIDR is still open", self.workflow)
        self.assertIn(
            "Runner SSH CIDR was recorded but is not open; no rule to close.",
            self.workflow,
        )
        self.assertIn("timeout-minutes: 35", self.workflow)


if __name__ == "__main__":
    unittest.main()
