-- ONLY before reopening writers. Never use against post-cutover live traffic.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
SELECT pg_advisory_xact_lock(20260912,675);
LOCK TABLE users, api_keys, user_platform_quotas, user_affiliates,
  redeem_codes, promo_codes, settings IN SHARE ROW EXCLUSIVE MODE;
DO $$
DECLARE item record; pk text; changed bigint;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM currency_cutover_20260912.completed) THEN
    RAISE EXCEPTION 'Missing successful cutover marker';
  END IF;
  IF (SELECT value::jsonb->>'settlement_currency' FROM settings WHERE key='pricing_currency_settings') IS DISTINCT FROM 'CNY' THEN
    RAISE EXCEPTION 'Wallet is no longer CNY';
  END IF;
  FOR item IN SELECT DISTINCT table_name,column_name FROM currency_cutover_20260912.amounts LOOP
    pk := CASE WHEN item.table_name='user_affiliates' THEN 'user_id' ELSE 'id' END;
    EXECUTE format('SELECT count(*) FROM currency_cutover_20260912.amounts a LEFT JOIN public.%I t ON t.%I=a.row_id WHERE a.table_name=%L AND a.column_name=%L AND (t.%I IS NULL OR t.%I IS DISTINCT FROM a.new_value)',item.table_name,pk,item.table_name,item.column_name,pk,item.column_name) INTO changed;
    IF changed <> 0 THEN RAISE EXCEPTION 'Post-cutover changes detected for %.%; reconcile instead of rollback',item.table_name,item.column_name; END IF;
    EXECUTE format('UPDATE public.%I t SET %I=a.old_value FROM currency_cutover_20260912.amounts a WHERE a.table_name=%L AND a.column_name=%L AND t.%I=a.row_id',item.table_name,item.column_name,item.table_name,item.column_name,pk);
  END LOOP;
END $$;
UPDATE settings s SET value=b.value,updated_at=now()
FROM currency_cutover_20260912.settings_before b WHERE b.key=s.key;
DELETE FROM settings WHERE key='pricing_currency_settings'
  AND NOT EXISTS (SELECT 1 FROM currency_cutover_20260912.settings_before WHERE key='pricing_currency_settings');
ALTER TABLE currency_cutover_20260912.completed ADD COLUMN rolled_back_at timestamptz;
UPDATE currency_cutover_20260912.completed SET rolled_back_at=now();
COMMIT;
