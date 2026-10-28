package booking

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
	ErrActivityNotFound           = errors.New("activity not found")
	ErrActivityNotBookable        = errors.New("activity is not available for booking")
	ErrInsufficientCapacity       = errors.New("not enough capacity available")
	ErrBookingNotFound            = errors.New("booking not found")
	ErrBookingAlreadyCancelled    = errors.New("booking already cancelled")
	ErrCancellationDeadlinePassed = errors.New(
		"booking cancellation deadline has passed",
	)
	ErrPaymentRequired = errors.New("payment is required for this activity")
)

type Booking struct {
	ID               uuid.UUID `json:"id"`
	CustomerID       uuid.UUID `json:"customer_id"`
	ActivityID       uuid.UUID `json:"activity_id"`
	Quantity         int       `json:"quantity"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	ActivityTitle    string    `json:"activity_title"`
	ActivityLocation string    `json:"activity_location"`
	ActivityStartsAt time.Time `json:"activity_starts_at"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(
	ctx context.Context,
	customerID uuid.UUID,
	activityID uuid.UUID,
	quantity int,
) (Booking, error) {
	if quantity <= 0 {
		return Booking{}, ErrInsufficientCapacity
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Booking{}, fmt.Errorf("begin booking transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var capacity int
	var status string
	var startsAt time.Time
	var priceMinor int64

	err = tx.QueryRow(ctx, `
    SELECT capacity, status, starts_at, price_minor
    FROM activities
    WHERE id = $1
    FOR UPDATE
`, activityID).Scan(
		&capacity,
		&status,
		&startsAt,
		&priceMinor,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrActivityNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("load activity: %w", err)
	}

	if !time.Now().Before(startsAt) {
		return Booking{}, ErrActivityNotBookable
	}

	if status != "published" {
		return Booking{}, ErrActivityNotBookable
	}

	if priceMinor > 0 {
		return Booking{}, ErrPaymentRequired
	}

	var bookedQuantity int
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::INTEGER
		FROM bookings
		WHERE activity_id = $1
		  AND status = 'confirmed'
	`, activityID).Scan(&bookedQuantity)
	if err != nil {
		return Booking{}, fmt.Errorf("calculate booked capacity: %w", err)
	}

	if quantity > capacity-bookedQuantity {
		return Booking{}, ErrInsufficientCapacity
	}

	var created Booking
	err = tx.QueryRow(ctx, `
    INSERT INTO bookings (
			customer_id,
			activity_id,
			quantity
    )
    VALUES ($1, $2, $3)
    RETURNING id, customer_id, activity_id, quantity, status, created_at
`, customerID, activityID, quantity).Scan(
		&created.ID,
		&created.CustomerID,
		&created.ActivityID,
		&created.Quantity,
		&created.Status,
		&created.CreatedAt,
	)
	if err != nil {
		return Booking{}, fmt.Errorf("create booking: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Booking{}, fmt.Errorf("commit booking transaction: %w", err)
	}

	return created, nil
}

func (r *Repository) Cancel(
	ctx context.Context,
	customerID uuid.UUID,
	bookingID uuid.UUID,
) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin cancellation transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Find the activity and optional reservation without locking the booking
	// row first. The activity lock serializes cancellation with payment webhooks
	// and activity cancellation, which use the same lock order.
	var activityID uuid.UUID
	var reservationID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT
			activity_id,
			COALESCE(reservation_id, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM bookings
		WHERE id = $1 AND customer_id = $2
	`, bookingID, customerID).Scan(&activityID, &reservationID)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBookingNotFound
	}
	if err != nil {
		return fmt.Errorf("find booking activity: %w", err)
	}

	// Use the same lock order as booking creation.
	var startsAt time.Time

	err = tx.QueryRow(ctx, `
    SELECT starts_at
    FROM activities
    WHERE id = $1
    FOR UPDATE
`, activityID).Scan(&startsAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrActivityNotFound
	}
	if err != nil {
		return fmt.Errorf("lock activity: %w", err)
	}

	if reservationID != uuid.Nil {
		var reservationStatus string
		err = tx.QueryRow(ctx, `
			SELECT status
			FROM reservations
			WHERE id = $1
			FOR UPDATE
		`, reservationID).Scan(&reservationStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBookingNotFound
		}
		if err != nil {
			return fmt.Errorf("lock booking reservation: %w", err)
		}
	}

	var status string
	err = tx.QueryRow(ctx, `
		SELECT status
		FROM bookings
		WHERE id = $1 AND customer_id = $2
		FOR UPDATE
	`, bookingID, customerID).Scan(&status)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBookingNotFound
	}
	if err != nil {
		return fmt.Errorf("lock booking: %w", err)
	}

	if status == "cancelled" {
		return ErrBookingAlreadyCancelled
	}

	if !time.Now().Before(startsAt.Add(-24 * time.Hour)) {
		return ErrCancellationDeadlinePassed
	}

	// Keep the shared payment workflow lock order:
	// activity -> reservation -> booking -> payment.
	if reservationID != uuid.Nil {
		type paymentForRefund struct {
			id                uuid.UUID
			status            string
			fulfillmentStatus string
			provider          string
			providerPaymentID string
			amountMinor       int64
			currency          string
		}
		rows, queryErr := tx.Query(ctx, `
			SELECT
				id, status, fulfillment_status, provider,
				COALESCE(provider_payment_id, ''), amount_minor, currency
			FROM payments
			WHERE reservation_id = $1
			ORDER BY id
			FOR UPDATE
		`, reservationID)
		if queryErr != nil {
			return fmt.Errorf("lock booking payments: %w", queryErr)
		}

		payments := make([]paymentForRefund, 0, 1)
		for rows.Next() {
			var p paymentForRefund
			if scanErr := rows.Scan(
				&p.id, &p.status, &p.fulfillmentStatus, &p.provider,
				&p.providerPaymentID, &p.amountMinor, &p.currency,
			); scanErr != nil {
				rows.Close()
				return fmt.Errorf("scan booking payment: %w", scanErr)
			}
			payments = append(payments, p)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return fmt.Errorf("iterate booking payments: %w", rowsErr)
		}
		rows.Close()

		for _, p := range payments {
			// Only a successfully captured and fulfilled payment is eligible
			// for a customer-cancellation refund.
			if p.status != "succeeded" || p.fulfillmentStatus != "fulfilled" {
				continue
			}
			if p.providerPaymentID == "" {
				return fmt.Errorf(
					"payment %s is fulfilled but has no provider payment ID",
					p.id,
				)
			}

			idempotencyKey := "trailnest-refund-" + p.id.String()
			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					payment_id, provider, provider_payment_id,
					idempotency_key, amount_minor, currency, status
				)
				VALUES ($1, $2, $3, $4, $5, $6, 'pending')
				ON CONFLICT (payment_id) DO NOTHING
			`, p.id, p.provider, p.providerPaymentID, idempotencyKey, p.amountMinor, p.currency)
			if err != nil {
				return fmt.Errorf("create refund job for payment %s: %w", p.id, err)
			}

			var existingProvider, existingProviderPaymentID, existingKey string
			var existingAmount int64
			var existingCurrency, refundStatus string
			err = tx.QueryRow(ctx, `
				SELECT provider, provider_payment_id, idempotency_key,
				       amount_minor, currency, status
				FROM payment_refunds
				WHERE payment_id = $1
				FOR UPDATE
			`, p.id).Scan(
				&existingProvider, &existingProviderPaymentID, &existingKey,
				&existingAmount, &existingCurrency, &refundStatus,
			)
			if err != nil {
				return fmt.Errorf("verify refund job for payment %s: %w", p.id, err)
			}
			if existingProvider != p.provider ||
				existingProviderPaymentID != p.providerPaymentID ||
				existingKey != idempotencyKey ||
				existingAmount != p.amountMinor ||
				existingCurrency != p.currency {
				return fmt.Errorf(
					"existing refund job for payment %s does not match the expected refund",
					p.id,
				)
			}

			var fulfillmentStatus string
			switch refundStatus {
			case "pending", "processing":
				fulfillmentStatus = "refund_required"
			case "review_required":
				fulfillmentStatus = "review_required"
			case "succeeded":
				fulfillmentStatus = "refunded"
			case "failed":
				// Keep failed jobs visible for explicit recovery. Do not
				// enqueue another refund or mark the payment as refunded.
				continue
			default:
				return fmt.Errorf("unexpected refund status %q for payment %s", refundStatus, p.id)
			}
			_, err = tx.Exec(ctx, `
				UPDATE payments
				SET fulfillment_status = $2, updated_at = NOW()
				WHERE id = $1
			`, p.id, fulfillmentStatus)
			if err != nil {
				return fmt.Errorf("update payment refund state for %s: %w", p.id, err)
			}
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE bookings
		SET status = 'cancelled',
		    updated_at = NOW()
		WHERE id = $1 AND customer_id = $2
	`, bookingID, customerID)
	if err != nil {
		return fmt.Errorf("cancel booking: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancellation: %w", err)
	}

	return nil
}

func (r *Repository) ListByCustomer(
	ctx context.Context,
	customerID uuid.UUID,
) ([]Booking, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			b.id,
			b.customer_id,
			b.activity_id,
			b.quantity,
			b.status,
			b.created_at,
			a.title,
			a.location,
			a.starts_at
		FROM bookings b
		JOIN activities a ON a.id = b.activity_id
		WHERE b.customer_id = $1
		ORDER BY b.created_at DESC
	`, customerID)
	if err != nil {
		return nil, fmt.Errorf("list customer bookings: %w", err)
	}
	defer rows.Close()

	bookings := make([]Booking, 0)

	for rows.Next() {
		var b Booking

		if err := rows.Scan(
			&b.ID,
			&b.CustomerID,
			&b.ActivityID,
			&b.Quantity,
			&b.Status,
			&b.CreatedAt,
			&b.ActivityTitle,
			&b.ActivityLocation,
			&b.ActivityStartsAt,
		); err != nil {
			return nil, fmt.Errorf("scan customer booking: %w", err)
		}

		bookings = append(bookings, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate customer bookings: %w", err)
	}

	return bookings, nil
}

func (r *Repository) GetByID(
	ctx context.Context,
	customerID uuid.UUID,
	bookingID uuid.UUID,
) (Booking, error) {
	var b Booking

	err := r.pool.QueryRow(ctx, `
		SELECT
			b.id,
			b.customer_id,
			b.activity_id,
			b.quantity,
			b.status,
			b.created_at,
			a.title,
			a.location,
			a.starts_at
		FROM bookings b
		JOIN activities a ON a.id = b.activity_id
		WHERE b.id = $1
		  AND b.customer_id = $2
	`, bookingID, customerID).Scan(
		&b.ID,
		&b.CustomerID,
		&b.ActivityID,
		&b.Quantity,
		&b.Status,
		&b.CreatedAt,
		&b.ActivityTitle,
		&b.ActivityLocation,
		&b.ActivityStartsAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrBookingNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("get customer booking: %w", err)
	}

	return b, nil
}

func (r *Repository) ListAllAdmin(
	ctx context.Context,
	limit int,
	offset int,
) ([]Booking, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			b.id,
			b.customer_id,
			b.activity_id,
			b.quantity,
			b.status,
			b.created_at,
			a.title,
			a.location,
			a.starts_at
		FROM bookings b
		JOIN activities a ON a.id = b.activity_id
		ORDER BY b.created_at DESC, b.id DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list all bookings for admin: %w", err)
	}
	defer rows.Close()

	bookings := make([]Booking, 0)

	for rows.Next() {
		var b Booking
		if err := rows.Scan(
			&b.ID,
			&b.CustomerID,
			&b.ActivityID,
			&b.Quantity,
			&b.Status,
			&b.CreatedAt,
			&b.ActivityTitle,
			&b.ActivityLocation,
			&b.ActivityStartsAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin booking: %w", err)
		}

		bookings = append(bookings, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin bookings: %w", err)
	}

	return bookings, nil
}

func (r *Repository) GetAdminByID(
	ctx context.Context,
	bookingID uuid.UUID,
) (Booking, error) {
	var b Booking

	err := r.pool.QueryRow(ctx, `
		SELECT
			b.id,
			b.customer_id,
			b.activity_id,
			b.quantity,
			b.status,
			b.created_at,
			a.title,
			a.location,
			a.starts_at
		FROM bookings b
		JOIN activities a ON a.id = b.activity_id
		WHERE b.id = $1
	`, bookingID).Scan(
		&b.ID,
		&b.CustomerID,
		&b.ActivityID,
		&b.Quantity,
		&b.Status,
		&b.CreatedAt,
		&b.ActivityTitle,
		&b.ActivityLocation,
		&b.ActivityStartsAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrBookingNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("get admin booking: %w", err)
	}

	return b, nil
}
