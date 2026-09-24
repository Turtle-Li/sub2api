#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SCRIPT = Path(__file__).resolve().parents[1] / "aws-candidate" / "allow-db-source.py"
SPEC = importlib.util.spec_from_file_location("allow_db_source", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class AllowDbSourceTest(unittest.TestCase):
    def test_uses_authoritative_backup_pipeline_lock(self):
        self.assertEqual(MODULE.BACKUP_LOCK, Path("/run/sub2api-db-backup/pipeline.lock"))

    def test_appends_source_without_removing_existing_sources(self):
        original = (
            '# comment\n'
            'PRODUCTION_PUBLIC_SOURCES=("4.216.216.16/32" "203.0.113.7/32")\n'
            'PUBLIC_INTERFACE=eth0\n'
        )
        updated = MODULE.update_firewall_config(original, "54.248.123.174/32")
        for source in ("4.216.216.16/32", "203.0.113.7/32", "54.248.123.174/32"):
            self.assertIn(source, updated)
        self.assertEqual(MODULE.update_firewall_config(updated, "54.248.123.174/32"), updated)

    def test_rejects_missing_azure_rollback_source(self):
        with self.assertRaisesRegex(AssertionError, "Azure rollback source"):
            MODULE.update_firewall_config(
                'PRODUCTION_PUBLIC_SOURCES=("203.0.113.7/32")\n',
                "54.248.123.174/32",
            )

    def test_clones_all_azure_hostssl_rules_idempotently(self):
        original = (
            "local all all trust\n"
            "hostssl sub2api sub2api 4.216.216.16/32 scram-sha-256\n"
            "hostssl replication replicator 4.216.216.16/32 scram-sha-256\n"
        )
        updated = MODULE.update_hba(original, "54.248.123.174/32")
        self.assertEqual(updated.count("54.248.123.174/32"), 2)
        self.assertEqual(MODULE.update_hba(updated, "54.248.123.174/32"), updated)

    def test_write_existing_preserves_inode(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config"
            path.write_text("old\n")
            inode = path.stat().st_ino
            MODULE.write_existing(path, "new\n")
            self.assertEqual(path.stat().st_ino, inode)
            self.assertEqual(path.read_text(), "new\n")

    def test_ufw_rule_detection_is_exact(self):
        status = (
            "Status: active\n"
            "5432/tcp on eth0 ALLOW IN 54.248.123.174 # Sub2API AWS candidate TLS\n"
            "6379/tcp on eth0 ALLOW IN 4.216.216.16\n"
        )
        with mock.patch.object(MODULE, "cmd", return_value=status):
            self.assertTrue(MODULE.ufw_rule_present("54.248.123.174", "5432"))
            self.assertFalse(MODULE.ufw_rule_present("54.248.123.174", "6379"))


if __name__ == "__main__":
    unittest.main()
