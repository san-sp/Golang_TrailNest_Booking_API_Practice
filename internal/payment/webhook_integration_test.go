package payment

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestSuccessfulWebhookIsIdempotent(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	eventID := "event_" + uuid.NewString()

	// Isolate this test's records using unique IDs and email.
	customerID := uuid.New()
	activityID := uuid.New()
	reservationID := uuid.New()
	paymentID := uuid.New()
	providerPaymentID := "test_" + uuid.NewString()
	email := uuid.NewString() + "@example.test"

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, customerID, email)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Integration test activity', '',
			'Test location', NOW() + INTERVAL '2 days',
			NOW() + INTERVAL '3 days', 10, 'published', 2500, 'INR'
		)
	`, activityID, customerID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO reservations (
			id, customer_id, activity_id, quantity, status, expires_at
		)
		VALUES ($1, $2, $3, 2, 'pending', NOW() + INTERVAL '15 minutes')
	`, reservationID, customerID, activityID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO payments (
			id, reservation_id, customer_id, amount_minor,
			currency, status, provider, provider_payment_id, idempotency_key
		)
		VALUES ($1, $2, $3, 5000, 'INR', 'pending', 'mock', $4, $5)
	`, paymentID, reservationID, customerID, providerPaymentID, uuid.NewString())
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))

	repo := NewRepository(pool)

	err = repo.ProcessSuccessfulWebhook(
		ctx, "mock", eventID, "payment.succeeded", providerPaymentID,
	)
	require.NoError(t, err)

	// A distinct event ID can also report the same successful payment.
	// It must not create another booking.
	err = repo.ProcessSuccessfulWebhook(
		ctx, "mock", eventID, "payment.succeeded", providerPaymentID,
	)
	require.NoError(t, err)

	var bookingCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE customer_id = $1 AND activity_id = $2
	`, customerID, activityID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Equal(t, 1, bookingCount)

	var paymentStatus, fulfillmentStatus, reservationStatus string

	err = pool.QueryRow(ctx, `
		SELECT status, fulfillment_status
		FROM payments WHERE id = $1
	`, paymentID).Scan(&paymentStatus, &fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "succeeded", paymentStatus)
	require.Equal(t, "fulfilled", fulfillmentStatus)

	err = pool.QueryRow(ctx, `
		SELECT status FROM reservations WHERE id = $1
	`, reservationID).Scan(&reservationStatus)
	require.NoError(t, err)
	require.Equal(t, "confirmed", reservationStatus)

	// Keep the imported time package useful for future expiry cases.
	_ = time.Minute
}

type webhookFixture struct {
	customerID        uuid.UUID
	activityID        uuid.UUID
	reservationID     uuid.UUID
	paymentID         uuid.UUID
	providerPaymentID string
}

func createWebhookFixture(t *testing.T, pool *pgxpool.Pool) webhookFixture {
	t.Helper()

	ctx := context.Background()
	f := webhookFixture{
		customerID:        uuid.New(),
		activityID:        uuid.New(),
		reservationID:     uuid.New(),
		paymentID:         uuid.New(),
		providerPaymentID: "test_" + uuid.NewString(),
	}

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, f.customerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Webhook test activity', '', 'Test location',
			NOW() + INTERVAL '2 days', NOW() + INTERVAL '3 days',
			10, 'published', 2500, 'INR'
		)
	`, f.activityID, f.customerID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO reservations (
			id, customer_id, activity_id, quantity, status, expires_at
		)
		VALUES ($1, $2, $3, 2, 'pending', NOW() + INTERVAL '15 minutes')
	`, f.reservationID, f.customerID, f.activityID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO payments (
			id, reservation_id, customer_id, amount_minor,
			currency, status, provider, provider_payment_id, idempotency_key
		)
		VALUES ($1, $2, $3, 5000, 'INR', 'pending', 'mock', $4, $5)
	`, f.paymentID, f.reservationID, f.customerID,
		f.providerPaymentID, uuid.NewString())
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return f
}

func TestConcurrentDuplicateWebhookCreatesOneBooking(t *testing.T) {
	pool := openTestPool(t)
	f := createWebhookFixture(t, pool)

	repo := NewRepository(pool)
	eventID := "event_" + uuid.NewString()

	start := make(chan struct{})
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			<-start
			errs <- repo.ProcessSuccessfulWebhook(
				context.Background(),
				"mock",
				eventID,
				"payment.succeeded",
				f.providerPaymentID,
			)
		}()
	}

	close(start)

	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}

	var bookingCount int
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM bookings
		WHERE customer_id = $1 AND activity_id = $2
	`, f.customerID, f.activityID).Scan(&bookingCount)

	require.NoError(t, err)
	require.Equal(t, 1, bookingCount)
}

func TestFailedPaymentAttemptCanBeRetriedSuccessfully(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	repo := NewRepository(pool)

	orderID := "order_" + uuid.NewString()
	failedPaymentID := "pay_failed_" + uuid.NewString()
	capturedPaymentID := "pay_captured_" + uuid.NewString()
	failedEventID := "event_failed_" + uuid.NewString()
	capturedEventID := "event_captured_" + uuid.NewString()

	// Configure the fixture as a Razorpay checkout with no
	// associated provider payment yet.
	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2
		WHERE id = $1
	`, f.paymentID, orderID)
	require.NoError(t, err)

	// First attempt fails. The checkout itself must remain pending.
	err = repo.ProcessFailedPaymentAttemptWebhook(
		ctx,
		"razorpay",
		failedEventID,
		"payment.failed",
		f.paymentID,
		orderID,
		failedPaymentID,
		5000,
		"INR",
	)
	require.NoError(t, err)

	var paymentStatus string
	err = pool.QueryRow(ctx, `
		SELECT status FROM payments WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus)
	require.NoError(t, err)
	require.Equal(t, "pending", paymentStatus)

	// Re-delivering the same failed event must be harmless.
	err = repo.ProcessFailedPaymentAttemptWebhook(
		ctx,
		"razorpay",
		failedEventID,
		"payment.failed",
		f.paymentID,
		orderID,
		failedPaymentID,
		5000,
		"INR",
	)
	require.NoError(t, err)

	// The second attempt succeeds. Record it, associate it, then
	// process fulfillment using the existing success transaction.
	err = repo.RecordPaymentAttempt(
		ctx,
		f.paymentID,
		"razorpay",
		orderID,
		capturedPaymentID,
		5000,
		"INR",
		"captured",
	)
	require.NoError(t, err)

	err = repo.AssociateProviderPayment(
		ctx,
		f.paymentID,
		orderID,
		capturedPaymentID,
		5000,
		"INR",
	)
	require.NoError(t, err)

	err = repo.ProcessSuccessfulWebhook(
		ctx,
		"razorpay",
		capturedEventID,
		"payment.captured",
		capturedPaymentID,
	)
	require.NoError(t, err)

	// Re-delivering the failed event must not undo success.
	err = repo.ProcessFailedPaymentAttemptWebhook(
		ctx,
		"razorpay",
		failedEventID,
		"payment.failed",
		f.paymentID,
		orderID,
		failedPaymentID,
		5000,
		"INR",
	)
	require.NoError(t, err)

	err = pool.QueryRow(ctx, `
		SELECT status FROM payments WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus)
	require.NoError(t, err)
	require.Equal(t, "succeeded", paymentStatus)

	var bookingCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE customer_id = $1 AND activity_id = $2
	`, f.customerID, f.activityID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Equal(t, 1, bookingCount)

	var attemptCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_attempts
		WHERE payment_id = $1
	`, f.paymentID).Scan(&attemptCount)
	require.NoError(t, err)
	// ----------- DEBUG------------
	rows, err := pool.Query(ctx, `
    SELECT provider_payment_id, status
    FROM payment_attempts
    WHERE payment_id = $1
    ORDER BY created_at
`, f.paymentID)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var providerPaymentID, status string
		require.NoError(t, rows.Scan(&providerPaymentID, &status))
		t.Logf("payment attempt: id=%s status=%s", providerPaymentID, status)
	}
	require.NoError(t, rows.Err())

	require.Equal(t, 2, attemptCount)
	// -----------------------------
	require.Equal(t, 2, attemptCount)
}

func TestRecordPaymentAttemptRejectsConflictingProviderPaymentID(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	first := createWebhookFixture(t, pool)
	second := createWebhookFixture(t, pool)
	repo := NewRepository(pool)

	orderID := "order_" + uuid.NewString()
	sharedProviderPaymentID := "pay_" + uuid.NewString()

	// Configure both local payments as Razorpay checkouts.
	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2
		WHERE id = $1
	`, first.paymentID, orderID)
	require.NoError(t, err)

	secondOrderID := "order_" + uuid.NewString()
	_, err = pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2
		WHERE id = $1
	`, second.paymentID, secondOrderID)
	require.NoError(t, err)

	// First checkout claims this provider payment ID.
	err = repo.RecordPaymentAttempt(
		ctx,
		first.paymentID,
		"razorpay",
		orderID,
		sharedProviderPaymentID,
		5000,
		"INR",
		"failed",
	)
	require.NoError(t, err)

	// A different checkout must not be able to claim the same ID.
	err = repo.RecordPaymentAttempt(
		ctx,
		second.paymentID,
		"razorpay",
		secondOrderID,
		sharedProviderPaymentID,
		5000,
		"INR",
		"captured",
	)
	require.Error(t, err)

	// Verify the original attempt remains associated with the first checkout.
	var storedPaymentID uuid.UUID
	var storedStatus string

	err = pool.QueryRow(ctx, `
		SELECT payment_id, status
		FROM payment_attempts
		WHERE provider = 'razorpay'
		  AND provider_payment_id = $1
	`, sharedProviderPaymentID).Scan(&storedPaymentID, &storedStatus)
	require.NoError(t, err)
	require.Equal(t, first.paymentID, storedPaymentID)
	require.Equal(t, "failed", storedStatus)
}
