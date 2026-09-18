"""Exercise response parsing and the diagnostic boundary without network calls."""
import io
from pathlib import Path
import unittest
from unittest import mock
from test_rotating import manager_module


class ProbeWireTests(unittest.TestCase):
    def response(self, wire, body):
        def fake_popen(args, **kwargs):
            Path(args[args.index('--dump-header') + 1]).write_text(wire)
            Path(args[args.index('--output') + 1]).write_text(body)
            child = mock.Mock()
            child.poll.return_value = 0
            child.stderr = io.StringIO('')
            return child
        with mock.patch.object(manager_module.subprocess, 'Popen', side_effect=fake_popen):
            return manager_module.probe_turn_state(
                'http://fake.invalid:1',
                dict(version='0.154.0', token='fixture', account='fixture', device='fixture'),
                'custom-model',
            )

    def test_connect_forbidden_is_proxy_stage(self):
        r = self.response('HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\n\r\n', 'private-secret')
        self.assertEqual(r['http_status'], 403)
        self.assertEqual(r['diagnostic']['source'], 'proxy')
        self.assertFalse(r['diagnostic']['retryable'])
        self.assertNotIn('private-secret', str(r))

    def test_upstream_temporary_error_and_retry_after(self):
        r = self.response('HTTP/1.1 200 Connection established\r\n\r\nHTTP/2 403\r\nRetry-After: 12\r\n\r\n',
                          '{"error":{"code":"temporarily_unavailable","message":"private-secret"}}')
        self.assertEqual(r['diagnostic']['source'], 'upstream')
        self.assertTrue(r['diagnostic']['retryable'])
        self.assertEqual(r['retry_after'], '12')
        self.assertNotIn('private-secret', str(r))

    def test_edge_html_is_not_assumed_temporary(self):
        r = self.response('HTTP/1.1 200 Connection established\r\n\r\nHTTP/2 403\r\nCF-Ray: 0123456789abcdef-SJC\r\n\r\n',
                          '<html>private-secret</html>')
        self.assertEqual(r['diagnostic']['source'], 'edge_hint')
        self.assertEqual(r['diagnostic']['body_kind'], 'html')
        self.assertEqual(r['diagnostic']['request_id'], '0123456789abcdef-SJC')
        self.assertFalse(r['diagnostic']['retryable'])
        self.assertNotIn('private-secret', str(r))

if __name__ == '__main__':
    unittest.main()
