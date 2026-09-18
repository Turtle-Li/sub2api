"""Loopback-only demo with fake data. Never accesses production or upstream."""
import argparse
import json
from pathlib import Path
import tempfile
import threading
import time
from datetime import datetime, timezone, timedelta
from unittest import mock
from test_rotating import manager_module as mod, FakeHost, FakeNotifier, valid_state, probe_result

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--port',type=int,default=18787)
parser.add_argument('--read-only',action='store_true')
args=parser.parse_args()
with tempfile.TemporaryDirectory(prefix='probe-panel-preview-') as tmp:
    root=Path(tmp)
    config=root/'config.json'
    config.write_text(json.dumps({'state_dir':str(root/'state'), 'accounts':[
        {'id':7,'name':'demo-account','models':[{'name':'gpt-6-astra'},{'name':'gpt-5.6-sol'}]}],
        'panel':{'read_only':args.read_only}, 'proxies':{'sources':[]}, 'degraded':{'enabled':False}}))
    class DemoHost(FakeHost):
        def write_pinned_state(self, **kwargs):
            ok=super().write_pinned_state(**kwargs)
            self.pins[kwargs['account_id']][kwargs['model']]['updated_at']=datetime.now(timezone.utc).isoformat()
            return ok
    host=DemoHost({7:{'gpt-6-astra':{'state':valid_state(),'updated_at':datetime.now(timezone.utc).isoformat()}}})
    with mock.patch.object(mod,'Sub2APIHost',return_value=host), mock.patch.object(mod,'FeishuNotifier',return_value=FakeNotifier()):
        manager=mod.StateManager(config)
    manager.proxies=['http://demo.invalid:80']
    manager._proxy_sources={manager.proxies[0]:'static'}
    manager.degraded.enabled = not args.read_only
    manager.degraded.fetch=lambda **kw: ([{'account_id':7,'account_name':'demo-account',
        'sent_model':'gpt-6-astra','requested_model':'gpt-6-astra','response_model':'gpt-5.6-luna',
        'count':3,'last_seen':(datetime.now(timezone.utc)-timedelta(minutes=5)).isoformat(),
        'ttft_avg_ms':1200}], '')
    for source in ['static','rainproxy','proxora']:
        manager.stats.record_attempt(7,'gpt-6-astra',source,200,292,420,True)
        manager.stats.record_persisted(7,'gpt-6-astra',source)
    class PreviewHandler(mod.PanelHandler):
        def _send(self, code, payload, content_type="application/json"):
            if content_type.startswith("text/html"):
                payload=payload.replace(b'<h1>Codex', '<h1>本地演示（模拟数据） · Codex'.encode())
            super()._send(code, payload, content_type)
        def route(self, callback):
            if self.path=='/ctsm':
                self.send_response(308); self.send_header('Location','/ctsm/'); self.end_headers(); return
            if self.path.startswith('/ctsm/'):
                self.path=self.path[len('/ctsm'):]
            callback()
        def do_GET(self): self.route(super().do_GET)
        def do_POST(self): self.route(super().do_POST)
        def do_DELETE(self): self.route(super().do_DELETE)
    PreviewHandler.manager=manager
    server=mod.ThreadingHTTPServer(('127.0.0.1',args.port),PreviewHandler)
    threading.Thread(target=server.serve_forever,daemon=True).start()
    print(f'DEMO ONLY http://127.0.0.1:{server.server_port}/ctsm/',flush=True)
    try:
        with mock.patch.object(mod,'probe_turn_state',side_effect=lambda *a,**kw: probe_result(200,valid_state())):
            while True:
                manager.drain_manual_queue()
                manager.reconcile_manual_jobs()
                manager._wake.wait(timeout=0.1); manager._wake.clear()
    except KeyboardInterrupt: pass
    finally: server.shutdown(); server.server_close(); manager.stats.close()
