CREATE TABLE payment_webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    provider_event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payment_id UUID REFERENCES payments(id),
    status TEXT NOT NULL DEFAULT 'processing'
        CHECK (status IN ('processing', 'processed', 'failed')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    CONSTRAINT payment_webhook_events_provider_event_unique
        UNIQUE (provider, provider_event_id)
);

CREATE INDEX payment_webhook_events_payment_id_idx
    ON payment_webhook_events(payment_id);
