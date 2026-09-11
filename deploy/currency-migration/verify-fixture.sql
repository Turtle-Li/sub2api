DO $$
BEGIN
 IF (SELECT balance FROM users WHERE id=1) <> 675 THEN RAISE EXCEPTION 'wallet'; END IF;
 IF (SELECT balance FROM users WHERE id=2) <> 0.83333327 THEN RAISE EXCEPTION 'rounding'; END IF;
 IF (SELECT balance_notify_threshold FROM users WHERE id=2) <> 20 THEN RAISE EXCEPTION 'percentage changed'; END IF;
 IF (SELECT quota FROM api_keys WHERE id=1) <> 675 OR (SELECT usage_5h FROM api_keys WHERE id=1) <> 6.75 THEN RAISE EXCEPTION 'wallet keys'; END IF;
 IF (SELECT quota FROM api_keys WHERE id=2) <> 100 OR (SELECT usage_5h FROM api_keys WHERE id=2) <> 1 THEN RAISE EXCEPTION 'subscription keys changed'; END IF;
 IF (SELECT quota FROM api_keys WHERE id=3) <> 675 THEN RAISE EXCEPTION 'unbound wallet key'; END IF;
 IF (SELECT weekly_limit_usd FROM user_platform_quotas WHERE id=1) IS NOT NULL THEN RAISE EXCEPTION 'null limit'; END IF;
 IF (SELECT daily_usage_usd FROM user_platform_quotas WHERE id=1) <> 6.75 THEN RAISE EXCEPTION 'platform usage'; END IF;
 IF (SELECT value FROM redeem_codes WHERE id=1) <> 67.5 OR (SELECT value FROM redeem_codes WHERE id=2) <> 10 OR (SELECT value FROM redeem_codes WHERE id=3) <> 2 THEN RAISE EXCEPTION 'redeem scope'; END IF;
 IF (SELECT sum(amount) FROM payment_orders) <> 110 THEN RAISE EXCEPTION 'historical payment changed'; END IF;
 IF (SELECT actual_cost FROM batch_image_jobs WHERE id=1) <> 2 THEN RAISE EXCEPTION 'historical batch changed'; END IF;
 IF (SELECT rate_multiplier FROM groups WHERE id=1) <> .8 OR (SELECT daily_limit_usd FROM groups WHERE id=2) <> 100 THEN RAISE EXCEPTION 'discount or subscription changed'; END IF;
 IF (SELECT value FROM settings WHERE key='default_balance')::numeric <> 13.5 THEN RAISE EXCEPTION 'default balance'; END IF;
END $$;
