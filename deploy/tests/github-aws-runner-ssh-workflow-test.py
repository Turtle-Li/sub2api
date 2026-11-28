#!/usr/bin/env python3
import os
from pathlib import Path
import re
import subprocess
from tempfile import TemporaryDirectory
from textwrap import dedent
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

    def test_workflow_never_mutates_lightsail_firewall(self) -> None:
        self.assertNotIn("  id-token: write\n", self.workflow)
        self.assertNotIn("aws-actions/configure-aws-credentials@", self.workflow)
        self.assertNotIn("checkip.amazonaws.com", self.workflow)
        self.assertNotIn("open-instance-public-ports", self.workflow)
        self.assertNotIn("close-instance-public-ports", self.workflow)
        self.assertNotIn("SUB2API_RUNNER_SSH_CIDR", self.workflow)

    def test_aws_is_the_only_production_deployment_target(self) -> None:
        self.assertIn(
            "description: 'AWS production environment that owns the target host SSH secrets'",
            self.workflow,
        )
        self.assertIn("          - aws-candidate\n", self.workflow)
        self.assertIn("        default: aws-candidate\n", self.workflow)
        self.assertNotIn("azure-production", self.workflow)

    def test_image_and_caddy_upload_order_is_preserved(self) -> None:
        image_upload_index = self.workflow.index(
            "Upload image and start verified blue-green release"
        )
        caddy_upload_index = self.workflow.index(
            "Upload and activate verified AWS Caddy configuration"
        )
        summary_index = self.workflow.index("Record release summary")
        self.assertLess(image_upload_index, caddy_upload_index)
        self.assertLess(caddy_upload_index, summary_index)
        self.assertIn("timeout-minutes: 35", self.workflow)

    def test_caddy_upload_is_bound_to_the_exact_source_commit_and_digest(self) -> None:
        self.assertIn("CADDY_CONFIG=deploy/aws-candidate/Caddyfile", self.workflow)
        self.assertIn(
            'CADDY_DIGEST="sha256:$(sha256sum "$CADDY_CONFIG" | awk \'{print $1}\')"',
            self.workflow,
        )
        self.assertIn(
            '"deploy-caddy ${SOURCE_COMMIT} ${CADDY_DIGEST}"',
            self.workflow,
        )
        self.assertIn('< "$CADDY_CONFIG"', self.workflow)

    def test_caddy_publication_is_opt_in(self) -> None:
        options = re.search(r"(?m)^      deploy_caddy:\n((?: {8}.*\n)+)", self.workflow)
        self.assertIsNotNone(options)
        self.assertIn("type: boolean\n", options[1])
        self.assertIn("default: false\n", options[1])
        self.assertIn("!inputs.build_only && inputs.deploy_caddy", self.workflow)

    def test_summary_reports_the_real_outcome_of_each_release_step(self) -> None:
        summary = self.workflow.split("- name: Record release summary\n", 1)[1]
        self.assertIn("!cancelled() && steps.archive.outcome == 'success'", summary)
        self.assertIn("IMAGE_DEPLOY_OUTCOME: ${{ steps.deploy_image.outcome }}", summary)
        self.assertIn("CADDY_DEPLOY_OUTCOME: ${{ steps.deploy_caddy.outcome }}", summary)
        command = dedent(summary.split("run: |\n", 1)[1]).replace(
            "${{ inputs.deployment_target }}", "aws-candidate"
        )
        cases = (
            ("false", "success", "skipped", "not requested"),
            ("true", "success", "success", "success"),
            ("true", "success", "failure", "failure"),
            ("true", "failure", "skipped", "skipped"),
        )
        with TemporaryDirectory() as directory:
            output = Path(directory) / "summary"
            for requested, image, caddy, displayed in cases:
                with self.subTest(requested=requested, image=image, caddy=caddy):
                    subprocess.run(
                        ["bash", "-c", command],
                        env={
                            **os.environ,
                            "BUILD_ONLY": "false",
                            "DEPLOY_CADDY": requested,
                            "IMAGE_DEPLOY_OUTCOME": image,
                            "CADDY_DEPLOY_OUTCOME": caddy,
                            "GITHUB_STEP_SUMMARY": str(output),
                        },
                        check=True,
                    )
                    result = output.read_text()
                    self.assertIn(f"Production image and blue-green switch: {image}", result)
                    self.assertIn(f"Caddy template publication: {displayed}", result)
                    self.assertEqual("before the Caddy step failed" in result, caddy == "failure")
                    output.unlink()


if __name__ == "__main__":
    unittest.main()
