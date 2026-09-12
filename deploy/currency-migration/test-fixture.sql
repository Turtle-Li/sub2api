-- Synthetic money only. No production data or credentials.
CREATE TABLE users(id bigint PRIMARY KEY,balance numeric(20,8),frozen_balance numeric(20,8),total_recharged numeric(20,8),balance_notify_threshold numeric(20,8),balance_notify_threshold_type text);
CREATE TABLE groups(id bigint PRIMARY KEY,subscription_type text,rate_multiplier numeric,daily_limit_usd numeric);
CREATE TABLE api_keys(id bigint PRIMARY KEY,group_id bigint,quota numeric(20,8),quota_used numeric(20,8),rate_limit_5h numeric(20,8),rate_limit_1d numeric(20,8),rate_limit_7d numeric(20,8),usage_5h numeric(20,8),usage_1d numeric(20,8),usage_7d numeric(20,8));
CREATE TABLE user_platform_quotas(id bigint PRIMARY KEY,daily_limit_usd numeric(20,8),weekly_limit_usd numeric(20,8),monthly_limit_usd numeric(20,8),daily_usage_usd numeric(20,8),weekly_usage_usd numeric(20,8),monthly_usage_usd numeric(20,8));
CREATE TABLE user_affiliates(user_id bigint PRIMARY KEY,aff_quota numeric(20,8),aff_frozen_quota numeric(20,8),aff_history_quota numeric(20,8));
CREATE TABLE user_affiliate_ledger(id bigint,amount numeric,frozen_until timestamptz);
CREATE TABLE redeem_codes(id bigint PRIMARY KEY,type text,status text,code text,value numeric(20,8));
CREATE TABLE promo_codes(id bigint PRIMARY KEY,bonus_amount numeric(20,8));
CREATE TABLE settings(id bigserial PRIMARY KEY,key varchar(100) UNIQUE,value text NOT NULL,updated_at timestamptz DEFAULT now());
CREATE TABLE payment_orders(id bigint,order_type text,status text,recharge_code text,amount numeric,pay_amount numeric);
CREATE TABLE batch_image_jobs(id bigint,status text,currency text,actual_cost numeric);
INSERT INTO users VALUES (1,100,0,120,10,'fixed'),(2,0.12345678,0,0,20,'percentage');
INSERT INTO groups VALUES(1,'standard',0.8,NULL),(2,'subscription',0.6,100);
INSERT INTO api_keys VALUES(1,1,100,5,10,20,30,1,2,3),(2,2,100,5,10,20,30,1,2,3),(3,NULL,100,5,10,20,30,1,2,3);
INSERT INTO user_platform_quotas VALUES(1,10,NULL,100,1,2,3);
INSERT INTO user_affiliates VALUES(1,5,0,10);
INSERT INTO redeem_codes VALUES(1,'balance','unused','synthetic-unused',10),(2,'balance','used','synthetic-used',10),(3,'concurrency','unused','synthetic-concurrency',2);
INSERT INTO promo_codes VALUES(1,5);
INSERT INTO settings(key,value) VALUES('default_balance','2'),('balance_low_notify_threshold','10');
INSERT INTO payment_orders VALUES(1,'balance','REFUNDED',NULL,10,10),(2,'subscription','COMPLETED',NULL,100,100);
INSERT INTO batch_image_jobs VALUES(1,'completed','USD',2);

CREATE TABLE subscription_plans(id bigint,entitlements jsonb);
INSERT INTO settings(key,value) VALUES('BALANCE_RECHARGE_MULTIPLIER','1'),('PAYMENT_RECHARGE_OPTIONS','[{"amount":99,"balance_bonus":4,"label":"synthetic"}]');
