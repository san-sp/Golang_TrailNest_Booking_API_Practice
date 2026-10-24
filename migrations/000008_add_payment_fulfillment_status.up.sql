ALTER TABLE payments
ADD COLUMN fulfillment_status TEXT NOT NULL DEFAULT 'pending'
CHECK (
    fulfillment_status IN (
        'pending',
        'fulfilled',
        'refund_required',
        'review_required'
    )
);

CREATE INDEX payments_fulfillment_status_idx
ON payments (fulfillment_status)
WHERE fulfillment_status IN ('refund_required', 'review_required');
