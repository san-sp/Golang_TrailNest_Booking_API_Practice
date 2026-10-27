ALTER TABLE payments
DROP CONSTRAINT payments_fulfillment_status_check;

ALTER TABLE payments
ADD CONSTRAINT payments_fulfillment_status_check
CHECK (
    fulfillment_status IN (
        'pending',
        'fulfilled',
        'refund_required',
        'review_required'
    )
);
