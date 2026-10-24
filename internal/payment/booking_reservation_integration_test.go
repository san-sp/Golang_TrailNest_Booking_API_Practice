package payment

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBookingReservationUniqueness(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	customerID := uuid.New()
	activityID := uuid.New()
	reservationID := uuid.New()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, customerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Constraint test activity', '',
			'Test location', NOW() + INTERVAL '2 days',
			NOW() + INTERVAL '3 days', 10, 'published', 2500, 'INR'
		)
	`, activityID, customerID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO reservations (
			id, customer_id, activity_id, quantity, status, expires_at
		)
		VALUES ($1, $2, $3, 1, 'pending', NOW() + INTERVAL '15 minutes')
	`, reservationID, customerID, activityID)
	require.NoError(t, err)

	// The first booking can reference the reservation.
	_, err = tx.Exec(ctx, `
		INSERT INTO bookings (customer_id, activity_id, reservation_id, quantity)
		VALUES ($1, $2, $3, 1)
	`, customerID, activityID, reservationID)
	require.NoError(t, err)

	// A second booking must not reference the same reservation.
	_, err = tx.Exec(ctx, `SAVEPOINT before_duplicate_booking`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
    INSERT INTO bookings (customer_id, activity_id, reservation_id, quantity)
    VALUES ($1, $2, $3, 1)
`, customerID, activityID, reservationID)
	require.Error(t, err)

	// Recover the transaction after the expected uniqueness violation.
	_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT before_duplicate_booking`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `RELEASE SAVEPOINT before_duplicate_booking`)
	require.NoError(t, err)

	// Direct bookings with NULL reservation_id remain allowed.
	_, err = tx.Exec(ctx, `
		INSERT INTO bookings (customer_id, activity_id, reservation_id, quantity)
		VALUES ($1, $2, NULL, 1)
	`, customerID, activityID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO bookings (customer_id, activity_id, reservation_id, quantity)
		VALUES ($1, $2, NULL, 1)
	`, customerID, activityID)
	require.NoError(t, err)

	require.NoError(t, tx.Rollback(ctx))
}
