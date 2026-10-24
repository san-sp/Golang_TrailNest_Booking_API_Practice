
-- Step 5: Roll back the core TrailNest schema.
-- Dropping bookings first respects its foreign-key dependencies.

DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS activities;
DROP TABLE IF EXISTS users;
