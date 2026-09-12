-- Run only after the reviewed release and verified backup, following ONLINE_CUTOVER_20260912.md.
-- psql -X -v ON_ERROR_STOP=1 -v recharge_factor=1 -v apply=false -f wallet-to-cny.sql rehearses and rolls back.
-- Setting apply=true is the explicit cutover. Do not use as a startup migration.
\set ON_ERROR_STOP on
\if :{?apply}
\else
\set apply false
\endif
\if :{?recharge_factor}
\else
\set recharge_factor 0
\endif
BEGIN;
CREATE TEMP TABLE cutover_parameters AS SELECT :'recharge_factor'::numeric AS recharge_factor;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
SELECT pg_advisory_xact_lock(20260912, 675);
LOCK TABLE users, api_keys, user_platform_quotas, user_affiliates,
  user_affiliate_ledger, redeem_codes, promo_codes, settings,
  payment_orders, batch_image_jobs, subscription_plans IN SHARE ROW EXCLUSIVE MODE;
DO $$
BEGIN
  IF (SELECT recharge_factor FROM cutover_parameters) NOT IN (1,6.75) THEN
    RAISE EXCEPTION 'Explicit recharge_factor=1 or 6.75 requires owner decision';
  END IF;
  IF EXISTS (SELECT 1 FROM user_affiliate_ledger) THEN
    RAISE EXCEPTION 'Historical affiliate ledger needs separate currency treatment';
  END IF;
  IF EXISTS (SELECT 1 FROM subscription_plans WHERE COALESCE((entitlements->>'balance_bonus')::numeric,0) <> 0) THEN
    RAISE EXCEPTION 'Plan bonus requires an explicit migration decision';
  END IF;
  IF COALESCE((SELECT value::jsonb->>'settlement_currency' FROM settings WHERE key='pricing_currency_settings'), 'USD') <> 'USD' THEN
    RAISE EXCEPTION 'Source wallet currency must be USD; migration may already have run';
  END IF;
  IF EXISTS (SELECT 1 FROM users WHERE frozen_balance <> 0) THEN
    RAISE EXCEPTION 'Frozen wallets must be drained first';
  END IF;
  IF EXISTS (SELECT 1 FROM batch_image_jobs WHERE status NOT IN ('completed','output_deleted','cancelled','failed')) THEN
    RAISE EXCEPTION 'Unfinished image batches exist';
  END IF;
  -- This packet intentionally does not implement legacy refundable credit conversion.
  IF EXISTS (SELECT 1 FROM payment_orders WHERE order_type='balance' AND status <> 'REFUNDED') THEN
    RAISE EXCEPTION 'Legacy balance orders require reconciliation before this cutover';
  END IF;
  IF EXISTS (SELECT 1 FROM payment_orders WHERE status NOT IN ('COMPLETED','REFUNDED','EXPIRED','CANCELLED')) THEN
    RAISE EXCEPTION 'Unfinished payment/refund work exists';
  END IF;
  IF EXISTS (SELECT 1 FROM user_affiliate_ledger WHERE frozen_until IS NOT NULL)
     OR EXISTS (SELECT 1 FROM user_affiliates WHERE aff_frozen_quota <> 0) THEN
    RAISE EXCEPTION 'Frozen affiliate entries require reconciliation';
  END IF;
  IF EXISTS (SELECT 1 FROM redeem_codes r JOIN payment_orders p ON p.recharge_code=r.code WHERE r.type='balance' AND r.status='unused') THEN
    RAISE EXCEPTION 'Unused payment-bound redeem codes require reconciliation';
  END IF;
END $$;

CREATE SCHEMA currency_cutover_20260912;
REVOKE ALL ON SCHEMA currency_cutover_20260912 FROM PUBLIC;
CREATE TABLE currency_cutover_20260912.amounts (
  table_name text NOT NULL, row_id bigint NOT NULL, column_name text NOT NULL,
  old_value numeric, new_value numeric,
  PRIMARY KEY (table_name,row_id,column_name)
);
CREATE TABLE currency_cutover_20260912.settings_before AS
SELECT key,value FROM settings WHERE key IN (
  'pricing_currency_settings','PAYMENT_RECHARGE_OPTIONS','BALANCE_RECHARGE_MULTIPLIER','default_balance','balance_low_notify_threshold',
  'auth_source_default_email_balance','auth_source_default_linuxdo_balance',
  'auth_source_default_oidc_balance','auth_source_default_wechat_balance',
  'auth_source_default_github_balance','auth_source_default_google_balance',
  'auth_source_default_dingtalk_balance');

-- This helper accepts only hard-coded identifiers/predicates from this file.
-- It snapshots money and numeric IDs, never API keys, redeem codes or secrets.
CREATE FUNCTION pg_temp.convert_money(tbl text, pk text, cols text[], predicate text DEFAULT 'true')
RETURNS void LANGUAGE plpgsql AS $$
DECLARE col text; mismatches bigint;
BEGIN
  FOREACH col IN ARRAY cols LOOP
    EXECUTE format('INSERT INTO currency_cutover_20260912.amounts SELECT %L,%I,%L,%I,round(%I*6.75,8) FROM public.%I WHERE %s',tbl,pk,col,col,col,tbl,predicate);
    EXECUTE format('UPDATE public.%I t SET %I=a.new_value FROM currency_cutover_20260912.amounts a WHERE a.table_name=%L AND a.column_name=%L AND t.%I=a.row_id',tbl,col,tbl,col,pk);
    EXECUTE format('SELECT count(*) FROM public.%I t JOIN currency_cutover_20260912.amounts a ON a.row_id=t.%I AND a.table_name=%L AND a.column_name=%L WHERE t.%I IS DISTINCT FROM a.new_value',tbl,pk,tbl,col,col) INTO mismatches;
    IF mismatches <> 0 THEN RAISE EXCEPTION 'Verification failed for %.%',tbl,col; END IF;
  END LOOP;
END $$;
SELECT pg_temp.convert_money('users','id',ARRAY['balance','frozen_balance','total_recharged']);
SELECT pg_temp.convert_money('users','id',ARRAY['balance_notify_threshold'],'balance_notify_threshold_type=''fixed''');
SELECT pg_temp.convert_money('api_keys','id',ARRAY['quota','quota_used','rate_limit_5h','rate_limit_1d','rate_limit_7d','usage_5h','usage_1d','usage_7d'],
  'NOT EXISTS (SELECT 1 FROM groups g WHERE g.id=api_keys.group_id AND g.subscription_type=''subscription'')');
SELECT pg_temp.convert_money('user_platform_quotas','id',ARRAY['daily_limit_usd','weekly_limit_usd','monthly_limit_usd','daily_usage_usd','weekly_usage_usd','monthly_usage_usd']);
SELECT pg_temp.convert_money('user_affiliates','user_id',ARRAY['aff_quota','aff_frozen_quota','aff_history_quota']);
SELECT pg_temp.convert_money('redeem_codes','id',ARRAY['value'],'type=''balance'' AND status=''unused''');
SELECT pg_temp.convert_money('promo_codes','id',ARRAY['bonus_amount']);
UPDATE settings SET value=round(value::numeric*6.75,8)::text, updated_at=now()
WHERE key IN (SELECT key FROM currency_cutover_20260912.settings_before WHERE key NOT IN ('pricing_currency_settings','PAYMENT_RECHARGE_OPTIONS','BALANCE_RECHARGE_MULTIPLIER'));
-- Retail principal/bonus policy is explicit, separate from existing wallets.
UPDATE settings SET value=round(value::numeric*(SELECT recharge_factor FROM cutover_parameters),8)::text,updated_at=now()
WHERE key='BALANCE_RECHARGE_MULTIPLIER';
UPDATE settings s SET value=(
 SELECT COALESCE(jsonb_agg(jsonb_set(item,'{balance_bonus}',to_jsonb(round(COALESCE((item->>'balance_bonus')::numeric,0)*(SELECT recharge_factor FROM cutover_parameters),8)),true) ORDER BY ordinal),'[]'::jsonb)::text
 FROM jsonb_array_elements(s.value::jsonb) WITH ORDINALITY AS entry(item,ordinal)
),updated_at=now() WHERE key='PAYMENT_RECHARGE_OPTIONS';
INSERT INTO settings (key,value,updated_at)
VALUES ('pricing_currency_settings','{"settlement_currency":"CNY","usd_to_cny_rate":6.75}',now())
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at;
CREATE TABLE currency_cutover_20260912.completed AS
SELECT now() AS migrated_at, 6.75::numeric AS exchange_rate, 'USD'::text AS source_currency, 'CNY'::text AS target_currency;
SELECT table_name,column_name,count(*) AS rows,sum(old_value) AS before,sum(new_value) AS after
FROM currency_cutover_20260912.amounts GROUP BY table_name,column_name ORDER BY table_name,column_name;
\if :apply
COMMIT;
\else
ROLLBACK;
\endif
