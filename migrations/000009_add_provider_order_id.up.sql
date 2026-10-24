ALTER TABLE payments
ADD COLUMN provider_order_id TEXT;

CREATE UNIQUE INDEX payments_provider_order_unique_idx
ON payments (provider, provider_order_id)
WHERE provider_order_id IS NOT NULL;