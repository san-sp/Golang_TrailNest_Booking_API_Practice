CREATE TABLE wishlist_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT wishlist_items_customer_activity_unique
        UNIQUE (customer_id, activity_id)
);

CREATE INDEX wishlist_items_customer_created_idx
    ON wishlist_items (customer_id, created_at DESC);

CREATE INDEX wishlist_items_activity_id_idx
    ON wishlist_items (activity_id);
