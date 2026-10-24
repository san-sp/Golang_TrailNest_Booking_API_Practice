DROP INDEX IF EXISTS bookings_reservation_id_unique_idx;

ALTER TABLE bookings
    DROP COLUMN IF EXISTS reservation_id;
