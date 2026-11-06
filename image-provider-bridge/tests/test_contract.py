import base64
import json
import subprocess
import sys
import pytest
import requests
from sub2_image_bridge.contract import Request, execute, InvalidRequest
from sub2_image_bridge import wan, seedream
from sub2_image_bridge.types import Credential, ProviderPhaseContractError
from sub2_image_bridge.bounded import BoundedSession

PNG = b'\x89PNG\r\n\x1a\ntest-image'
ENC = base64.b64encode(PNG).decode()

class Response:
    def __init__(self, value=None, status=200, content=PNG, mime='image/png'):
        self.value, self.status_code, self.content = value,status,content
        self.headers = {'x-request-id':'safe-request-id','Content-Type':mime}
        self.closed = False
    def json(self):
        return self.value
    def close(self):
        self.closed = True
    def iter_content(self,size):
        yield self.content

class Session:
    def __init__(self,response=None,failure=None):
        self.response,self.failure,self.calls = response,failure,[]
    def request(self,*args,**kwargs):
        self.calls.append((args,kwargs))
        if self.failure:
            raise self.failure
        return self.response
    post = request
    get = request

def payload(provider='seedream',operation='submit'):
    return {'provider':provider,'operation':operation,'model':'doubao-seedream-5-0-pro-260628' if provider=='seedream' else 'wan2.7-image','prompt':'preserve product','references':['data:image/png;base64,'+ENC],'parameters':{'size':'2K'},'request_key':'test-123','secret':{'api_key':'ark-secret' if provider=='seedream' else 'sk-secret','endpoint_url':seedream.SEEDREAM_ENDPOINT if provider=='seedream' else 'https://ws-abcdefgh.cn-beijing.maas.aliyuncs.com/api/v1'}}

def test_seedream_success_shape():
    session=Session(Response({'data':[{'b64_json':ENC}]}))
    out=execute(Request.parse(payload()),session)
    assert out['state']=='completed' and out['images'][0]['b64_json']==ENC
    args,kwargs=session.calls[0]
    assert args==(seedream.SEEDREAM_ENDPOINT+'/images/generations',)
    assert kwargs['headers']['X-Client-Request-Id']=='test-123'
    assert kwargs['headers']['Authorization']=='Bearer ark-secret'
    assert kwargs['json']['output_format']=='png'
    assert 'sequential_image_generation' not in kwargs['json']
    assert kwargs['allow_redirects'] is False
    assert len(session.calls)==1

@pytest.mark.parametrize('provider',['seedream','wan'])
@pytest.mark.parametrize('value,status,failure',[(None,200,requests.Timeout('secret')),([],503,None),({},200,None),([],200,None)])
def test_ambiguous_submit_never_replays(provider,value,status,failure):
    session=Session(Response(value,status),failure)
    out=execute(Request.parse(payload(provider)),session)
    assert out['state']=='outcome_unknown'
    assert len(session.calls)==1
    assert 'secret' not in json.dumps(out)

@pytest.mark.parametrize('provider',['seedream','wan'])
def test_http_rejection_redacts_upstream(provider):
    session=Session(Response({'code':'ARK_SECRET','error':{'code':'ARK_SECRET'}},400))
    out=execute(Request.parse(payload(provider)),session)
    assert out['state']=='rejected'
    assert 'ARK_SECRET' not in json.dumps(out)

def test_wan_submit_and_existing_task_operations():
    request=payload('wan')
    session=Session(Response({'output':{'task_id':'task-123456','task_status':'PENDING'}}))
    assert execute(Request.parse(request),session)=={'state':'accepted','task_id':'task-123456'}
    assert session.calls[0][1]['headers']['X-DashScope-Async']=='enable'
    assert session.calls[0][0]==('POST',request['secret']['endpoint_url']+wan.WAN_SUBMIT_PATH)
    for op,method,path in [('status','GET','/tasks/task-123456'),('cancel','POST','/tasks/task-123456/cancel')]:
        session.calls.clear()
        out=execute(Request.parse({'provider':'wan','operation':op,'task_id':'task-123456','secret':request['secret']}),session)
        assert out['state']=='accepted'
        assert session.calls[0][0]==(method,request['secret']['endpoint_url']+path)
        assert len(session.calls)==1

def test_wan_collect_bounded_download():
    request=payload('wan')
    status={'output':{'task_id':'task-123456','task_status':'SUCCEEDED','choices':[{'message':{'content':[{'type':'image','image':'https://bucket.aliyuncs.com/image.png'}]}}]}}
    class CollectSession(Session):
        def get(self,*args,**kwargs):
            self.calls.append((args,kwargs))
            return Response()
    session=CollectSession(Response(status))
    out=execute(Request.parse({'provider':'wan','operation':'collect','task_id':'task-123456','secret':request['secret']}),session)
    assert out['images'][0]['b64_json']==ENC
    assert len(session.calls)==2
    assert 'Authorization' not in session.calls[1][1]['headers']

@pytest.mark.parametrize('url',['http://bucket.aliyuncs.com/image','https://evil.com/image','https://user@bucket.aliyuncs.com/image'])
def test_wan_result_host_rejects_without_io(url):
    session=Session()
    with pytest.raises(wan.WanProviderPhaseError):
        wan.WanPhaseHTTPClient(Credential('', ''),session=session).image(url)
    assert session.calls==[]

@pytest.mark.parametrize('mime,content',[('text/html',PNG),('image/png',b'x'*(24*1024*1024+1)),('image/png',b'')])
def test_wan_result_mime_size_empty(mime,content):
    session=Session(Response(content=content,mime=mime))
    with pytest.raises(wan.WanProviderPhaseError):
        wan.WanPhaseHTTPClient(Credential('', ''),session=session).image('https://bucket.aliyuncs.com/i')
    assert session.response.closed

@pytest.mark.parametrize('provider,size,count,version',[('wan','4K',1,'2'),('wan','2K',10,'2'),('seedream','4K',1,'4'),('seedream','2K',11,'4'),('seedream','1728x2304',1,'3')])
def test_original_policy_rejects(provider,size,count,version):
    raw=payload(provider)
    raw['parameters']={'size':size,'profile_version':version}
    raw['references']*=count
    with pytest.raises(ProviderPhaseContractError):
        Request.parse(raw).body()

def test_policy_legacy_and_native_sizes():
    raw=payload()
    raw['parameters']={'size':'1728x2304','profile_version':'4'}
    assert Request.parse(raw).body()['size']=='1728x2304'
    raw['model']='doubao-seedream-5-0-260128'
    raw['parameters']={'size':'4K','profile_version':'2'}
    assert Request.parse(raw).body()['sequential_image_generation']=='disabled'

@pytest.mark.parametrize('change',[{'parameters':{'n':2}},{'references':['https://unsupported.test/i']},{'operation':'status'},{'parameters':{'seed':1}},{'secret':{'api_key':'ark-secret','endpoint_url':'http://localhost'}}])
def test_invalid_request_before_network(change):
    raw=payload(); raw.update(change)
    with pytest.raises(InvalidRequest):
        Request.parse(raw)

@pytest.mark.parametrize('raw',[b'[]',b'{}',b'{invalid',b'{}\n{}',json.dumps({'provider':'seedream','operation':'capabilities'}).encode()])
def test_cli_one_json_no_stderr(raw):
    run=subprocess.run([sys.executable,'-m','sub2_image_bridge'],input=raw,capture_output=True,check=True)
    result=json.loads(run.stdout)
    assert result['state'] in {'rejected','completed'}
    assert run.stderr==b''
    assert len(run.stdout.splitlines())==1

def test_response_memory_ceiling(monkeypatch):
    import sub2_image_bridge.bounded as bounded
    monkeypatch.setattr(bounded,'MAX_JSON_BYTES',8)
    response=Response(content=b'x'*9)
    monkeypatch.setattr(requests.Session,'request',lambda *args,**kwargs:response)
    with BoundedSession() as session, pytest.raises(requests.RequestException):
        session.post('https://fixture.invalid')
    assert response.closed

@pytest.mark.parametrize('data',[[{'b64_json':'bad!'}],[{'b64_json':base64.b64encode(b'not-image').decode()}],[],[{'b64_json':ENC},{'b64_json':ENC}]])
def test_seedream_bad_2xx_images_are_unknown(data):
    session=Session(Response({'data':data}))
    assert execute(Request.parse(payload()),session)['state']=='outcome_unknown'
    assert len(session.calls)==1

def test_subprocess_success_with_offline_session():
    harness = '''import runpy
from sub2_image_bridge.bounded import BoundedSession
class Response:
    status_code=200
    headers={"x-request-id":"fixture-id"}
    def json(self): return {"data":[{"b64_json":"iVBORw0KGgo="}]}
    def close(self): pass
def request(self,*args,**kwargs): return Response()
BoundedSession.post=request
runpy.run_module("sub2_image_bridge",run_name="__main__")
'''
    run=subprocess.run([sys.executable,'-c',harness],input=json.dumps(payload()).encode(),capture_output=True,check=True)
    out=json.loads(run.stdout)
    assert out['state']=='completed'
    assert out['images'][0]['mime_type']=='image/png'
    assert b'ark-secret' not in run.stdout and run.stderr==b''

def test_malformed_capability_and_deep_json_subprocess():
    for raw in [b'['*1200+b']'*1200,json.dumps({'provider':'seedream','operation':'capabilities','secret':{'api_key':'private'}}).encode()]:
        run=subprocess.run([sys.executable,'-m','sub2_image_bridge'],input=raw,capture_output=True,check=True)
        assert json.loads(run.stdout)['state']=='rejected'
        assert run.stderr==b''


@pytest.mark.parametrize("provider", ["wan", "seedream"])
def test_text_only_native_contract(provider):
    raw = payload(provider)
    raw["references"] = []
    response = {"output": {"task_id": "task-123456"}} if provider == "wan" else {"data": [{"b64_json": ENC}]}
    session = Session(Response(response))
    out = execute(Request.parse(raw), session)
    assert out["state"] == ("accepted" if provider == "wan" else "completed")
    body = session.calls[0][1]["json"]
    if provider == "wan":
        assert body["input"]["messages"][0]["content"] == [{"text": raw["prompt"]}]
    else:
        assert "image" not in body
        assert body["output_format"] == "png"
    assert len(session.calls) == 1

@pytest.mark.parametrize('provider', ['wan', 'seedream'])
def test_module_upstream_recursion_is_unknown_after_one_submit(provider):
    harness = '''import runpy
from sub2_image_bridge.bounded import BoundedSession
calls = []
class Response:
    status_code = 200
    headers = {}
    def json(self): raise RecursionError("private-upstream-detail-ark-secret")
    def close(self): pass
def request(self, *args, **kwargs):
    calls.append((args, kwargs))
    return Response()
BoundedSession.post = request
BoundedSession.request = request
runpy.run_module("sub2_image_bridge", run_name="__main__")
assert len(calls) == 1
'''
    run = subprocess.run([sys.executable, '-c', harness], input=json.dumps(payload(provider)).encode(), capture_output=True, check=True)
    result = json.loads(run.stdout)
    assert result['state'] == 'outcome_unknown'
    assert result['category'] == 'invalid_provider_response'
    assert run.stderr == b''
    assert b'private-upstream-detail' not in run.stdout
    assert b'ark-secret' not in run.stdout and b'sk-secret' not in run.stdout
    assert len(run.stdout.splitlines()) == 1


def test_module_invalid_model_rejected_before_transport():
    harness = '''import runpy
from sub2_image_bridge.bounded import BoundedSession
def request(self, *args, **kwargs):
    raise AssertionError("must not transmit invalid input")
BoundedSession.post = request
runpy.run_module("sub2_image_bridge", run_name="__main__")
'''
    raw = payload()
    raw['model'] = 'unsupported-model'
    run = subprocess.run([sys.executable, '-c', harness], input=json.dumps(raw).encode(), capture_output=True, check=True)
    assert json.loads(run.stdout)['state'] == 'rejected'
    assert run.stderr == b''

@pytest.mark.parametrize('model,version', [('doubao-seedream-5-0-pro-260628','2'),('doubao-seedream-5-0-pro-260628','4'),('doubao-seedream-5-0-lite-260128','4')])
def test_seedream_single_output_validator_rejects_group_options(model, version):
    from sub2_image_bridge.seedream_policy import validate_body
    raw = payload()
    raw['model'] = model
    raw['parameters']['profile_version'] = version
    body = Request.parse(raw).body()
    body['sequential_image_generation_options'] = {'max_images': 1}
    with pytest.raises(ProviderPhaseContractError):
        validate_body(body, version)
