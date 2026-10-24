package payment

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) ProcessFailedPaymentAttemptWebhook(
	ctx context.Context,
	provider string,
	eventID string,
	eventType string,
	paymentID uuid.UUID,
	providerOrderID string,
	providerPaymentID string,
	amountMinor int64,
	currency string,
) error {
	provider = strings.TrimSpace(provider)
	eventID = strings.TrimSpace(eventID)
	eventType = strings.TrimSpace(eventType)
	providerOrderID = strings.TrimSpace(providerOrderID)
	providerPaymentID = strings.TrimSpace(providerPaymentID)
	currency = strings.ToUpper(strings.TrimSpace(currency))

	if provider == "" ||
		eventID == "" ||
		eventType != "payment.failed" ||
		providerOrderID == "" ||
		providerPaymentID == "" ||
		amountMinor <= 0 ||
		len(currency) != 3 {
		return errors.New("invalid failed payment attempt")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var savedOrderID string
	var savedProvider string
	var savedAmount int64
	var savedCurrency string

	err = tx.QueryRow(ctx, `
		SELECT provider_order_id, provider, amount_minor, currency
		FROM payments
		WHERE id = $1
		FOR UPDATE
	`, paymentID).Scan(
		&savedOrderID,
		&savedProvider,
		&savedAmount,
		&savedCurrency,
	)
	if err != nil {
		return err
	}

	if savedProvider != provider ||
		savedOrderID != providerOrderID ||
		savedAmount != amountMinor ||
		!strings.EqualFold(savedCurrency, currency) {
		return errors.New("failed payment attempt does not match checkout")
	}

	var existingEventType string
	var existingPaymentID uuid.UUID
	var existingEventStatus string

	err = tx.QueryRow(ctx, `
		SELECT event_type, payment_id, status
		FROM payment_webhook_events
		WHERE provider = $1 AND provider_event_id = $2
	`, provider, eventID).Scan(
		&existingEventType,
		&existingPaymentID,
		&existingEventStatus,
	)

	if err == nil {
		if existingEventType != eventType ||
			existingPaymentID != paymentID {
			return ErrIdempotencyConflict
		}

		if existingEventStatus == "processed" {
			return tx.Commit(ctx)
		}

		return errors.New("webhook event has not completed processing")
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO payment_webhook_events (
			provider,
			provider_event_id,
			event_type,
			payment_id,
			status
		)
		VALUES ($1, $2, $3, $4, 'processing')
	`, provider, eventID, eventType, paymentID)
	if err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO payment_attempts (
			payment_id,
			provider,
			provider_payment_id,
			amount_minor,
			currency,
			status
		)
		VALUES ($1, $2, $3, $4, $5, 'failed')
		ON CONFLICT (provider, provider_payment_id)
		DO UPDATE SET
			status = CASE
				WHEN payment_attempts.status = 'captured'
					THEN 'captured'
				ELSE 'failed'
			END,
			updated_at = NOW()
		WHERE payment_attempts.payment_id = EXCLUDED.payment_id
			AND payment_attempts.amount_minor = EXCLUDED.amount_minor
			AND payment_attempts.currency = EXCLUDED.currency
	`, paymentID, provider, providerPaymentID, amountMinor, currency)
	if err != nil {
		return err
	}

	if tag.RowsAffected() != 1 {
		return errors.New("payment attempt conflicts with an existing attempt")
	}

	tag, err = tx.Exec(ctx, `
		UPDATE payment_webhook_events
		SET status = 'processed', processed_at = NOW()
		WHERE provider = $1 AND provider_event_id = $2
	`, provider, eventID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("could not mark webhook event processed")
	}

	return tx.Commit(ctx)
}
