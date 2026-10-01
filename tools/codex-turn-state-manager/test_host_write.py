#!/usr/bin/env python3
"""Mock-only coverage for the SQL that Sub2APIHost.write_pinned_state emits.

run_sql/read_pinned_states are stubbed, so nothing reaches docker or PostgreSQL.
"""

import base64
import re
import unittest
from unittest import mock

import manager


class HostWriteTests(unittest.TestCase):
    def _write(self, **kwargs):
        host = manager.Sub2APIHost({"container": "test", "use_sudo": False})
        executed = []
        with mock.patch.object(host, "run_sql", side_effect=lambda sql, read_only=True: executed.append(sql) or ""), \
                mock.patch.object(host, "read_pinned_states", return_value={"gpt-6-astra": {"state": "gAAAAAB_ok"}}):
            ok = host.write_pinned_state(
                account_id=9,
                model="gpt-6-astra",
                state="gAAAAAB_ok",
                expires_at_iso="2026-10-02T12:00:00+00:00",
                state_len=292,
                **kwargs,
            )
        self.assertTrue(ok)
        self.assertEqual(len(executed), 1)
        return executed[0]

    @staticmethod
    def _decoded_payloads(sql):
        return [base64.b64decode(m).decode("utf-8")
                for m in re.findall(r"decode\('([A-Za-z0-9+/=]+)', 'base64'\)", sql)]

    def test_proxy_credentials_never_reach_accounts_extra(self):
        sql = self._write(
            cookie="__cflb=a; __oailb=b",
            cookie_expires_at_iso="2026-10-02T12:02:30+00:00",
            proxy="http://relay:s3cret@100.79.230.109:7890",
        )
        decoded = "\n".join(self._decoded_payloads(sql))
        self.assertNotIn("s3cret", sql + decoded)
        self.assertNotIn("relay:", sql + decoded)
        self.assertIn('"proxy_endpoint": "100.79.230.109:7890"', decoded)
        self.assertNotIn('"proxy":', decoded)

    def test_write_enqueues_scheduler_outbox_event_in_same_transaction(self):
        sql = self._write()
        update_at = sql.index("UPDATE accounts")
        outbox_at = sql.index("INSERT INTO scheduler_outbox")
        self.assertLess(update_at, outbox_at)
        self.assertIn("'account_changed', 9, NULL, NULL", sql)
        self.assertIn(manager.scheduler_outbox_dedup_key(9), sql)
        self.assertIn("EXCEPTION WHEN undefined_table", sql)

    def test_dedup_key_matches_go_scheduler_outbox(self):
        # Pinned by TestSchedulerOutboxDedupKeyMatchesTurnStateManager in
        # backend/internal/repository/scheduler_outbox_repo_test.go.
        self.assertEqual(
            manager.scheduler_outbox_dedup_key(9),
            "scheduler_outbox:8ff3d5cb02708a941c0c97b3c284ddffdad885615336d19668da3c422c8ea5d2",
        )

    def test_proxy_endpoint_strips_credentials(self):
        self.assertEqual(manager.proxy_endpoint("http://u:p@1.2.3.4:8080"), "1.2.3.4:8080")
        self.assertEqual(manager.proxy_endpoint("not a url"), "")


if __name__ == "__main__":
    unittest.main()
