#!/usr/bin/env bash
# Disposable local PostgreSQL evidence for the restrictive forward-pause CAS.
set -Eeuo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
sql_file="${script_dir}/../sub2api-reviewed-refunds-forward-pause.sql"
test_container="sub2api-forward-pause-test-$$"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-forward-pause-pg.XXXXXX")"
cleanup() {
  docker rm -f "$test_container" >/dev/null 2>&1 || true
  rm -rf -- "$test_dir"
}
trap cleanup EXIT
image="${SUB2API_TEST_POSTGRES_IMAGE:-postgres:18.1-alpine3.23}"
docker run -d --name "$test_container" --network none -e POSTGRES_HOST_AUTH_METHOD=trust "$image" >/dev/null
ready=false
for _ in $(seq 1 30); do
  if docker exec "$test_container" pg_isready -U postgres >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[ "$ready" = true ]
pg() { docker exec -i "$test_container" psql -XqAt -U postgres -d postgres -v ON_ERROR_STOP=1 "$@"; }
pg <<'SQL'
CREATE TABLE settings(key text PRIMARY KEY, value text NOT NULL, updated_at timestamptz DEFAULT now());
CREATE TABLE unified_payment_refund_attempts(entitlement_reserved boolean,quote_revision text,refund_kind text);
INSERT INTO settings(key,value) VALUES('PAYMENT_REVIEWED_REFUNDS_ENABLED','true'),('unrelated','keep');
SQL
pg < "$sql_file" > "$test_dir/success"
rg -qx DB_CAS_FALSE_CONFIRMED "$test_dir/success"
[ "$(pg -c "SELECT value FROM settings WHERE key='PAYMENT_REVIEWED_REFUNDS_ENABLED'")" = false ]
[ "$(pg -c "SELECT value FROM settings WHERE key='unrelated'")" = keep ]
if pg < "$sql_file" > "$test_dir/replay" 2>&1; then echo 'replayed restrictive CAS unexpectedly succeeded'; exit 1; fi
if rg -q DB_CAS_FALSE_CONFIRMED "$test_dir/replay"; then echo 'failed CAS emitted acknowledgement'; exit 1; fi
pg -c "UPDATE settings SET value='true' WHERE key='PAYMENT_REVIEWED_REFUNDS_ENABLED'; INSERT INTO unified_payment_refund_attempts VALUES(true,'quote','balance')"
if pg < "$sql_file" > "$test_dir/pending" 2>&1; then echo 'pending refund bypassed guard'; exit 1; fi
[ "$(pg -c "SELECT value FROM settings WHERE key='PAYMENT_REVIEWED_REFUNDS_ENABLED'")" = true ]
pg -c 'TRUNCATE unified_payment_refund_attempts'
python3 - "$test_container" "$sql_file" <<'PY'
import subprocess,sys,time
container,sql=sys.argv[1:]
cmd=['docker','exec','-i',container,'psql','-XqAt','-U','postgres','-d','postgres','-v','ON_ERROR_STOP=1']
locker=subprocess.Popen(cmd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
locker.stdin.write("BEGIN; SELECT pg_advisory_xact_lock_shared(91625647919686); INSERT INTO unified_payment_refund_attempts VALUES(true,'quote','subscription'); SELECT 'HELD';\n")
locker.stdin.flush()
while True:
 line=locker.stdout.readline()
 assert line, 'locker exited before shared lock'
 if line.strip()=='HELD': break
with open(sql) as source:
 pause=subprocess.Popen(cmd,stdin=source,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
deadline=time.monotonic()+8
while True:
 count=subprocess.check_output(cmd+['-c',"SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted"],text=True).strip()
 if int(count)>0:break
 assert pause.poll() is None and time.monotonic()<deadline,'CAS did not wait for shared reservation lock'
locker.stdin.write('COMMIT;\n\\q\n');locker.stdin.flush()
locker.wait(timeout=8)
output,error=pause.communicate(timeout=10)
assert pause.returncode!=0 and 'DB_CAS_FALSE_CONFIRMED' not in output
assert 'reviewed refund reservations remain' in error
flag=subprocess.check_output(cmd+['-c',"SELECT value FROM settings WHERE key='PAYMENT_REVIEWED_REFUNDS_ENABLED'"],text=True).strip()
assert flag=='true'
print('FORWARD_PAUSE_ADVISORY_RACE_PASS')
PY
pg -c "TRUNCATE unified_payment_refund_attempts; CREATE TABLE payment_refund_benefit_sources(state text); INSERT INTO payment_refund_benefit_sources VALUES('RESERVED')"
if pg < "$sql_file" > "$test_dir/benefit-pending" 2>&1; then echo 'benefit hold bypassed pause guard'; exit 1; fi
[ "$(pg -c "SELECT value FROM settings WHERE key='PAYMENT_REVIEWED_REFUNDS_ENABLED'")" = true ]
printf 'Forward-pause PostgreSQL checks passed.\n'
