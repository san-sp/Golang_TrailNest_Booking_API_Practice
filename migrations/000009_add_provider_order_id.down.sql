DROP INDEX IF EXISTS payments_provider_order_unique_idx;

ALTER TABLE payments
DROP COLUMN IF EXISTS provider_order_id;