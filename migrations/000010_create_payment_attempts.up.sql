CREATE TABLE payment_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    payment_id UUID NOT NULL REFERENCES payments(id),
    provider TEXT NOT NULL,
    provider_payment_id TEXT NOT NULL,

    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL,

    status TEXT NOT NULL
        CHECK (status IN ('created', 'failed', 'authorized', 'captured')),

    failure_code TEXT,
    failure_description TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT payment_attempts_provider_payment_unique
        UNIQUE (provider, provider_payment_id)
);

CREATE INDEX payment_attempts_payment_id_idx
    ON payment_attempts(payment_id);
