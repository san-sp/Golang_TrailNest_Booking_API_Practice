CREATE TABLE payment_refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    payment_id UUID NOT NULL REFERENCES payments(id),
    provider TEXT NOT NULL,
    provider_payment_id TEXT NOT NULL,
    provider_refund_id TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,

    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL DEFAULT 'INR',

    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (
            status IN (
                'pending',
                'processing',
                'succeeded',
                'failed',
                'review_required'
            )
        ),

    attempt_count INTEGER NOT NULL DEFAULT 0
        CHECK (attempt_count >= 0),

    claimed_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX payment_refunds_provider_refund_unique_idx
    ON payment_refunds (provider, provider_refund_id)
    WHERE provider_refund_id IS NOT NULL;

CREATE INDEX payment_refunds_status_created_idx
    ON payment_refunds (status, created_at)
    WHERE status IN ('pending', 'processing', 'review_required');

-- This workflow supports one full-refund record per payment.
-- Prevents separate refund records from being created for the same payment.
CREATE UNIQUE INDEX payment_refunds_payment_id_unique_idx
    ON payment_refunds (payment_id);
