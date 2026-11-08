-- Catalog-only addition. Purchased order snapshots remain immutable.
ALTER TABLE shop_products ADD COLUMN IF NOT EXISTS sku_config JSONB;
