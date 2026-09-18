"""Local-only panel regressions: no real database, proxy or model requests."""
import http.client
import json
import threading
import unittest
from unittest import mock
import test_accounts_retry as support
from test_rotating import manager_module as mod, probe_result, valid_state

class PanelTests(support.AccountsAndRetryTests):
    # Reuse the harmless config/host factory, not the inherited test cases.
    def make(self):
        return self._manager([self._account(7, 'test-account', [self._model('a'), self._model('b')])])

    def test_upsert_preserves_baseline_siblings_and_config(self):
        manager, host, state, config = self.make()
        before = config.read_bytes()
        manager.upsert_account(7, 'test-account', 'c', 292)
        self.assertEqual([m['name'] for m in manager.accounts()[0]['models']], ['a','b','c'])
        self.assertEqual(config.read_bytes(), before)

    def test_invalid_target_does_not_write(self):
        manager, *_ = self.make()
        with self.assertRaises(ValueError): manager.upsert_account(7,'test-account','a',0)
        self.assertFalse(manager._overlay_file.exists())

    def test_queue_rejects_unknown_and_deduplicates(self):
        manager, *_ = self.make()
        with self.assertRaises(ValueError): manager.enqueue_manual(99,'a',True)
        with self.assertRaises(ValueError): manager.enqueue_manual(7,'unknown',True)
        first = manager.enqueue_manual(7,'a',True)
        self.assertEqual(first, manager.enqueue_manual(7,'a',True))
        self.assertEqual(len(manager._manual_queue),1)

    def test_timestamp_offset_comparison_and_missing_evidence(self):
        verdict=mod.StateManager._degradation_verdict
        self.assertEqual(verdict({'last_seen':'2026-09-18T10:00:00+08:00'}, {'updated_at':'2026-09-18T03:00:00Z'}),'handled')
        self.assertEqual(verdict(None,{}),'no_record')
        self.assertEqual(verdict({'last_seen':'invalid'},{'updated_at':'invalid'}),'unknown')

    def test_manual_job_waits_for_real_pin(self):
        manager, host, *_ = self.make()
        job=manager.enqueue_manual(7,'a',True)
        with mock.patch.object(mod,'probe_turn_state',return_value=probe_result(200,valid_state(312))):
            manager.drain_manual_queue()
        self.assertEqual(manager.job(job)['status'],'waiting')
        with mock.patch.object(mod,'probe_turn_state',return_value=probe_result(200,valid_state())):
            manager.run_check_and_refresh()
            manager.reconcile_manual_jobs()
        self.assertEqual(manager.job(job)['status'],'done')
        self.assertEqual(manager.job(job)['updated'],1)

    def test_manual_queue_serial_and_backoff(self):
        manager, *_ = self.make()
        owner = threading.get_ident()
        calls = []
        def probe(*args, **kwargs):
            calls.append(threading.get_ident())
            return probe_result(200, valid_state())
        first = manager.enqueue_manual(7, 'a', True)
        second = manager.enqueue_manual(7, 'b', True)
        with mock.patch.object(mod, 'probe_turn_state', side_effect=probe):
            self.assertEqual(calls, [])
            manager._retry_after['7:a'] = mod.time.time() + 300
            manager.drain_manual_queue()
        self.assertEqual(calls, [owner])
        self.assertEqual(manager.job(first)['status'], 'waiting')
        self.assertEqual(manager.job(second)['status'], 'done')
        self.assertGreater(manager._retry_after['7:a'], mod.time.time())

    def test_paused_waiting_job_is_cancelled(self):
        manager, host, *_ = self.make()
        job = manager.enqueue_manual(7, 'a', True)
        manager._retry_after['7:a'] = mod.time.time() + 300
        manager.drain_manual_queue()
        manager.remove_model(7, 'a')
        manager.reconcile_manual_jobs()
        self.assertEqual(manager.job(job)['status'], 'cancelled')
        self.assertEqual([m['name'] for m in manager.accounts()[0]['models']], ['b'])
        self.assertEqual(host.write_calls, [])

    def test_corrupt_overlay_is_not_overwritten(self):
        manager, *_ = self.make()
        manager.upsert_account(7, 'test-account', 'c', 292)
        manager._overlay_file.write_text('{"7":{"models":"invalid"}}')
        before = manager._overlay_file.read_bytes()
        with self.assertRaises(ValueError): manager.upsert_account(7,'test-account','d',292)
        self.assertEqual(manager._overlay_file.read_bytes(), before)
        self.assertEqual([m['name'] for m in manager.accounts()[0]['models']], ['a','b','c'])

    def test_snapshot_failure_does_not_claim_healthy_or_leak(self):
        manager, host, *_ = self.make()
        manager.degraded.fetch = lambda: ([], 'backend query unavailable')
        state = manager.snapshot()
        self.assertEqual(state['accounts'][0]['models'][0]['degradation'], 'unknown')
        host.read_pinned_states = mock.Mock(side_effect=RuntimeError('secret-database-uri'))
        self.assertNotIn('secret-database-uri', json.dumps(manager.snapshot()))

    def test_legacy_history_redacts_sensitive_fields(self):
        manager, *_ = self.make()
        manager._history_file.write_text(json.dumps({'7:a':[
            {'ok':True,'at':1,'state_len':292,'proxy':'http://secret-user:secret-password@host',
             'error':'secret-db','state':'secret-state','source':'http://secret'}]}))
        data = json.dumps(manager._load_history())
        self.assertNotIn('secret',data)
        self.assertIn('legacy',data)

    def test_private_read_only_surface(self):
        manager, *_ = self.make()
        manager.config['panel'] = {'read_only': True}
        manager.degraded.enabled = False
        snapshot = manager.snapshot()
        self.assertTrue(snapshot['read_only'])
        self.assertFalse(snapshot['degraded_enabled'])
        handler = type('ReadOnlyHandler', (mod.PanelHandler,), {'manager': manager})
        server = mod.ThreadingHTTPServer(('127.0.0.1',0), handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        try:
            conn = http.client.HTTPConnection(*server.server_address, timeout=3)
            for method, path, body in [('POST','/api/probe','{"account_id":7}'),
                                      ('POST','/api/accounts','{"account_id":7,"name":"test-account","model":"c"}'),
                                      ('DELETE','/api/accounts/7/a',None)]:
                conn.request(method,path,body=body,headers={'X-CTSM-Panel':'1'})
                response=conn.getresponse(); self.assertEqual(response.status,403); response.read()
            conn.request('GET','/api/state'); response=conn.getresponse()
            self.assertEqual(response.status,200)
            self.assertTrue(json.loads(response.read())['read_only'])
            self.assertFalse(manager._overlay_file.exists())
            self.assertEqual(manager.jobs(), [])
            conn.close()
        finally: server.shutdown(); server.server_close(); thread.join()

    def test_stats_http_thread_and_csrf(self):
        manager, *_ = self.make()
        handler=type('TestHandler',(mod.PanelHandler,),{'manager':manager})
        server=mod.ThreadingHTTPServer(('127.0.0.1',0),handler)
        thread=threading.Thread(target=server.serve_forever,daemon=True); thread.start()
        try:
            conn=http.client.HTTPConnection(*server.server_address,timeout=3)
            conn.request('GET','/api/stats'); response=conn.getresponse()
            self.assertEqual(response.status,200,response.read().decode() if response.status!=200 else '')
            self.assertEqual(json.loads(response.read())['totals']['attempts'],0)
            conn.request('POST','/api/probe',body='{"account_id":7}',headers={'Content-Type':'application/json'})
            response=conn.getresponse(); self.assertEqual(response.status,403); response.read()
            conn.request('POST','/api/probe',body='{"account_id":7}',headers={'X-CTSM-Panel':'1','Origin':'https://evil.invalid'})
            response=conn.getresponse(); self.assertEqual(response.status,403); response.read()
            conn.close()
        finally: server.shutdown(); server.server_close(); thread.join()

# Prevent duplicate inherited baseline tests (these run in their own module).
for name in dir(support.AccountsAndRetryTests):
    if name.startswith('test_') and name not in PanelTests.__dict__:
        setattr(PanelTests,name,None)

if __name__=='__main__': unittest.main()
