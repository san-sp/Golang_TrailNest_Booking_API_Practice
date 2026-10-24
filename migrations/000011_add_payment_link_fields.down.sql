DROP INDEX IF EXISTS payments_provider_payment_link_unique_idx;

ALTER TABLE payments
    DROP COLUMN IF EXISTS provider_payment_link_url,
    DROP COLUMN IF EXISTS provider_payment_link_id;
