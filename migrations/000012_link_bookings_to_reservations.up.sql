-- Link payment-confirmed bookings to their originating reservations.
-- Direct bookings may continue to have a NULL reservation_id.
ALTER TABLE bookings
    ADD COLUMN reservation_id UUID REFERENCES reservations(id);

-- A reservation can produce at most one booking.
CREATE UNIQUE INDEX bookings_reservation_id_unique_idx
    ON bookings (reservation_id)
    WHERE reservation_id IS NOT NULL;
