import base64
import json
import unittest
from unittest.mock import patch

from legacy_host_adapter import IntegratedSub2APIHost, ACCOUNT_NAME_RE
from manager import MIN_CODEX_CLIENT_VERSION


class HostAdapterTests(unittest.TestCase):
    def test_name_is_encoded_and_query_is_account_scoped(self):
        host = IntegratedSub2APIHost({'container': 'test-backend', 'use_sudo': False})
        name = "中文测试 O'Reilly \\ test"
        data = dict(token='dummy', account='dummy', device='dummy', version=MIN_CODEX_CLIENT_VERSION)
        with patch.object(host, 'run_sql', return_value=json.dumps(data)) as sql:
            self.assertEqual(host.fetch_account(17, name), data)
        statement = sql.call_args.args[0]
        self.assertNotIn(name, statement)
        self.assertIn(base64.b64encode(name.encode()).decode(), statement)
        self.assertIn('a.id = 17', statement)
        self.assertIn("a.platform = 'openai'", statement)
        self.assertIn("a.type IN ('oauth', 'setup-token')", statement)

    def test_invalid_names_never_query_database(self):
        host = IntegratedSub2APIHost({'container': 'test-backend'})
        with patch.object(host, 'run_sql') as sql:
            for name in ('', 'x\n', 'x\x00', 'x' * 129):
                self.assertIsNone(ACCOUNT_NAME_RE.fullmatch(name))
                with self.assertRaises(ValueError):
                    host.fetch_account(1, name)
            sql.assert_not_called()

    def test_original_credential_validation_is_retained(self):
        host = IntegratedSub2APIHost({'container': 'test-backend'})
        with patch.object(host, 'run_sql', return_value='{}'):
            with self.assertRaisesRegex(RuntimeError, 'unusable'):
                host.fetch_account(1, '中文')

    def test_pin_write_and_scheduler_event_share_transaction(self):
        host = IntegratedSub2APIHost({'container': 'test-backend'})
        with patch.object(host, 'run_sql') as sql, patch.object(host, 'read_pinned_states', return_value={'gpt-x': {'state': 'safe-state'}}):
            self.assertTrue(host.write_pinned_state(17, 'gpt-x', 'safe-state', '2026-09-18T03:00:00Z', 10))
        sql.assert_called_once()
        statement = sql.call_args.args[0]
        self.assertIs(sql.call_args.kwargs['read_only'], False)
        self.assertIn("COALESCE(extra->'pinned_codex_turn_states'", statement)
        self.assertIn("INSERT INTO scheduler_outbox", statement)
        self.assertIn("SELECT 'account_changed', id, NULL, NULL FROM updated", statement)
        self.assertNotIn('safe-state', statement)

    def test_pin_readback_failure_is_not_success(self):
        host = IntegratedSub2APIHost({'container': 'test-backend'})
        with patch.object(host, 'run_sql'), patch.object(host, 'read_pinned_states', return_value={}):
            self.assertFalse(host.write_pinned_state(17, 'gpt-x', 'safe-state', '2026-09-18T03:00:00Z', 10))

    def test_reuses_native_fingerprint_without_legacy_device(self):
        host = IntegratedSub2APIHost({'container': 'test-backend'})
        for mode in ('device', 'session', 'full'):
            data = dict(token='dummy', account='dummy', device=None,
                        fingerprint_mode=mode,
                        fingerprint_seed='11111111-2222-4333-8444-555555555555',
                        version=MIN_CODEX_CLIENT_VERSION)
            with self.subTest(mode=mode), patch.object(host, 'run_sql', return_value=json.dumps(data)):
                result = host.fetch_account(17, '中文')
                # Fixed vector from the target Go deriveStableUUIDv4 contract.
                self.assertEqual(result['device'], '8375f89a-c7b4-4024-b6cd-f58f9ebd30b2')
                self.assertNotIn('fingerprint_seed', result)

    def test_native_fingerprint_never_enables_off_or_invents_seed(self):
        host = IntegratedSub2APIHost({'container': 'test-backend'})
        for mode, seed in [('off', '11111111-2222-4333-8444-555555555555'),
                           ('session', ''), ('session', 'bad-seed'),
                           ('session', '00000000-0000-0000-0000-000000000000')]:
            data = dict(token='dummy', account='dummy', device=None, fingerprint_mode=mode,
                        fingerprint_seed=seed, version=MIN_CODEX_CLIENT_VERSION)
            with self.subTest(mode=mode, seed=seed), patch.object(host, 'run_sql', return_value=json.dumps(data)):
                with self.assertRaisesRegex(RuntimeError, 'device'):
                    host.fetch_account(17, '中文')

    def test_real_device_override_matches_native_priority(self):
        host = IntegratedSub2APIHost({'container': 'test-backend'})
        data = dict(token='dummy', account='dummy', device='  real-device  ',
                    fingerprint_mode='session', fingerprint_seed='bad-seed',
                    version=MIN_CODEX_CLIENT_VERSION)
        with patch.object(host, 'run_sql', return_value=json.dumps(data)):
            self.assertEqual(host.fetch_account(17, '中文')['device'], 'real-device')
