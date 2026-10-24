DROP INDEX IF EXISTS payments_fulfillment_status_idx;

ALTER TABLE payments
DROP COLUMN IF EXISTS fulfillment_status;
