ALTER TABLE activities
ADD COLUMN price_minor BIGINT NOT NULL DEFAULT 0
    CHECK (price_minor >= 0),
ADD COLUMN currency CHAR(3) NOT NULL DEFAULT 'INR'
    CHECK (currency ~ '^[A-Z]{3}$');
