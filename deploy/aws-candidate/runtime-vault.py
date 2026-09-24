#!/usr/bin/python3
"""Archive the approved source runtime in Vault, then inject the AWS candidate."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

VAULT = str(Path.home() / ".local/bin/infra-vault")
ITEM_NAME = "sub2api-aws-runtime-snapshot-20260924-47c28fe4"
SOURCE = "sub2api-candidate"
TARGET = "ubuntu@54.248.123.174"
REVISION = "47c28fe46956fbac22d8bfb4c0b42a261f6ef4f9"
SSH = ["/usr/bin/ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=15"]

COLLECT = r'''
import base64,json,pathlib,subprocess,sys
expected_revision=sys.argv[1]
x=json.loads(subprocess.check_output(['docker','inspect','sub2api-blue']))[0]
if not x['State']['Running']:
 x=json.loads(subprocess.check_output(['docker','inspect','sub2api-green']))[0]
assert x['State']['Running'] and x['State']['Health']['Status']=='healthy'
assert x['Config']['Labels']['org.opencontainers.image.revision']==expected_revision
paths={
 'config':'/var/lib/docker/volumes/sub2api-candidate-data/_data/config.yaml',
 'health':'/opt/sub2api/secrets/internal-health-token',
 'ca':'/opt/sub2api/db-host-ca/ca.crt',
 'api_chain':'/opt/sub2api/certs/api.turtleligpt.com/current/fullchain.pem',
 'api_key':'/opt/sub2api/certs/api.turtleligpt.com/current/privkey.pem',
 'www_chain':'/var/lib/docker/volumes/sub2api-candidate-caddy-data/_data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/www.turtleligpt.com/www.turtleligpt.com.crt',
 'www_key':'/var/lib/docker/volumes/sub2api-candidate-caddy-data/_data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/www.turtleligpt.com/www.turtleligpt.com.key',
}
files={k:base64.b64encode(pathlib.Path(p).read_bytes()).decode() for k,p in paths.items()}
env=x['Config']['Env']
assert all('\n' not in s and '\r' not in s and '=' in s for s in env)
print(json.dumps({'revision':x['Config']['Labels']['org.opencontainers.image.revision'],'env':env,'files':files}))
'''

INSTALL = r'''
import base64,json,os,pathlib,sys
expected_revision=sys.argv[1]
b=json.load(sys.stdin)
assert b['revision']==expected_revision
assert set(b['files'])=={'config','health','ca','api_chain','api_key','www_chain','www_key'}
root='/opt/sub2api'
entries={
 'config':('/var/lib/docker/volumes/sub2api-candidate-data/_data/config.yaml',0o600,1000),
 'health':(root+'/secrets/internal-health-token',0o600,1000),
 'ca':(root+'/db-host-ca/ca.crt',0o644,0),
 'api_chain':(root+'/certs/api/current/fullchain.pem',0o644,0),
 'api_key':(root+'/certs/api/current/privkey.pem',0o600,0),
 'www_chain':(root+'/certs/www/current/fullchain.pem',0o644,0),
 'www_key':(root+'/certs/www/current/privkey.pem',0o600,0),
}
env=b['env']
assert all(isinstance(s,str) and '\n' not in s and '\r' not in s and '=' in s for s in env)
values=dict(s.split('=',1) for s in env)
assert len(values)==len(env)
assert values['DATABASE_SSLMODE']=='verify-full'
assert values['REDIS_ENABLE_TLS']=='true'
external={'DATABASE_HOST','DATABASE_PORT','DATABASE_USER','DATABASE_PASSWORD','DATABASE_DBNAME','DATABASE_SSLMODE','REDIS_HOST','REDIS_PORT','REDIS_USERNAME','REDIS_PASSWORD','REDIS_DB','REDIS_ENABLE_TLS'}
assert external <= values.keys()
writes=[]
for key,(name,mode,uid) in entries.items():
 data=base64.b64decode(b['files'][key],validate=True)
 assert 0<len(data)<100000
 writes.append((name,data,mode,uid))
writes.append(('/etc/sub2api-external-runtime.env',('\n'.join(k+'='+values[k] for k in sorted(external))+'\n').encode(),0o600,0))
writes.append(('/etc/sub2api-candidate-app.env',('\n'.join(env)+'\n').encode(),0o600,0))
for name,data,mode,uid in writes:
 p=pathlib.Path(name)
 for parent in p.parents:
  assert not parent.is_symlink()
 assert not p.is_symlink()
 if p.exists():
  assert p.is_file() and p.read_bytes()==data, 'existing runtime differs; review required'
for name,data,mode,uid in writes:
 p=pathlib.Path(name)
 p.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
 if not p.exists():
  fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode)
  with os.fdopen(fd,'wb') as f:
   f.write(data); f.flush(); os.fsync(f.fileno())
 os.chmod(p,mode); os.chown(p,uid,1000 if name.endswith('internal-health-token') else uid)
print('candidate_runtime_injected=true files='+str(len(writes)))
'''


def run(command, data=None, timeout=180):
    result = subprocess.run(command, input=data, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError("bounded operation failed (output withheld)")
    return result.stdout


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["archive", "inject", "consume"])
    parser.add_argument("--item-id")
    parser.add_argument("--item-name", default=ITEM_NAME)
    parser.add_argument("--source-host", default=SOURCE)
    parser.add_argument("--target-host", default=TARGET)
    parser.add_argument("--target-proxy-jump")
    parser.add_argument("--expected-revision", default=REVISION)
    args = parser.parse_args()
    if args.action == "archive":
        items = json.loads(run([VAULT, "unlock-run", "--", "list-item-index"]))
        matches = [i for i in items if i["name"] == args.item_name]
        if len(matches) > 1:
            raise RuntimeError("duplicate runtime Vault items")
        if matches:
            print(json.dumps({"id": matches[0]["id"], "name": args.item_name, "reused": True}))
            return
        raw = run(SSH + [args.source_host, "sudo python3 - " + args.expected_revision], COLLECT.encode())
        bundle = json.loads(raw)
        if bundle.get("revision") != args.expected_revision:
            raise RuntimeError("source revision mismatch")
        request = {
            "type": "secure-note", "name": args.item_name,
            "folderId": "8bfa654b-cd70-42e3-9b68-021f04885ed9",
            "notes": "Owner-authorized AWS standby runtime migration. Source Azure Sub2API. Source revision " + args.expected_revision + ". Contains runtime configuration and certificate keys; rotate/reconcile on source credential change.",
            "fields": [{"name": "runtime_bundle", "type": "hidden", "value": json.dumps(bundle)}],
        }
        meta = json.loads(run([VAULT, "unlock-run", "--", "create-item"], json.dumps(request).encode()))
        print(json.dumps({"id": meta.get("id"), "name": args.item_name}))
    elif args.action == "inject":
        if not args.item_id:
            raise RuntimeError("exact Vault item ID required")
        path = Path(__file__).resolve()
        consume_args = ["consume", "--target-host", args.target_host,
                        "--expected-revision", args.expected_revision]
        if args.target_proxy_jump:
            consume_args.extend(["--target-proxy-jump", args.target_proxy_jump])
        request = {"itemId": args.item_id,
                   "env": [{"source": "field:runtime_bundle", "name": "SUB2_AWS_RUNTIME_BUNDLE"}],
                   "command": {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "args": consume_args},
                   "timeoutSeconds": 180}
        output = run([VAULT, "unlock-run", "--", "run-item-env"], json.dumps(request).encode(), 240)
        if b"candidate_runtime_injected=true" not in output:
            raise RuntimeError("injection acknowledgement missing")
        print("candidate_runtime_injected=true")
    else:
        import shlex
        raw = os.environ.pop("SUB2_AWS_RUNTIME_BUNDLE")
        json.loads(raw)
        target_ssh = list(SSH)
        if args.target_proxy_jump:
            target_ssh.extend(["-J", args.target_proxy_jump])
        remote = "sudo python3 -c " + shlex.quote(INSTALL) + " " + shlex.quote(args.expected_revision)
        result = run(target_ssh + [args.target_host, remote], raw.encode())
        if not result.startswith(b"candidate_runtime_injected=true"):
            raise RuntimeError("unexpected installation acknowledgement")
        print("candidate_runtime_injected=true")


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print("runtime migration failed; secret-bearing output withheld", file=sys.stderr)
        sys.exit(1)
