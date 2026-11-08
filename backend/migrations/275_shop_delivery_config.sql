-- Optional presentation settings for future manual account-delivery purchases.
-- Existing order snapshots, delivery ciphertext and financial records are unchanged.
ALTER TABLE shop_products ADD COLUMN IF NOT EXISTS delivery_config JSONB;
