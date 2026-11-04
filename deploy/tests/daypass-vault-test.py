#!/usr/bin/env python3
"""Exercise the dedicated agent profile and stdin-only injection with fake Docker."""
import os
import json
from pathlib import Path
import subprocess
import tempfile

SCRIPT = Path(__file__).resolve().parents[1] / "sub2api-daypass-vault-container.sh"
IMAGE = "sub2api:prebuilt-" + "a" * 40
REF = "vault://secret/data/ops/newapi/daypass#bearer"


def check_release_contract(root):
    source = (SCRIPT.parent / "sub2api-blue-green-release.sh").read_text()
    start = source.index("container_matches_daypass_runtime() {")
    end = source.index("\n}\n", start) + 3
    function = source[start:end]
    environment = root / "app-env.json"
    mounts = root / "app.mounts"
    valid_env = "SHOP_DAYPASS_ENABLED=true\nSHOP_DAYPASS_PRODUCT_ID=8\n"
    valid_mount = "volume|sub2api_daypass_vault|/run/sub2api-daypass-vault|false\n"
    cases = [
        (True, valid_env, valid_mount, True),
        (True, valid_env.replace("=8", "=9"), valid_mount, False),
        (True, valid_env + "SHOP_DAYPASS_PRODUCT_ID=8\n", valid_mount, False),
        (True, valid_env + "SHOP_DAYPASS_BEARER=synthetic\n", valid_mount, False),
        (True, valid_env + "DAYPASS_SHOP_BEARER=synthetic\n", valid_mount, False),
        (True, valid_env, valid_mount.replace("|false", "|true"), False),
        (True, valid_env, valid_mount.replace("volume|sub2api_daypass_vault|", "volume|sub2api_feishu_vault|"), False),
        (True, valid_env, valid_mount * 2, False),
        (True, valid_env, "", False),
        (True, ["UNRELATED=prefix\nSHOP_DAYPASS_ENABLED=true\nSHOP_DAYPASS_PRODUCT_ID=8"], valid_mount, False),
        (True, ["SHOP_DAYPASS_ENABLED=true\nSHOP_DAYPASS_PRODUCT_ID=8"], valid_mount, False),
        (False, "", "", True),
        (False, "SHOP_DAYPASS_ENABLED=false\n", valid_mount, False),
        (False, valid_env, "", False),
    ]
    for enabled, env_value, mount_value, accepted in cases:
        environment.write_text(json.dumps(env_value.splitlines() if isinstance(env_value, str) else env_value))
        mounts.write_text(mount_value)
        shell = 'docker() { cat "$DAYPASS_CONTRACT_JSON"; }\n' + function + "\nSHOP_DAYPASS_ENABLED=" + str(enabled).lower() + "\n" + \
            "SHOP_DAYPASS_PRODUCT_ID=8\nDAYPASS_VAULT_VOLUME=sub2api_daypass_vault\n" + \
            "CONTAINER_DAYPASS_VAULT_PATH=/run/sub2api-daypass-vault\n" + \
            'container_matches_daypass_runtime "$1" "$2"\n'
        result = subprocess.run(["bash", "-c", shell, "contract", str(environment), str(mounts)],
            env={**os.environ, "DAYPASS_CONTRACT_JSON": str(environment)}, text=True, capture_output=True, timeout=5)
        assert (result.returncode == 0) == accepted, (enabled, env_value, mount_value, result.stderr)


def main():
    with tempfile.TemporaryDirectory(prefix="daypass-vault-test-") as directory:
        root = Path(directory).resolve()
        (root / "bin").mkdir()
        (root / "locks").mkdir(mode=0o700)
        realpath = root / "bin/realpath"
        realpath.write_text("#!/usr/bin/env python3\nimport pathlib,sys\nprint(pathlib.Path(sys.argv[-1]).resolve(strict=True))\n")
        realpath.chmod(0o700)
        flock = root / "bin/flock"
        flock.write_text("#!/usr/bin/env python3\nimport fcntl,sys\nfcntl.flock(int(sys.argv[-1]),fcntl.LOCK_EX|fcntl.LOCK_NB)\n")
        flock.chmod(0o700)
        config = root / "config.env"
        config.write_text("SHOP_DAYPASS_ENABLED=true\nSHOP_DAYPASS_PRODUCT_ID=8\n")
        config.chmod(0o600)
        docker = root / "bin/docker"
        docker.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
a=sys.argv[1:];root=Path(os.environ['DAYPASS_TEST_ROOT'])
with (root/'calls').open('a') as f:f.write(json.dumps(a)+'\\n')
image='sub2api:prebuilt-'+'a'*40
public='/run/sub2api-daypass-vault';admin=public+'-admin'
if a[0]=='image':
 fmt=a[-1]
 if 'Architecture' in fmt:print('linux/amd64')
 elif 'revision' in fmt:print('a'*40)
 elif 'source' in fmt:print('https://github.com/Turtle-Li/sub2api')
 elif 'version' in fmt:print('test-build')
 else:sys.exit(1)
elif 'inspect' in a:
 if not (root/'created').exists():sys.exit(1)
 if '--format' not in a:sys.exit(0)
 fmt=a[-1]
 values={'{{.Config.Image}}':image,'{{.State.Running}}':'true','{{.Config.User}}':'1000:1000','{{.HostConfig.NetworkMode}}':'none','{{.HostConfig.ReadonlyRootfs}}':'true','{{.HostConfig.RestartPolicy.Name}}':'unless-stopped','{{.HostConfig.PidsLimit}}':'64','{{.HostConfig.Init}}':'true','{{json .HostConfig.CapDrop}}':'["ALL"]','{{json .HostConfig.SecurityOpt}}':'["no-new-privileges"]','{{.State.Health.Status}}':'healthy' if (root/'loaded').exists() else 'unhealthy'}
 values.update({'{{json .Config.Entrypoint}}':'["/app/docker-entrypoint.sh"]','{{json .HostConfig.CapAdd}}':'null','{{.HostConfig.Privileged}}':'false'})
 drift=os.environ.get('DAYPASS_TEST_PROFILE_FIELD','')
 if drift in values:values[drift]='unsafe'
 if fmt in values:print(values[fmt])
 elif '.HostConfig.Tmpfs' in fmt:
  print(admin+'|rw,noexec,nosuid,nodev,size=1m,mode=0700,uid=1000,gid=1000\\n/tmp|rw,noexec,nosuid,nodev,size=4m,mode=0700,uid=1000,gid=1000')
  if drift=='tmpfs':print('/extra|rw')
 elif 'range .Mounts' in fmt:print('volume|sub2api_daypass_vault|'+public+'|true')
 elif '.Config.Cmd' in fmt:
  cmd=['/app/sub2api-vault-agent','serve','--public-socket',public+'/public.sock','--admin-socket',admin+'/admin.sock','--allowed-ref','vault://secret/data/ops/newapi/daypass#bearer']
  if os.environ.get('DAYPASS_TEST_BAD_PROFILE')=='1':cmd+=['--allowed-ref','vault://secret/data/ops/feishu/payment#webhook_url']
  print(json.dumps(cmd,separators=(',',':')))
 elif 'Healthcheck.Test' in fmt:print(json.dumps(['CMD-SHELL','/app/sub2api-vault-agent check --public-socket '+public+'/public.sock'],separators=(',',':')))
 else:sys.exit(1)
elif a[0]=='run':
 if '--name' in a:(root/'created').touch()
elif a[0]=='exec':
 if 'load' in a:
  value=sys.stdin.read()
  if len(value)!=64:sys.exit(1)
  (root/'loaded').touch()
 elif not (root/'loaded').exists():sys.exit(1)
elif a[:2]!=['volume','create']:sys.exit(1)
''')
        docker.chmod(0o700)
        environment = os.environ.copy()
        environment.update(PATH=str(root / "bin") + os.pathsep + environment["PATH"],
            DAYPASS_TEST_ROOT=str(root), SUB2API_MAINTENANCE_LOCK_ALLOW_NON_ROOT_FOR_TESTS="1",
            SUB2API_MAINTENANCE_LOCK_FILE=str(root / "locks/maintenance.lock"),
            SUB2API_AUTODEPLOY_CONFIG_FILE=str(config))

        def run(action, stdin="", expected=0):
            result = subprocess.run(["bash", str(SCRIPT), action, IMAGE], env=environment,
                input=stdin, text=True, capture_output=True, timeout=20)
            assert (result.returncode == 0) == (expected == 0), (action, result.stdout, result.stderr)
            assert "d" * 64 not in result.stdout + result.stderr

        run("prepare")
        run("ready", expected=1)
        run("load", "d" * 64 + "\n")
        run("ready")
        run("load", "d" * 64 + "\nextra\n", expected=1)
        run("load", "invalid\n", expected=1)
        environment["DAYPASS_TEST_BAD_PROFILE"] = "1"
        run("ready", expected=1)
        del environment["DAYPASS_TEST_BAD_PROFILE"]
        for field in ["{{json .Config.Entrypoint}}", "{{json .HostConfig.CapAdd}}", "{{.HostConfig.Privileged}}", "tmpfs"]:
            environment["DAYPASS_TEST_PROFILE_FIELD"] = field
            run("ready", expected=1)
        del environment["DAYPASS_TEST_PROFILE_FIELD"]
        assert "d" * 64 not in (root / "calls").read_text()
        for field in ["SHOP_DAYPASS_BEARER", "DAYPASS_SHOP_BEARER"]:
            environment[field] = "d" * 64
            run("ready", expected=1)
            del environment[field]
        ssh = root / "bin/ssh"
        ssh.write_text("#!/usr/bin/env python3\nimport os,sys\nassert 'DAYPASS_SHOP_BEARER' not in os.environ\nassert 'SHOP_DAYPASS_BEARER' not in os.environ\nassert 'd'*64 not in str(sys.argv)\nassert sys.stdin.read()=='d'*64+'\\n'\n")
        ssh.chmod(0o700)
        environment["DAYPASS_SHOP_BEARER"] = "d" * 64
        environment["SHOP_DAYPASS_BEARER"] = "d" * 64
        injected = subprocess.run(["bash", str(SCRIPT.parent / "sub2api-daypass-vault-inject.sh"), IMAGE],
            env=environment, text=True, capture_output=True, timeout=5)
        assert injected.returncode == 0, injected.stderr
        assert "d" * 64 not in injected.stdout + injected.stderr
        check_release_contract(root)
        print("PASS dedicated exact ref, locked readiness, stdin-only load, bad profile and raw-env rejection")
        print("PASS release disabled/enabled, exact SKU, no raw bearer, single read-only dedicated mount")


if __name__ == "__main__":
    main()
