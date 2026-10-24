-- Store hosted Razorpay Payment Links separately from Razorpay Order IDs.
ALTER TABLE payments
    ADD COLUMN provider_payment_link_id TEXT,
    ADD COLUMN provider_payment_link_url TEXT;

-- A provider Payment Link must belong to at most one local payment.
CREATE UNIQUE INDEX payments_provider_payment_link_unique_idx
    ON payments (provider, provider_payment_link_id)
    WHERE provider_payment_link_id IS NOT NULL;
