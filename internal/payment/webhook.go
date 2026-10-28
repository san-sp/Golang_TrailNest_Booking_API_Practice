package payment

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ProcessSuccessfulWebhook processes an already-verified provider event.
// Call this only after validating the provider's webhook signature and
// confirming the event represents a successful payment.
func (r *Repository) ProcessSuccessfulWebhook(
	ctx context.Context,
	provider string,
	eventID string,
	eventType string,
	providerPaymentID string,
) error {
	if provider == "" || eventID == "" || providerPaymentID == "" {
		return errors.New("invalid webhook event")
	}

	switch eventType {
	case "payment.captured", "payment.succeeded", "payment_link.paid":
		// Supported successful-payment event types.
	default:
		return errors.New("unsupported successful payment event type")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin webhook transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Locate the payment without locking it yet. The provider payment ID
	// must already have been associated with a payment in our database.
	var paymentID uuid.UUID
	var reservationID uuid.UUID
	var customerID uuid.UUID
	var amountMinor int64
	var currency string

	err = tx.QueryRow(ctx, `
        SELECT id, reservation_id, customer_id, amount_minor, currency
        FROM payments
        WHERE provider = $1 AND provider_payment_id = $2
`, provider, providerPaymentID).Scan(
		&paymentID,
		&reservationID,
		&customerID,
		&amountMinor,
		&currency,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPaymentNotFound
	}
	if err != nil {
		return fmt.Errorf("find provider payment: %w", err)
	}

	var activityID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT activity_id
		FROM reservations
		WHERE id = $1 AND customer_id = $2
	`, reservationID, customerID).Scan(&activityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReservationNotFound
	}
	if err != nil {
		return fmt.Errorf("find reservation activity: %w", err)
	}

	// Keep the lock order consistent with checkout:
	// activity -> reservation -> payment.
	var lockedActivityID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, activityID).Scan(&lockedActivityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReservationNotFound
	}
	if err != nil {
		return fmt.Errorf("lock activity: %w", err)
	}

	var reservationStatus string
	var quantity int
	var active bool

	err = tx.QueryRow(ctx, `
		SELECT status, quantity, expires_at > NOW()
		FROM reservations
		WHERE id = $1 AND customer_id = $2
		FOR UPDATE
	`, reservationID, customerID).Scan(
		&reservationStatus,
		&quantity,
		&active,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReservationNotFound
	}
	if err != nil {
		return fmt.Errorf("lock reservation: %w", err)
	}

	var paymentStatus string
	var lockedProvider string
	var lockedProviderPaymentID string

	err = tx.QueryRow(ctx, `
		SELECT status, provider, COALESCE(provider_payment_id, '')
		FROM payments
		WHERE id = $1
		FOR UPDATE
	`, paymentID).Scan(
		&paymentStatus,
		&lockedProvider,
		&lockedProviderPaymentID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPaymentNotFound
	}
	if err != nil {
		return fmt.Errorf("lock payment: %w", err)
	}

	if lockedProvider != provider ||
		lockedProviderPaymentID != providerPaymentID {
		return errors.New("provider payment identity mismatch")
	}

	// Record the event inside the same transaction as confirmation.
	// A duplicate event ID cannot be processed twice.
	var webhookEventID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_webhook_events (
			provider, provider_event_id, event_type, payment_id
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_event_id) DO NOTHING
		RETURNING id
	`, provider, eventID, eventType, paymentID).Scan(&webhookEventID)

	if errors.Is(err, pgx.ErrNoRows) {
		var existingEventPaymentID uuid.UUID
		var existingEventType string
		var eventStatus string

		err = tx.QueryRow(ctx, `
			SELECT payment_id, event_type, status
			FROM payment_webhook_events
			WHERE provider = $1 AND provider_event_id = $2
		`, provider, eventID).Scan(
			&existingEventPaymentID,
			&existingEventType,
			&eventStatus,
		)
		if err != nil {
			return fmt.Errorf("read duplicate webhook event: %w", err)
		}

		if existingEventPaymentID != paymentID ||
			existingEventType != eventType {
			return errors.New("webhook event ID conflicts with an existing event")
		}

		if eventStatus == "processed" {
			return tx.Commit(ctx)
		}

		return errors.New("webhook event already exists but is not processed")
	}

	if err != nil {
		return fmt.Errorf("record webhook event: %w", err)
	}

	// A different event may report success for a payment already handled.
	// Do not create a second booking.
	if paymentStatus == "succeeded" {
		_, err = tx.Exec(ctx, `
			UPDATE payment_webhook_events
			SET status = 'processed', processed_at = NOW()
			WHERE id = $1
		`, webhookEventID)
		if err != nil {
			return fmt.Errorf("mark duplicate success processed: %w", err)
		}
		return tx.Commit(ctx)
	}

	if paymentStatus != "pending" {
		return fmt.Errorf("payment cannot be confirmed from status %q", paymentStatus)
	}

	if quantity < 1 {
		return ErrInvalidQuantity
	}

	// The provider has confirmed that money was captured.
	// If fulfillment is impossible, record that fact rather than
	// rolling back the webhook forever.
	needsRefund := reservationStatus != "pending" || !active

	if !needsRefund {
		var capacity int
		var confirmedQuantity int
		var otherReservedQuantity int

		err = tx.QueryRow(ctx, `
			SELECT capacity
			FROM activities
			WHERE id = $1
		`, activityID).Scan(&capacity)
		if err != nil {
			return fmt.Errorf("load activity capacity: %w", err)
		}

		err = tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(quantity), 0)
			FROM bookings
			WHERE activity_id = $1 AND status = 'confirmed'
		`, activityID).Scan(&confirmedQuantity)
		if err != nil {
			return fmt.Errorf("count confirmed bookings: %w", err)
		}

		err = tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(quantity), 0)
			FROM reservations
			WHERE activity_id = $1
			  AND id <> $2
			  AND status = 'pending'
			  AND expires_at > NOW()
		`, activityID, reservationID).Scan(&otherReservedQuantity)
		if err != nil {
			return fmt.Errorf("count active reservations: %w", err)
		}

		if confirmedQuantity+otherReservedQuantity+quantity > capacity {
			needsRefund = true
		}
	}

	if needsRefund {
		_, err = tx.Exec(ctx, `
                UPDATE payments
                SET status = 'succeeded',
                    fulfillment_status = 'refund_required',
                    updated_at = NOW()
                WHERE id = $1
        `, paymentID)
		if err != nil {
			return fmt.Errorf("record payment requiring refund: %w", err)
		}

		refundIdempotencyKey := "trailnest-refund-" + paymentID.String()

		tag, err := tx.Exec(ctx, `
    INSERT INTO payment_refunds (
        payment_id,
        provider,
        provider_payment_id,
        idempotency_key,
        amount_minor,
        currency,
        status
    )
    VALUES ($1, $2, $3, $4, $5, $6, 'pending')
    ON CONFLICT (payment_id) DO NOTHING
`,
			paymentID,
			provider,
			providerPaymentID,
			refundIdempotencyKey,
			amountMinor,
			currency,
		)
		if err != nil {
			return fmt.Errorf("create refund job: %w", err)
		}

		if tag.RowsAffected() == 0 {
			var existingProvider string
			var existingProviderPaymentID string
			var existingIdempotencyKey string
			var existingAmount int64
			var existingCurrency string

			err = tx.QueryRow(ctx, `
        SELECT
            provider,
            provider_payment_id,
            idempotency_key,
            amount_minor,
            currency
        FROM payment_refunds
        WHERE payment_id = $1
        FOR UPDATE
    `, paymentID).Scan(
				&existingProvider,
				&existingProviderPaymentID,
				&existingIdempotencyKey,
				&existingAmount,
				&existingCurrency,
			)
			if err != nil {
				return fmt.Errorf("verify existing refund job: %w", err)
			}

			if existingProvider != provider ||
				existingProviderPaymentID != providerPaymentID ||
				existingIdempotencyKey != refundIdempotencyKey ||
				existingAmount != amountMinor ||
				existingCurrency != currency {
				return fmt.Errorf(
					"existing refund job does not match payment %s",
					paymentID,
				)
			}
		}

		_, err = tx.Exec(ctx, `
			UPDATE payment_webhook_events
			SET status = 'processed', processed_at = NOW()
			WHERE id = $1
		`, webhookEventID)
		if err != nil {
			return fmt.Errorf("mark webhook processed: %w", err)
		}

		// Commit the provider's successful payment even though we
		// couldn't fulfill the reservation. A separate refund workflow
		// must perform and verify the actual refund.
		return tx.Commit(ctx)
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO bookings (
                customer_id,
                activity_id,
                reservation_id,
                quantity,
                status
        )
        VALUES ($1, $2, $3, $4, 'confirmed')
`, customerID, activityID, reservationID, quantity)
	if err != nil {
		return fmt.Errorf("create confirmed booking: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE reservations
		SET status = 'confirmed', updated_at = NOW()
		WHERE id = $1 AND status = 'pending'
		  AND expires_at > NOW()
	`, reservationID)
	if err != nil {
		return fmt.Errorf("confirm reservation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("reservation changed before confirmation")
	}

	tag, err = tx.Exec(ctx, `
		UPDATE payments
		SET status = 'succeeded',
		    fulfillment_status = 'fulfilled',
		    updated_at = NOW()
		WHERE id = $1 AND status = 'pending'
	`, paymentID)
	if err != nil {
		return fmt.Errorf("mark payment succeeded: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("payment changed before confirmation")
	}

	_, err = tx.Exec(ctx, `
		UPDATE payment_webhook_events
		SET status = 'processed', processed_at = NOW()
		WHERE id = $1
	`, webhookEventID)
	if err != nil {
		return fmt.Errorf("mark webhook processed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit successful payment: %w", err)
	}

	return nil
}
