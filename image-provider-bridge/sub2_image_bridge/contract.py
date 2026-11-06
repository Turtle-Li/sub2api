"""One process request, one provider action. Never retry a submission."""
import base64
import re
import requests
from dataclasses import dataclass
from urllib.parse import urlsplit
from . import wan, seedream, seedream_model_policy as policy
from .types import Credential, ProviderPhaseContractError
from .capabilities import model_capabilities
from .wan_policy import validate_body as validate_wan
from .seedream_policy import validate_body as validate_seedream

class InvalidRequest(ValueError):
    pass

@dataclass(frozen=True, slots=True)
class Request:
    provider: str
    operation: str
    model: str
    prompt: str
    references: tuple[str, ...]
    size: str
    profile_version: str
    request_key: str
    task_id: str
    credential: Credential

    @classmethod
    def parse(cls, raw):
        allowed = {'provider','operation','model','prompt','references','parameters','request_key','task_id','secret'}
        if not isinstance(raw, dict) or set(raw) - allowed:
            raise InvalidRequest()
        provider, operation = raw.get('provider'), raw.get('operation')
        if provider not in {'wan', 'seedream'} or operation not in {'capabilities','submit','status','collect','cancel'}:
            raise InvalidRequest()
        if operation == 'capabilities':
            if set(raw) != {'provider', 'operation'}:
                raise InvalidRequest()
            return cls(provider, operation, '', '', (), '', '', '', '', Credential('', ''))
        if provider == 'seedream' and operation != 'submit':
            raise InvalidRequest()
        secret = raw.get('secret')
        if not isinstance(secret, dict) or set(secret) - {'api_key','endpoint_url','timeout_seconds'}:
            raise InvalidRequest()
        key, endpoint = secret.get('api_key'), secret.get('endpoint_url')
        timeout = secret.get('timeout_seconds',120)
        if not isinstance(key,str) or key != key.strip() or not key.startswith('sk-' if provider == 'wan' else 'ark-') or len(key)>4096:
            raise InvalidRequest()
        if not isinstance(endpoint,str) or type(timeout) is not int or not 1 <= timeout <= 600:
            raise InvalidRequest()
        parsed = urlsplit(endpoint)
        if provider == 'wan':
            if (parsed.scheme != 'https' or wan._WORKSPACE_HOST.fullmatch((parsed.hostname or '').lower()) is None or parsed.path.rstrip('/') != '/api/v1' or parsed.port is not None or parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment):
                raise InvalidRequest()
        elif endpoint.rstrip('/') != seedream.SEEDREAM_ENDPOINT:
            raise InvalidRequest()
        credential = Credential(endpoint, key, timeout)
        if operation != 'submit':
            task = raw.get('task_id')
            if set(raw) - {'provider','operation','task_id','secret'} or not isinstance(task,str) or not wan._TASK_ID.fullmatch(task):
                raise InvalidRequest()
            return cls(provider, operation, '', '', (), '', '', '', task, credential)
        params = raw.get('parameters', {})
        if not isinstance(params,dict) or set(params) - {'size','profile_version','n'} or type(params.get('n',1)) is not int or params.get('n',1) != 1:
            raise InvalidRequest()
        model, prompt, refs, request_key = (raw.get(k) for k in ('model','prompt','references','request_key'))
        size = params.get('size','2K')
        version = params.get('profile_version','2' if provider == 'wan' else '4')
        if (not all(isinstance(v,str) for v in (model,prompt,request_key,size,version)) or not prompt.strip() or len(prompt)>6000 or not re.fullmatch(r'[A-Za-z0-9._:-]{1,200}',request_key) or not isinstance(refs,list) or not 0 <= len(refs) <= 14 or any(not isinstance(v,str) or re.fullmatch(r'data:image/(?:png|jpeg|webp);base64,[A-Za-z0-9+/=]+',v) is None for v in refs) or 'task_id' in raw):
            raise InvalidRequest()
        return cls(provider, operation, model,prompt,tuple(refs),size,version,request_key,'',credential)

    def body(self):
        if self.provider == 'wan':
            body = {'model':self.model,'input':{'messages':[{'role':'user','content':[{'image':v} for v in self.references]+[{'text':self.prompt}]}]},'parameters':{'size':self.size,'n':1,'watermark':False,'enable_sequential':False}}
            validate_wan(body,self.profile_version)
            return body
        body = {'model':self.model,'prompt':self.prompt,'image':list(self.references),'size':self.size,'response_format':'b64_json','watermark':False}
        if not self.references:
            del body['image']
        if policy.seedream_requires_png(self.model):
            body['output_format']='png'
        if policy.seedream_supports_sequential_image_generation(self.model,self.profile_version):
            body['sequential_image_generation']='disabled'
        validate_seedream(body,self.profile_version)
        return body

def error(state, category):
    return {'state':state,'category':category,'message':'Provider action did not produce a confirmed result.'}

def execute(request: Request, session):
    if request.operation == 'capabilities':
        return {'state':'completed','provider':request.provider,'operations':['capabilities','submit','status','collect','cancel'] if request.provider == 'wan' else ['capabilities','submit'],'n':1,'requires_references':False,'request_key_is_idempotency_key':False,'models':model_capabilities(request.provider)}
    try:
        body = request.body() if request.operation == 'submit' else None
    except (ProviderPhaseContractError,ValueError,TypeError,RecursionError):
        return error('rejected','invalid_request')
    try:
        if request.provider == 'seedream':
            result, request_id = seedream.SeedreamHTTPClient(request.credential,session=session).generate(body,request.request_key)
            images = seedream._images(result,1)
            if request.credential.api_key in request_id:
                request_id = 'redacted-request-id'
            return {'state':'completed','request_id':request_id,'images':[{'mime_type':mime,'b64_json':base64.b64encode(content).decode('ascii')} for content,mime in images],'usage':{'image_count':len(images)}}
        client = wan.WanPhaseHTTPClient(request.credential,session=session)
        if request.operation == 'submit':
            result = client.submit(body)
            task_id = result.get('output',{}).get('task_id')
            if not isinstance(task_id,str) or not wan._TASK_ID.fullmatch(task_id) or request.credential.api_key in task_id:
                return error('outcome_unknown','invalid_submit_response')
            return {'state':'accepted','task_id':task_id}
        result = client.cancel(request.task_id) if request.operation == 'cancel' else client.status(request.task_id)
        wan._require_task(result,request.task_id)
        status = wan._status(result)
        if status not in {'PENDING','RUNNING','SUCCEEDED','FAILED','UNKNOWN','CANCELED'}:
            return error('outcome_unknown','invalid_status_response')
        if request.operation == 'collect' and status == 'SUCCEEDED':
            urls = wan._image_urls(result)
            if len(urls) != 1:
                return error('outcome_unknown','invalid_result_count')
            mime, content = client.image(urls[0])
            return {'state':'completed','task_id':request.task_id,'images':[{'mime_type':mime,'b64_json':base64.b64encode(content).decode('ascii')}],'usage':{'image_count':1}}
        return {'state':'accepted' if status in {'PENDING','RUNNING'} else 'completed','task_id':request.task_id,'task_status':status}
    except (seedream.SeedreamProviderPhaseError,wan.WanProviderPhaseError) as exc:
        unknown = exc.outcome_unknown or (request.operation == 'submit' and (getattr(exc,'submission_diagnostic_category',None) is not None or exc.code == 'WAN_RESPONSE_INVALID'))
        # Source error codes can incorporate upstream strings: never emit them.
        return error('outcome_unknown' if unknown else 'rejected','provider_error')
    except (AttributeError,TypeError,ValueError,KeyError,RecursionError,requests.RequestException):
        # Malformed upstream JSON after transmission must never permit replay.
        return error('outcome_unknown','invalid_provider_response')
