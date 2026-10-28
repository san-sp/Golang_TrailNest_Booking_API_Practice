package cancellation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrActivityNotFound          = errors.New("activity not found")
	ErrInvalidActivityTransition = errors.New("invalid activity status transition")
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type Activity struct {
	ID                uuid.UUID
	OwnerID           uuid.UUID
	Title             string
	Description       string
	Location          string
	StartsAt          time.Time
	EndsAt            time.Time
	Capacity          int
	Status            string
	CreatedAt         time.Time
	PriceMinor        int64
	Currency          string
	ImageURL          *string
	RemainingCapacity int
}

func (s *Service) CancelActivity(
	ctx context.Context,
	activityID uuid.UUID,
	ownerID *uuid.UUID,
) (Activity, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Activity{}, fmt.Errorf("begin activity cancellation: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Lock the activity first. The payment webhook uses this same
	// lock order, so cancellation and payment processing serialize.
	var currentStatus string
	var currentOwnerID uuid.UUID

	err = tx.QueryRow(ctx, `
		SELECT status, owner_id
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, activityID).Scan(&currentStatus, &currentOwnerID)

	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("lock activity for cancellation: %w", err)
	}

	if ownerID != nil && currentOwnerID != *ownerID {
		return Activity{}, ErrActivityNotFound
	}

	if currentStatus != "published" && currentStatus != "draft" {
		return Activity{}, ErrInvalidActivityTransition
	}

	// Lock reservations in a stable order before locking payments.
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM reservations
		WHERE activity_id = $1
		ORDER BY id
		FOR UPDATE
	`, activityID)
	if err != nil {
		return Activity{}, fmt.Errorf("lock activity reservations: %w", err)
	}

	var reservationIDs []uuid.UUID
	for rows.Next() {
		var reservationID uuid.UUID
		if err := rows.Scan(&reservationID); err != nil {
			rows.Close()
			return Activity{}, fmt.Errorf("scan reservation ID: %w", err)
		}
		reservationIDs = append(reservationIDs, reservationID)
	}

	if err := rows.Err(); err != nil {
		rows.Close()
		return Activity{}, fmt.Errorf("iterate activity reservations: %w", err)
	}
	rows.Close()

	// Cancel pending reservations. Confirmed reservations remain as
	// historical checkout records; their bookings are cancelled below.
	_, err = tx.Exec(ctx, `
		UPDATE reservations
		SET status = 'cancelled', updated_at = NOW()
		WHERE activity_id = $1
		  AND status = 'pending'
	`, activityID)
	if err != nil {
		return Activity{}, fmt.Errorf("cancel pending reservations: %w", err)
	}

	// Cancel confirmed bookings, including direct bookings that might
	// not have a reservation_id.
	_, err = tx.Exec(ctx, `
		UPDATE bookings
		SET status = 'cancelled', updated_at = NOW()
		WHERE activity_id = $1
		  AND status = 'confirmed'
	`, activityID)
	if err != nil {
		return Activity{}, fmt.Errorf("cancel confirmed bookings: %w", err)
	}

	// Lock payments only after reservation locks. Queue refunds only
	// for successful payments that have been fulfilled.
	rows, err = tx.Query(ctx, `
		SELECT
			p.id,
			p.status,
			p.fulfillment_status,
			p.provider,
			COALESCE(p.provider_payment_id, ''),
			p.amount_minor,
			p.currency
		FROM payments p
		JOIN reservations r ON r.id = p.reservation_id
		WHERE r.activity_id = $1
		ORDER BY p.id
		FOR UPDATE OF p
	`, activityID)
	if err != nil {
		return Activity{}, fmt.Errorf("lock activity payments: %w", err)
	}

	type paymentForRefund struct {
		id                uuid.UUID
		status            string
		fulfillmentStatus string
		provider          string
		providerPaymentID string
		amountMinor       int64
		currency          string
	}

	var payments []paymentForRefund
	for rows.Next() {
		var p paymentForRefund
		if err := rows.Scan(
			&p.id,
			&p.status,
			&p.fulfillmentStatus,
			&p.provider,
			&p.providerPaymentID,
			&p.amountMinor,
			&p.currency,
		); err != nil {
			rows.Close()
			return Activity{}, fmt.Errorf("scan activity payment: %w", err)
		}
		payments = append(payments, p)
	}

	if err := rows.Err(); err != nil {
		rows.Close()
		return Activity{}, fmt.Errorf("iterate activity payments: %w", err)
	}
	rows.Close()

	for _, p := range payments {
		// Never initiate another refund for unpaid, failed, refunded,
		// or uncertain payments.
		if p.status != "succeeded" {
			continue
		}

		switch p.fulfillmentStatus {
		case "refunded", "review_required", "refund_required":
			// Existing workflow states must not create a second job.
			continue
		case "fulfilled":
			// Eligible for a refund job below.
		default:
			continue
		}

		if p.providerPaymentID == "" {
			return Activity{}, fmt.Errorf(
				"payment %s is fulfilled but has no provider payment ID",
				p.id,
			)
		}

		idempotencyKey := "trailnest-refund-" + p.id.String()

		_, err = tx.Exec(ctx, `
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
			p.id,
			p.provider,
			p.providerPaymentID,
			idempotencyKey,
			p.amountMinor,
			p.currency,
		)
		if err != nil {
			return Activity{}, fmt.Errorf("create refund job for payment %s: %w", p.id, err)
		}

		// Verify that any pre-existing job belongs to this same
		// payment/provider request before changing payment state.
		var existingProvider string
		var existingProviderPaymentID string
		var existingIdempotencyKey string
		var existingAmount int64
		var existingCurrency string
		var existingRefundStatus string

		err = tx.QueryRow(ctx, `
			SELECT
				provider,
				provider_payment_id,
				idempotency_key,
				amount_minor,
				currency,
				status
			FROM payment_refunds
			WHERE payment_id = $1
			FOR UPDATE
		`, p.id).Scan(
			&existingProvider,
			&existingProviderPaymentID,
			&existingIdempotencyKey,
			&existingAmount,
			&existingCurrency,
			&existingRefundStatus,
		)
		if err != nil {
			return Activity{}, fmt.Errorf("verify refund job for payment %s: %w", p.id, err)
		}

		if existingProvider != p.provider ||
			existingProviderPaymentID != p.providerPaymentID ||
			existingIdempotencyKey != idempotencyKey ||
			existingAmount != p.amountMinor ||
			existingCurrency != p.currency {
			return Activity{}, fmt.Errorf(
				"existing refund job for payment %s does not match the expected refund",
				p.id,
			)
		}

		switch existingRefundStatus {
		case "pending", "processing":
			_, err = tx.Exec(ctx, `
				UPDATE payments
				SET fulfillment_status = 'refund_required',
				    updated_at = NOW()
				WHERE id = $1
			`, p.id)
		case "review_required":
			_, err = tx.Exec(ctx, `
				UPDATE payments
				SET fulfillment_status = 'review_required',
				    updated_at = NOW()
				WHERE id = $1
			`, p.id)
		case "succeeded":
			_, err = tx.Exec(ctx, `
				UPDATE payments
				SET fulfillment_status = 'refunded',
				    updated_at = NOW()
				WHERE id = $1
			`, p.id)
		case "failed":
			// Preserve the failed refund job for explicit recovery.
			// Do not enqueue another refund or mark the payment as refunded.
			continue
		default:
			return Activity{}, fmt.Errorf(
				"unexpected refund job status %q for payment %s",
				existingRefundStatus,
				p.id,
			)
		}
		if err != nil {
			return Activity{}, fmt.Errorf("update payment refund state for %s: %w", p.id, err)
		}
	}

	// Change the activity status in the same transaction as all the
	// booking, reservation, payment, and refund-job updates.
	var a Activity
	err = tx.QueryRow(ctx, `
		UPDATE activities
		SET status = 'cancelled', updated_at = NOW()
		WHERE id = $1
		RETURNING
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, created_at,
			price_minor, currency, image_url,
			capacity
			  - COALESCE((
				  SELECT SUM(b.quantity)
				  FROM bookings b
				  WHERE b.activity_id = activities.id
				    AND b.status = 'confirmed'
			  ), 0)::INTEGER
			  - COALESCE((
				  SELECT SUM(res.quantity)
				  FROM reservations res
				  WHERE res.activity_id = activities.id
				    AND res.status = 'pending'
				    AND res.expires_at > NOW()
			  ), 0)::INTEGER
	`, activityID).Scan(
		&a.ID,
		&a.OwnerID,
		&a.Title,
		&a.Description,
		&a.Location,
		&a.StartsAt,
		&a.EndsAt,
		&a.Capacity,
		&a.Status,
		&a.CreatedAt,
		&a.PriceMinor,
		&a.Currency,
		&a.ImageURL,
		&a.RemainingCapacity,
	)
	if err != nil {
		return Activity{}, fmt.Errorf("update cancelled activity: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Activity{}, fmt.Errorf("commit activity cancellation: %w", err)
	}

	return a, nil
}
