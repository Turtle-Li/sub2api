#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
migration="${INVOICE_MIGRATION_SQL:-$root/backend/migrations/239_payment_invoice_requests.sql}"
container="sub2-invoice-migration-test-$$"
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$container" --network none -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18-alpine >/dev/null
ready=false
for _ in $(seq 1 30); do
  if docker exec "$container" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
$ready || { echo 'isolated PostgreSQL did not become ready' >&2; exit 1; }
psql() { docker exec -i "$container" psql -X -h 127.0.0.1 -U postgres -v ON_ERROR_STOP=1 "$@"; }
psql <<'SQL'
CREATE TABLE payment_orders(id BIGINT PRIMARY KEY, status TEXT, amount NUMERIC(20,8), pay_amount NUMERIC(20,2), product_snapshot JSONB);
INSERT INTO payment_orders VALUES (1,'COMPLETED',105,100,'{"name":"Purchased plan"}'),(2,'REFUNDED',1,1,'{}'),(3,'COMPLETED',2,2,'{}');
CREATE TABLE original_order_evidence AS TABLE payment_orders;
SQL
psql < "$migration"
psql < "$migration"
psql <<'SQL'
INSERT INTO payment_invoice_requests(order_id,user_id,title_type,title,recipient_email,amount,currency)
VALUES(1,71,'personal','Invoice owner','buyer@example.test',100,'CNY');
DO $$
DECLARE invoice_id BIGINT;
BEGIN
  SELECT id INTO invoice_id FROM payment_invoice_requests WHERE order_id=1;
  IF EXISTS ((TABLE payment_orders EXCEPT TABLE original_order_evidence) UNION ALL (TABLE original_order_evidence EXCEPT TABLE payment_orders)) THEN
    RAISE EXCEPTION 'migration rewrote payment evidence';
  END IF;
  BEGIN
    INSERT INTO payment_invoice_requests(order_id,user_id,title_type,title,recipient_email,amount,currency) VALUES(1,71,'personal','Duplicate','buyer@example.test',100,'CNY');
    RAISE EXCEPTION 'duplicate order accepted';
  EXCEPTION WHEN unique_violation THEN NULL; END;
  BEGIN
    DELETE FROM payment_orders WHERE id=1;
    RAISE EXCEPTION 'financial order with invoice was deletable';
  EXCEPTION WHEN foreign_key_violation OR restrict_violation THEN NULL; END;
  BEGIN
    UPDATE payment_invoice_requests SET amount=0 WHERE id=invoice_id;
    RAISE EXCEPTION 'zero amount accepted';
  EXCEPTION WHEN check_violation THEN NULL; END;
  BEGIN
    UPDATE payment_invoice_requests SET title_type='enterprise',tax_identifier='' WHERE id=invoice_id;
    RAISE EXCEPTION 'enterprise without tax identifier accepted';
  EXCEPTION WHEN check_violation THEN NULL; END;
  BEGIN
    UPDATE payment_invoice_requests SET email_delivery_status='UNKNOWN' WHERE id=invoice_id;
    RAISE EXCEPTION 'invalid email state accepted';
  EXCEPTION WHEN check_violation THEN NULL; END;
  BEGIN
    UPDATE payment_invoice_requests SET feishu_notification_attempts=-1 WHERE id=invoice_id;
    RAISE EXCEPTION 'negative notification attempt accepted';
  EXCEPTION WHEN check_violation THEN NULL; END;
  INSERT INTO payment_invoice_documents(invoice_request_id,filename,size_bytes,sha256,data)
  VALUES(invoice_id,'official.pdf',8,repeat('a',64),decode('255044462d312e37','hex'));
  BEGIN
    INSERT INTO payment_invoice_documents(invoice_request_id,filename,size_bytes,sha256,data)
    VALUES(invoice_id,'duplicate.pdf',8,repeat('b',64),decode('255044462d312e37','hex'));
    RAISE EXCEPTION 'duplicate invoice document accepted';
  EXCEPTION WHEN unique_violation THEN NULL; END;
  BEGIN
    INSERT INTO payment_invoice_requests(order_id,user_id,title_type,title,recipient_email,amount,currency)
    VALUES(3,72,'personal','Rollback owner','rollback@example.test',2,'CNY') RETURNING id INTO invoice_id;
    INSERT INTO payment_invoice_documents(invoice_request_id,filename,size_bytes,sha256,data)
    VALUES(invoice_id,'rollback.pdf',8,repeat('c',64),decode('255044462d312e37','hex'));
    RAISE EXCEPTION USING ERRCODE='P1234',MESSAGE='simulate failure before commit';
  EXCEPTION WHEN SQLSTATE 'P1234' THEN NULL; END;
  IF EXISTS(SELECT FROM payment_invoice_requests WHERE order_id=3) OR (SELECT count(*) FROM payment_invoice_documents) <> 1 THEN
    RAISE EXCEPTION 'invoice and document did not roll back together';
  END IF;
END $$;
SELECT 'invoice migration invariants passed' AS result;
SQL
