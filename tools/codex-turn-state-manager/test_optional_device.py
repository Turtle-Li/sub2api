import base64,io,json,unittest
from pathlib import Path
from unittest import mock
from test_rotating import manager_module as m
class OptionalDeviceTests(unittest.TestCase):
 def test_credentials_optional_device(self):
  host=m.Sub2APIHost({})
  for device in (None,'','existing-device','bad\nvalue'):
   data=dict(token='token',account='account',version='0.154.0',device=device)
   with mock.patch.object(host,'run_sql',return_value=json.dumps(data)):
    if device=='bad\nvalue':
     with self.assertRaises(RuntimeError):host.fetch_account(60,'test@example.com')
    else:self.assertEqual(host.fetch_account(60,'test@example.com')['device'],device or '')
 def test_utf8_account_name_uses_base64_sql_comparison(self):
  host=m.Sub2APIHost({})
  name='账号 九  测试'
  data=dict(token='token',account='account',version='0.154.0',device=None)
  with mock.patch.object(host,'run_sql',return_value=json.dumps(data)) as run_sql:
   self.assertEqual(host.fetch_account(9,name)['device'],'')
  sql=run_sql.call_args.args[0]
  encoded=base64.b64encode(name.encode('utf-8')).decode('ascii')
  self.assertIn(f"convert_from(decode('{encoded}', 'base64'), 'UTF8')",sql)
  self.assertNotIn(name,sql)
  with self.assertRaises(ValueError):host.fetch_account(9,'账号\n测试')
 def test_wire_omits_only_absent_device(self):
  for device in (None,'existing-device'):
   captured=[]
   class Input(io.StringIO):
    def close(self):captured.append(self.getvalue());super().close()
   def popen(args,**kwargs):
    Path(args[args.index('--dump-header')+1]).write_text('HTTP/2 200\r\nx-codex-turn-state: test\r\n\r\n')
    child=mock.Mock();child.stdin=Input();child.stderr=io.StringIO('');child.poll.return_value=0;return child
   with mock.patch.object(m.subprocess,'Popen',side_effect=popen):
    m.probe_turn_state('http://test.invalid:80',dict(token='token',account='account',version='0.154.0',device=device),'model')
   self.assertEqual('X-Codex-Installation-ID' in captured[0],bool(device))
   self.assertIn('ChatGPT-Account-ID',captured[0])
