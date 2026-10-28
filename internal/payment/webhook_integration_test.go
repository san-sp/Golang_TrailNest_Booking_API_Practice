package payment

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"trailnest/internal/reservation"

	"github.com/gin-gonic/gin"
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
	var savedReservationID uuid.UUID

	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM bookings
        WHERE customer_id = $1 AND activity_id = $2
`, customerID, activityID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Equal(t, 1, bookingCount)

	err = pool.QueryRow(ctx, `
        SELECT reservation_id
        FROM bookings
        WHERE customer_id = $1 AND activity_id = $2
`, customerID, activityID).Scan(&savedReservationID)
	require.NoError(t, err)
	require.Equal(t, reservationID, savedReservationID)

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

func TestSuccessfulWebhookRejectsUnsupportedEventType(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	repo := NewRepository(pool)

	eventID := "event_" + uuid.NewString()

	err := repo.ProcessSuccessfulWebhook(
		ctx,
		"mock",
		eventID,
		"payment.failed",
		f.providerPaymentID,
	)
	require.ErrorContains(t, err, "unsupported successful payment event type")

	var paymentStatus, reservationStatus string

	err = pool.QueryRow(ctx, `
        SELECT p.status, r.status
        FROM payments p
        JOIN reservations r ON r.id = p.reservation_id
        WHERE p.id = $1
    `, f.paymentID).Scan(&paymentStatus, &reservationStatus)

	require.NoError(t, err)
	require.Equal(t, "pending", paymentStatus)
	require.Equal(t, "pending", reservationStatus)

	var bookingCount, eventCount int

	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM bookings
        WHERE reservation_id = $1
    `, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Zero(t, bookingCount)

	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM payment_webhook_events
        WHERE provider = $1 AND provider_event_id = $2
    `, "mock", eventID).Scan(&eventCount)
	require.NoError(t, err)
	require.Zero(t, eventCount)
}

type webhookFixture struct {
	customerID        uuid.UUID
	activityID        uuid.UUID
	reservationID     uuid.UUID
	paymentID         uuid.UUID
	providerPaymentID string
	idempotencyKey    string
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
		idempotencyKey:    uuid.NewString(),
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
		f.providerPaymentID, f.idempotencyKey)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return f
}

func createHostedPaymentFixture(
	t *testing.T,
	pool *pgxpool.Pool,
) (webhookFixture, string) {
	t.Helper()

	f := createWebhookFixture(t, pool)
	ctx := context.Background()
	linkID := "plink_" + uuid.NewString()

	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = NULL,
		    provider_payment_link_id = $2,
		    provider_payment_link_url = $3
		WHERE id = $1
	`, f.paymentID, linkID, "https://rzp.io/i/testlink")

	require.NoError(t, err)

	return f, linkID
}

func createHostedPaymentWithoutLinkFixture(
	t *testing.T,
	pool *pgxpool.Pool,
) webhookFixture {
	t.Helper()

	f := createWebhookFixture(t, pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
			provider_payment_id = NULL,
			provider_order_id = NULL,
			provider_payment_link_id = NULL,
			provider_payment_link_url = NULL
		WHERE id = $1
	`, f.paymentID)
	require.NoError(t, err)

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

func TestConcurrentPaymentConfirmationsRespectCapacity(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	// Create the first customer, activity, reservation, and payment.
	first := createWebhookFixture(t, pool)

	// Create a second customer with a separate reservation and payment
	// for the same activity.
	second := webhookFixture{
		customerID:        uuid.New(),
		activityID:        first.activityID,
		reservationID:     uuid.New(),
		paymentID:         uuid.New(),
		providerPaymentID: "test_" + uuid.NewString(),
		idempotencyKey:    uuid.NewString(),
	}

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, second.customerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO reservations (
			id, customer_id, activity_id, quantity, status, expires_at
		)
		VALUES ($1, $2, $3, 1, 'pending', NOW() + INTERVAL '15 minutes')
	`, second.reservationID, second.customerID, second.activityID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO payments (
			id, reservation_id, customer_id, amount_minor,
			currency, status, provider, provider_payment_id, idempotency_key
		)
		VALUES ($1, $2, $3, 2500, 'INR', 'pending', 'mock', $4, $5)
	`, second.paymentID, second.reservationID, second.customerID,
		second.providerPaymentID, second.idempotencyKey)
	require.NoError(t, err)

	// Both reservations exist, but only one guest can be accommodated.
	_, err = tx.Exec(ctx, `
		UPDATE reservations SET quantity = 1 WHERE id = $1
	`, first.reservationID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		UPDATE payments SET amount_minor = 2500 WHERE id = $1
	`, first.paymentID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		UPDATE activities SET capacity = 2 WHERE id = $1
	`, first.activityID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))

	repo := NewRepository(pool)
	start := make(chan struct{})
	errs := make(chan error, 2)

	for _, f := range []webhookFixture{first, second} {
		f := f
		go func() {
			<-start
			errs <- repo.ProcessSuccessfulWebhook(
				ctx,
				"mock",
				"event_"+uuid.NewString(),
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
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE activity_id = $1 AND status = 'confirmed'
	`, first.activityID).Scan(&bookingCount)
	require.NoError(t, err)

	var fulfilledCount, refundRequiredCount int
	err = pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE fulfillment_status = 'fulfilled'),
			COUNT(*) FILTER (WHERE fulfillment_status = 'refund_required')
		FROM payments
		WHERE id = ANY($1)
	`, []uuid.UUID{first.paymentID, second.paymentID}).Scan(
		&fulfilledCount,
		&refundRequiredCount,
	)
	require.Equal(t, 2, bookingCount)
	require.Equal(t, 2, fulfilledCount)
	require.Equal(t, 0, refundRequiredCount)
}

func TestConcurrentDuplicateFailedPaymentWebhook(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	repo := NewRepository(pool)

	orderID := "order_" + uuid.NewString()
	providerPaymentID := "pay_failed_" + uuid.NewString()
	eventID := "event_failed_" + uuid.NewString()

	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2
		WHERE id = $1
	`, f.paymentID, orderID)
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			<-start
			errs <- repo.ProcessFailedPaymentAttemptWebhook(
				ctx,
				"razorpay",
				eventID,
				"payment.failed",
				f.paymentID,
				orderID,
				providerPaymentID,
				5000,
				"INR",
			)
		}()
	}

	close(start)

	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}

	var eventCount, attemptCount, bookingCount int
	var paymentStatus string

	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_webhook_events
		WHERE provider = 'razorpay'
		  AND provider_event_id = $1
	`, eventID).Scan(&eventCount)
	require.NoError(t, err)
	require.Equal(t, 1, eventCount)

	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_attempts
		WHERE provider = 'razorpay'
		  AND provider_payment_id = $1
	`, providerPaymentID).Scan(&attemptCount)
	require.NoError(t, err)
	require.Equal(t, 1, attemptCount)

	err = pool.QueryRow(ctx, `
		SELECT status FROM payments WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus)
	require.NoError(t, err)
	require.Equal(t, "pending", paymentStatus)

	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM bookings WHERE reservation_id = $1
	`, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Zero(t, bookingCount)
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

func TestAssociateProviderPaymentRejectsConflictingProviderPaymentID(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	repo := NewRepository(pool)

	orderID := "order_" + uuid.NewString()
	firstProviderPaymentID := "pay_" + uuid.NewString()
	secondProviderPaymentID := "pay_" + uuid.NewString()

	_, err := pool.Exec(ctx, `
		UPDATE payments
		SET provider = 'razorpay',
		    provider_payment_id = NULL,
		    provider_order_id = $2
		WHERE id = $1
	`, f.paymentID, orderID)
	require.NoError(t, err)

	// Associate the first provider payment ID.
	err = repo.AssociateProviderPayment(
		ctx,
		f.paymentID,
		orderID,
		firstProviderPaymentID,
		5000,
		"INR",
	)
	require.NoError(t, err)

	// A different provider payment ID must not replace the original.
	err = repo.AssociateProviderPayment(
		ctx,
		f.paymentID,
		orderID,
		secondProviderPaymentID,
		5000,
		"INR",
	)
	require.ErrorIs(t, err, ErrIdempotencyConflict)

	// Verify that the original association remains unchanged.
	var storedProviderPaymentID string
	err = pool.QueryRow(ctx, `
		SELECT provider_payment_id
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&storedProviderPaymentID)
	require.NoError(t, err)
	require.Equal(t, firstProviderPaymentID, storedProviderPaymentID)
}

func TestRecordPaymentLinkAttempt(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	f, linkID := createHostedPaymentFixture(t, pool)
	repo := NewRepository(pool)

	err := repo.RecordPaymentLinkAttempt(
		ctx,
		f.paymentID,
		linkID,
		f.providerPaymentID,
		5000,
		"INR",
		"captured",
	)
	require.NoError(t, err)

	// Repeating the same attempt must be harmless.
	err = repo.RecordPaymentLinkAttempt(
		ctx,
		f.paymentID,
		linkID,
		f.providerPaymentID,
		5000,
		"INR",
		"captured",
	)
	require.NoError(t, err)

	var count int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_attempts
		WHERE payment_id = $1
		  AND provider_payment_id = $2
	`, f.paymentID, f.providerPaymentID).Scan(&count)

	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestRecordPaymentLinkAttemptRejectsMismatchedLink(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f, linkID := createHostedPaymentFixture(t, pool)
	repo := NewRepository(pool)

	err := repo.RecordPaymentLinkAttempt(
		ctx,
		f.paymentID,
		"plink_"+uuid.NewString(),
		f.providerPaymentID,
		5000,
		"INR",
		"captured",
	)

	require.Error(t, err)

	// The invalid attempt must not be inserted.
	var count int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_attempts
		WHERE payment_id = $1
	`, f.paymentID).Scan(&count)

	require.NoError(t, err)
	require.Zero(t, count)

	// The legitimate link remains saved.
	var savedLinkID string
	err = pool.QueryRow(ctx, `
		SELECT provider_payment_link_id
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&savedLinkID)

	require.NoError(t, err)
	require.Equal(t, linkID, savedLinkID)
}

func installBookingInsertFailure(
	t *testing.T,
	pool *pgxpool.Pool,
	reservationID uuid.UUID,
) {
	t.Helper()

	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION public.test_fail_booking_insert()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.reservation_id = '`+reservationID.String()+`'::uuid THEN
				RAISE EXCEPTION 'intentional booking insert failure for integration test';
			END IF;

			RETURN NEW;
		END;
		$$;

		CREATE TRIGGER test_fail_booking_insert_trigger
		BEFORE INSERT ON public.bookings
		FOR EACH ROW
		EXECUTE FUNCTION public.test_fail_booking_insert();
	`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, cleanupErr := pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS test_fail_booking_insert_trigger
				ON public.bookings;
			DROP FUNCTION IF EXISTS public.test_fail_booking_insert();
		`)
		require.NoError(t, cleanupErr)
	})
}

func TestPaymentLinkPaidWebhookCreatesOneBooking(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f, linkID := createHostedPaymentFixture(t, pool)
	repo := NewRepository(pool)

	const secret = "test_webhook_secret"
	providerPaymentID := "pay_" + uuid.NewString()
	eventID := "event_" + uuid.NewString()

	// Mock the two Razorpay API lookups made by the webhook handler.
	providerServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Path {
			case "/v1/payment_links/" + linkID:
				_, _ = fmt.Fprintf(w, `{
					"id": %q,
					"reference_id": %q,
					"amount": 5000,
					"amount_paid": 5000,
					"currency": "INR",
					"status": "paid"
				}`, linkID, f.paymentID.String())

			case "/v1/payments/" + providerPaymentID:
				_, _ = fmt.Fprintf(w, `{
					"id": %q,
					"payment_link_id": %q,
					"amount": 5000,
					"currency": "INR",
					"status": "captured"
				}`, providerPaymentID, linkID)

			default:
				http.NotFound(w, r)
			}
		},
	))
	defer providerServer.Close()

	razorpay := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       providerServer.Client(),
		apiBaseURL: providerServer.URL,
	}

	handler := NewWebhookHandler(repo, razorpay, secret)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/webhooks/razorpay", handler.Handle)

	body := []byte(fmt.Sprintf(`{
		"event": "payment_link.paid",
		"payload": {
			"payment_link": {
				"entity": {
					"id": %q,
					"reference_id": %q,
					"amount": 5000,
					"currency": "INR",
					"status": "paid"
				}
			},
			"payment": {
				"entity": {
					"id": %q,
					"payment_link_id": %q,
					"amount": 5000,
					"currency": "INR",
					"status": "captured"
				}
			}
		}
	}`, linkID, f.paymentID.String(), providerPaymentID, linkID))

	sendWebhook := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPost,
			"/webhooks/razorpay",
			bytes.NewReader(body),
		)
		req.Header.Set("X-Razorpay-Signature", testHMAC(string(body), secret))
		req.Header.Set("X-Razorpay-Event-Id", eventID)

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	firstResponse := sendWebhook()
	require.Equal(t, http.StatusOK, firstResponse.Code, firstResponse.Body.String())

	// Deliver the exact same event again.
	secondResponse := sendWebhook()
	require.Equal(t, http.StatusOK, secondResponse.Code, secondResponse.Body.String())

	// A different event ID can report the same captured payment.
	// It must not create another booking.
	req := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/razorpay",
		bytes.NewReader(body),
	)
	req.Header.Set("X-Razorpay-Signature", testHMAC(string(body), secret))
	req.Header.Set("X-Razorpay-Event-Id", "event_"+uuid.NewString())

	thirdResponse := httptest.NewRecorder()
	router.ServeHTTP(thirdResponse, req)

	require.Equal(t, http.StatusOK, thirdResponse.Code, thirdResponse.Body.String())
	var bookingCount int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE customer_id = $1 AND activity_id = $2
	`, f.customerID, f.activityID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Equal(t, 1, bookingCount)

	var savedReservationID uuid.UUID

	err = pool.QueryRow(ctx, `
        SELECT reservation_id
        FROM bookings
        WHERE customer_id = $1 AND activity_id = $2
`, f.customerID, f.activityID).Scan(&savedReservationID)

	require.NoError(t, err)
	require.Equal(t, f.reservationID, savedReservationID)

	var status, fulfillmentStatus string
	err = pool.QueryRow(ctx, `
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&status, &fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status)
	require.Equal(t, "fulfilled", fulfillmentStatus)
}

func TestPaymentLinkPaidWebhookRetriesAfterFulfillmentFailure(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f, linkID := createHostedPaymentFixture(t, pool)
	repo := NewRepository(pool)

	const secret = "test_webhook_secret"
	providerPaymentID := "pay_" + uuid.NewString()
	eventID := "event_" + uuid.NewString()

	providerServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Path {
			case "/v1/payment_links/" + linkID:
				_, _ = fmt.Fprintf(w, `{
					"id": %q,
					"reference_id": %q,
					"amount": 5000,
					"amount_paid": 5000,
					"currency": "INR",
					"status": "paid"
				}`, linkID, f.paymentID.String())

			case "/v1/payments/" + providerPaymentID:
				_, _ = fmt.Fprintf(w, `{
					"id": %q,
					"payment_link_id": %q,
					"amount": 5000,
					"currency": "INR",
					"status": "captured"
				}`, providerPaymentID, linkID)

			default:
				http.NotFound(w, r)
			}
		},
	))
	defer providerServer.Close()

	razorpay := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       providerServer.Client(),
		apiBaseURL: providerServer.URL,
	}

	handler := NewWebhookHandler(repo, razorpay, secret)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/webhooks/razorpay", handler.Handle)

	body := []byte(fmt.Sprintf(`{
		"event": "payment_link.paid",
		"payload": {
			"payment_link": {
				"entity": {
					"id": %q,
					"reference_id": %q,
					"amount": 5000,
					"currency": "INR",
					"status": "paid"
				}
			},
			"payment": {
				"entity": {
					"id": %q,
					"payment_link_id": %q,
					"amount": 5000,
					"currency": "INR",
					"status": "captured"
				}
			}
		}
	}`, linkID, f.paymentID.String(), providerPaymentID, linkID))

	sendWebhook := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPost,
			"/webhooks/razorpay",
			bytes.NewReader(body),
		)
		req.Header.Set("X-Razorpay-Signature", testHMAC(string(body), secret))
		req.Header.Set("X-Razorpay-Event-Id", eventID)

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	// Force booking creation to fail inside the fulfillment transaction.
	installBookingInsertFailure(t, pool, f.reservationID)

	firstResponse := sendWebhook()
	require.Equal(
		t,
		http.StatusInternalServerError,
		firstResponse.Code,
		firstResponse.Body.String(),
	)

	// The earlier payment-attempt and provider-payment association
	// transactions should have persisted.
	var attemptCount int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_attempts
		WHERE payment_id = $1
		  AND provider_payment_id = $2
		  AND status = 'captured'
	`, f.paymentID, providerPaymentID).Scan(&attemptCount)
	require.NoError(t, err)
	require.Equal(t, 1, attemptCount)

	var savedProviderPaymentID string
	err = pool.QueryRow(ctx, `
		SELECT provider_payment_id
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&savedProviderPaymentID)
	require.NoError(t, err)
	require.Equal(t, providerPaymentID, savedProviderPaymentID)

	// The failed fulfillment transaction must leave no partial booking,
	// reservation confirmation, payment success, or webhook event.
	assertRetryableState := func() {
		t.Helper()

		var paymentStatus, fulfillmentStatus, reservationStatus string
		err := pool.QueryRow(ctx, `
			SELECT p.status, p.fulfillment_status, r.status
			FROM payments p
			JOIN reservations r ON r.id = p.reservation_id
			WHERE p.id = $1
		`, f.paymentID).Scan(
			&paymentStatus,
			&fulfillmentStatus,
			&reservationStatus,
		)
		require.NoError(t, err)
		require.Equal(t, "pending", paymentStatus)
		require.NotEqual(t, "fulfilled", fulfillmentStatus)
		require.Equal(t, "pending", reservationStatus)

		var bookingCount int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM bookings
			WHERE reservation_id = $1
		`, f.reservationID).Scan(&bookingCount)
		require.NoError(t, err)
		require.Zero(t, bookingCount)

		var webhookEventCount int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM payment_webhook_events
			WHERE provider = 'razorpay'
			  AND provider_event_id = $1
		`, eventID).Scan(&webhookEventCount)
		require.NoError(t, err)
		require.Zero(t, webhookEventCount)
	}

	assertRetryableState()

	// Remove the injected failure so the same webhook can succeed.
	_, err = pool.Exec(ctx, `
		DROP TRIGGER IF EXISTS test_fail_booking_insert_trigger
			ON public.bookings;
		DROP FUNCTION IF EXISTS public.test_fail_booking_insert();
	`)
	require.NoError(t, err)

	secondResponse := sendWebhook()
	require.Equal(
		t,
		http.StatusOK,
		secondResponse.Code,
		secondResponse.Body.String(),
	)

	var paymentStatus, fulfillmentStatus, reservationStatus string
	err = pool.QueryRow(ctx, `
		SELECT p.status, p.fulfillment_status, r.status
		FROM payments p
		JOIN reservations r ON r.id = p.reservation_id
		WHERE p.id = $1
	`, f.paymentID).Scan(
		&paymentStatus,
		&fulfillmentStatus,
		&reservationStatus,
	)
	require.NoError(t, err)
	require.Equal(t, "succeeded", paymentStatus)
	require.Equal(t, "fulfilled", fulfillmentStatus)
	require.Equal(t, "confirmed", reservationStatus)

	var bookingCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE reservation_id = $1
	`, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Equal(t, 1, bookingCount)

	var processedEventCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_webhook_events
		WHERE provider = 'razorpay'
		  AND provider_event_id = $1
		  AND status = 'processed'
	`, eventID).Scan(&processedEventCount)
	require.NoError(t, err)
	require.Equal(t, 1, processedEventCount)
}

func TestConcurrentWebhookAndReservationExpiryStayConsistent(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	f := createWebhookFixture(t, pool)

	// Make the reservation overdue so the expiry worker and webhook
	// compete for the same activity and reservation.
	_, err := pool.Exec(ctx, `
		UPDATE reservations
		SET expires_at = NOW() - INTERVAL '100 years'
		WHERE id = $1
	`, f.reservationID)
	require.NoError(t, err)

	paymentRepo := NewRepository(pool)
	reservationRepo := reservation.NewRepository(pool)

	start := make(chan struct{})
	errs := make(chan error, 2)

	go func() {
		<-start
		errs <- paymentRepo.ProcessSuccessfulWebhook(
			ctx,
			"mock",
			"event_"+uuid.NewString(),
			"payment.succeeded",
			f.providerPaymentID,
		)
	}()

	go func() {
		<-start
		_, err := reservationRepo.ExpireOverdue(ctx, 10)
		errs <- err
	}()
	close(start)

	require.NoError(t, <-errs)
	require.NoError(t, <-errs)

	var reservationStatus, paymentStatus, fulfillmentStatus string

	err = pool.QueryRow(ctx, `
		SELECT status
		FROM reservations
		WHERE id = $1
	`, f.reservationID).Scan(&reservationStatus)
	require.NoError(t, err)
	require.Equal(t, "expired", reservationStatus)

	err = pool.QueryRow(ctx, `
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus, &fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "succeeded", paymentStatus)
	require.Equal(t, "refund_required", fulfillmentStatus)

	var bookingCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bookings
		WHERE reservation_id = $1
		  AND status = 'confirmed'
	`, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Zero(t, bookingCount)
}

func TestCreatePaymentRejectsHostedPaymentLink(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f, _ := createHostedPaymentFixture(t, pool)

	service := NewCheckoutService(pool, &RazorpayClient{
		keyID:     "test_key",
		keySecret: "test_secret",
	})

	_, err := service.CreatePayment(
		ctx,
		f.customerID,
		f.reservationID,
		f.idempotencyKey,
	)

	require.Error(t, err)
	require.ErrorContains(
		t,
		err,
		"use hosted checkout",
	)
}

func TestCreateHostedPaymentRecoversExistingLink(t *testing.T) {
	pool := openTestPool(t)
	f := createHostedPaymentWithoutLinkFixture(t, pool)
	linkID := "plink_" + uuid.NewString()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/v1/payment_links" {
			t.Errorf("path = %q, want /v1/payment_links", r.URL.Path)
		}
		if got := r.URL.Query().Get("reference_id"); got != f.paymentID.String() {
			t.Errorf("reference_id = %q, want %q", got, f.paymentID.String())
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
    "payment_links": [{
        "id": %q,
        "short_url": "https://rzp.io/i/recovered",
        "reference_id": %q,
        "amount": 5000,
        "amount_paid": 0,
        "currency": "INR",
        "status": "created"
}]
		}`, linkID, f.paymentID.String())
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}
	service := NewCheckoutService(pool, client)

	result, err := service.CreateHostedPayment(
		context.Background(),
		f.customerID,
		f.reservationID,
		f.idempotencyKey,
	)
	require.NoError(t, err)
	require.Equal(t, linkID, result.PaymentLinkID)
	require.Equal(t, "https://rzp.io/i/recovered", result.PaymentLinkURL)

	var savedID, savedURL string
	err = pool.QueryRow(context.Background(), `
		SELECT provider_payment_link_id, provider_payment_link_url
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&savedID, &savedURL)
	require.NoError(t, err)
	require.Equal(t, result.PaymentLinkID, savedID)
	require.Equal(t, result.PaymentLinkURL, savedURL)
}

func TestCreateHostedPaymentRecoversAfterCreateResponseLost(t *testing.T) {
	pool := openTestPool(t)
	f := createHostedPaymentWithoutLinkFixture(t, pool)

	linkID := "plink_" + uuid.NewString()
	linkURL := "https://rzp.io/i/recovered-after-timeout"

	var mu sync.Mutex
	var storedLink *RazorpayPaymentLink
	createRequests := 0

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == "/v1/payment_links":

			if got := r.URL.Query().Get("reference_id"); got != f.paymentID.String() {
				t.Errorf("reference_id = %q, want %q", got, f.paymentID.String())
			}

			mu.Lock()
			link := storedLink
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")

			if link == nil {
				_, _ = w.Write([]byte(`{"payment_links":[]}`))
				return
			}

			_, _ = fmt.Fprintf(w, `{
				"payment_links": [{
					"id": %q,
					"short_url": %q,
					"reference_id": %q,
					"amount": 5000,
					"amount_paid": 0,
					"currency": "INR",
					"status": "created"
				}]
			}`,
				link.ID,
				link.ShortURL,
				link.ReferenceID,
			)

		case r.Method == http.MethodPost &&
			r.URL.Path == "/v1/payment_links":

			mu.Lock()
			createRequests++

			storedLink = &RazorpayPaymentLink{
				ID:          linkID,
				ShortURL:    linkURL,
				ReferenceID: f.paymentID.String(),
				Amount:      5000,
				Currency:    "INR",
				Status:      "created",
			}
			mu.Unlock()

			// Simulate successful creation followed by a lost response.
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("HTTP server does not support connection hijacking")
				http.Error(w, "response lost", http.StatusInternalServerError)
				return
			}

			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack connection: %v", err)
				return
			}
			_ = conn.Close()

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}
	service := NewCheckoutService(pool, client)
	ctx := context.Background()

	// First attempt: the provider creates the link, but the response is lost.
	_, err := service.CreateHostedPayment(
		ctx,
		f.customerID,
		f.reservationID,
		f.idempotencyKey,
	)
	require.Error(t, err)

	mu.Lock()
	gotCreateRequests := createRequests
	mu.Unlock()
	require.Equal(t, 1, gotCreateRequests)

	// Retry: the lookup finds the link created by the first attempt.
	result, err := service.CreateHostedPayment(
		ctx,
		f.customerID,
		f.reservationID,
		f.idempotencyKey,
	)
	require.NoError(t, err)
	require.Equal(t, linkID, result.PaymentLinkID)
	require.Equal(t, linkURL, result.PaymentLinkURL)

	// The retry must recover the existing link, not create another.
	mu.Lock()
	gotCreateRequestsAfterRetry := createRequests
	mu.Unlock()
	require.Equal(t, 1, gotCreateRequestsAfterRetry)

	var savedID, savedURL string
	err = pool.QueryRow(ctx, `
		SELECT provider_payment_link_id, provider_payment_link_url
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&savedID, &savedURL)

	require.NoError(t, err)
	require.Equal(t, linkID, savedID)
	require.Equal(t, linkURL, savedURL)
}

func TestConcurrentHostedPaymentCreatesOneLink(t *testing.T) {
	pool := openTestPool(t)
	f := createHostedPaymentWithoutLinkFixture(t, pool)

	linkID := "plink_" + uuid.NewString()
	var mu sync.Mutex
	createRequests := 0

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == "/v1/payment_links":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"payment_links":[]}`)

		case r.Method == http.MethodPost &&
			r.URL.Path == "/v1/payment_links":
			mu.Lock()
			createRequests++
			mu.Unlock()

			// Keep the first provider request open briefly so the
			// competing checkout has a chance to contend for the lock.
			time.Sleep(150 * time.Millisecond)

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"id": %q,
				"short_url": "https://rzp.io/i/concurrent",
				"reference_id": %q,
				"amount": 5000,
				"amount_paid": 0,
				"currency": "INR",
				"status": "created"
			}`, linkID, f.paymentID.String())

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}
	service := NewCheckoutService(pool, client)

	start := make(chan struct{})
	type outcome struct {
		result HostedPaymentResult
		err    error
	}
	results := make(chan outcome, 2)

	for i := 0; i < 2; i++ {
		go func() {
			<-start
			result, err := service.CreateHostedPayment(
				context.Background(),
				f.customerID,
				f.reservationID,
				f.idempotencyKey,
			)
			results <- outcome{result: result, err: err}
		}()
	}

	close(start)

	first := <-results
	second := <-results

	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, linkID, first.result.PaymentLinkID)
	require.Equal(t, linkID, second.result.PaymentLinkID)
	require.Equal(t, "https://rzp.io/i/concurrent", first.result.PaymentLinkURL)
	require.Equal(t, first.result.PaymentLinkURL, second.result.PaymentLinkURL)

	mu.Lock()
	gotCreateRequests := createRequests
	mu.Unlock()
	require.Equal(t, 1, gotCreateRequests)
}

func TestCreateHostedPaymentStopsOnLookupError(t *testing.T) {
	pool := openTestPool(t)
	f := createHostedPaymentWithoutLinkFixture(t, pool)

	var createRequests int

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method == http.MethodPost &&
			r.URL.Path == "/v1/payment_links" {
			createRequests++
			t.Error("must not create a link when lookup fails")
		}

		if r.Method != http.MethodGet ||
			r.URL.Path != "/v1/payment_links" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("provider error"))
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}
	service := NewCheckoutService(pool, client)

	_, err := service.CreateHostedPayment(
		context.Background(),
		f.customerID,
		f.reservationID,
		f.idempotencyKey,
	)

	require.Error(t, err)
	require.Equal(t, 0, createRequests)
}

func TestCreateHostedPaymentRejectsMismatchedAmount(t *testing.T) {
	pool := openTestPool(t)
	f := createHostedPaymentWithoutLinkFixture(t, pool)

	linkID := "plink_" + uuid.NewString()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"payment_links": [{
				"id": %q,
				"short_url": "https://rzp.io/i/mismatch",
				"reference_id": %q,
				"amount": 9999,
				"amount_paid": 0,
				"currency": "INR",
				"status": "created"
			}]
		}`, linkID, f.paymentID.String())
	}))
	defer server.Close()

	client := &RazorpayClient{
		keyID:      "test_key",
		keySecret:  "test_secret",
		http:       server.Client(),
		apiBaseURL: server.URL,
	}
	service := NewCheckoutService(pool, client)

	_, err := service.CreateHostedPayment(
		context.Background(),
		f.customerID,
		f.reservationID,
		f.idempotencyKey,
	)

	require.ErrorContains(
		t,
		err,
		"does not match the local payment",
	)

	var savedLinkID string
	err = pool.QueryRow(context.Background(), `
		SELECT COALESCE(provider_payment_link_id, '')
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&savedLinkID)
	require.NoError(t, err)
	require.Empty(t, savedLinkID)
}

func TestSuccessfulPaymentWithExpiredReservationRequiresRefund(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	repo := NewRepository(pool)

	// The provider captured the payment, but the reservation expired.
	_, err := pool.Exec(ctx, `
        UPDATE reservations
        SET expires_at = NOW() - INTERVAL '1 minute'
        WHERE id = $1
    `, f.reservationID)
	require.NoError(t, err)

	eventID := "event_" + uuid.NewString()

	err = repo.ProcessSuccessfulWebhook(
		ctx,
		"mock",
		eventID,
		"payment.succeeded",
		f.providerPaymentID,
	)
	require.NoError(t, err)

	var paymentStatus, fulfillmentStatus, reservationStatus string
	err = pool.QueryRow(ctx, `
        SELECT p.status, p.fulfillment_status, r.status
        FROM payments p
        JOIN reservations r ON r.id = p.reservation_id
        WHERE p.id = $1
    `, f.paymentID).Scan(
		&paymentStatus,
		&fulfillmentStatus,
		&reservationStatus,
	)
	require.NoError(t, err)

	require.Equal(t, "succeeded", paymentStatus)
	require.Equal(t, "refund_required", fulfillmentStatus)
	require.Equal(t, "pending", reservationStatus)

	var bookingCount int
	err = pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM bookings
        WHERE reservation_id = $1
          AND status = 'confirmed'
    `, f.reservationID).Scan(&bookingCount)
	require.NoError(t, err)
	require.Zero(t, bookingCount)

	var eventStatus string
	err = pool.QueryRow(ctx, `
        SELECT status
        FROM payment_webhook_events
        WHERE provider = $1 AND provider_event_id = $2
    `, "mock", eventID).Scan(&eventStatus)
	require.NoError(t, err)
	require.Equal(t, "processed", eventStatus)

	var refundCount int
	var refundProvider string
	var refundProviderPaymentID string
	var refundIdempotencyKey string
	var refundAmount int64
	var refundCurrency string
	var refundStatus string

	err = pool.QueryRow(ctx, `
                SELECT
                        COUNT(*),
                        COALESCE(MAX(provider), ''),
                        COALESCE(MAX(provider_payment_id), ''),
                        COALESCE(MAX(idempotency_key), ''),
                        COALESCE(MAX(amount_minor), 0),
                        COALESCE(MAX(currency), ''),
                        COALESCE(MAX(status), '')
                FROM payment_refunds
                WHERE payment_id = $1
        `, f.paymentID).Scan(
		&refundCount,
		&refundProvider,
		&refundProviderPaymentID,
		&refundIdempotencyKey,
		&refundAmount,
		&refundCurrency,
		&refundStatus,
	)
	require.NoError(t, err)

	require.Equal(t, 1, refundCount)
	require.Equal(t, "mock", refundProvider)
	require.Equal(t, f.providerPaymentID, refundProviderPaymentID)
	require.Equal(t, "trailnest-refund-"+f.paymentID.String(), refundIdempotencyKey)
	require.Positive(t, refundAmount)
	require.Equal(t, "INR", refundCurrency)
	require.Equal(t, "pending", refundStatus)
}

func TestSuccessfulPaymentRejectsConflictingRefundJob(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	f := createWebhookFixture(t, pool)
	repo := NewRepository(pool)

	// Expire the reservation so the successful payment requires a refund.
	_, err := pool.Exec(ctx, `
		UPDATE reservations
		SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, f.reservationID)
	require.NoError(t, err)

	// Insert a conflicting refund job for this payment.
	_, err = pool.Exec(ctx, `
		INSERT INTO payment_refunds (
			payment_id,
			provider,
			provider_payment_id,
			idempotency_key,
			amount_minor,
			currency,
			status
		)
		VALUES ($1, 'mock', $2, $3, 1, 'INR', 'pending')
	`,
		f.paymentID,
		f.providerPaymentID,
		"conflicting-refund-"+f.paymentID.String(),
	)
	require.NoError(t, err)

	eventID := "event_" + uuid.NewString()

	err = repo.ProcessSuccessfulWebhook(
		ctx,
		"mock",
		eventID,
		"payment.succeeded",
		f.providerPaymentID,
	)
	require.ErrorContains(t, err, "existing refund job does not match payment")

	// The payment update must roll back.
	var paymentStatus, fulfillmentStatus string
	err = pool.QueryRow(ctx, `
		SELECT status, fulfillment_status
		FROM payments
		WHERE id = $1
	`, f.paymentID).Scan(&paymentStatus, &fulfillmentStatus)
	require.NoError(t, err)
	require.Equal(t, "pending", paymentStatus)
	require.Equal(t, "pending", fulfillmentStatus)

	// The pre-existing conflicting refund job must remain unchanged.
	var refundCount int
	var refundAmount int64
	var refundIdempotencyKey string

	err = pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(MAX(amount_minor), 0),
		       COALESCE(MAX(idempotency_key), '')
		FROM payment_refunds
		WHERE payment_id = $1
	`, f.paymentID).Scan(
		&refundCount,
		&refundAmount,
		&refundIdempotencyKey,
	)
	require.NoError(t, err)
	require.Equal(t, 1, refundCount)
	require.EqualValues(t, 1, refundAmount)
	require.Equal(t, "conflicting-refund-"+f.paymentID.String(), refundIdempotencyKey)

	// The failed event must not be marked processed.
	var eventCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM payment_webhook_events
		WHERE provider = $1 AND provider_event_id = $2
	`, "mock", eventID).Scan(&eventCount)
	require.NoError(t, err)
	require.Zero(t, eventCount)
}
