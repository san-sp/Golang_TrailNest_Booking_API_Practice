package payment

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrPaymentNotFound     = errors.New("payment not found")
	ErrIdempotencyConflict = errors.New("idempotency key already used for a different request")
	ErrReservationNotFound = errors.New("reservation not found")
	ErrReservationNotReady = errors.New("reservation is not available for payment")
)

type Payment struct {
	ID                uuid.UUID `json:"id"`
	ReservationID     uuid.UUID `json:"reservation_id"`
	CustomerID        uuid.UUID `json:"customer_id"`
	AmountMinor       int64     `json:"amount_minor"`
	Currency          string    `json:"currency"`
	Status            string    `json:"status"`
	Provider          string    `json:"provider"`
	ProviderPaymentID string    `json:"provider_payment_id,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key"`
	CreatedAt         time.Time `json:"created_at"`
	ProviderOrderID   string    `json:"provider_order_id,omitempty"`
	FulfillmentStatus string    `json:"fulfillment_status"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func scanPayment(row pgx.Row) (Payment, error) {
	var payment Payment

	err := row.Scan(
		&payment.ID,
		&payment.ReservationID,
		&payment.CustomerID,
		&payment.AmountMinor,
		&payment.Currency,
		&payment.Status,
		&payment.Provider,
		&payment.ProviderPaymentID,
		&payment.ProviderOrderID,
		&payment.IdempotencyKey,
		&payment.CreatedAt,
		&payment.FulfillmentStatus,
	)

	return payment, err
}

const paymentColumns = `
    id, reservation_id, customer_id, amount_minor, currency,
    status, provider, COALESCE(provider_payment_id, ''),
    COALESCE(provider_order_id, ''),
    idempotency_key, created_at, fulfillment_status
`

func (r *Repository) findByIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	customerID uuid.UUID,
	idempotencyKey string,
) (Payment, error) {
	return scanPayment(tx.QueryRow(ctx, `
		SELECT `+paymentColumns+`
		FROM payments
		WHERE customer_id = $1 AND idempotency_key = $2
	`, customerID, idempotencyKey))
}

func validateSamePayment(
	existing Payment,
	reservationID uuid.UUID,
	amountMinor int64,
	currency string,
	provider string,
) error {
	if existing.ReservationID != reservationID ||
		existing.AmountMinor != amountMinor ||
		existing.Currency != currency ||
		existing.Provider != provider {
		return ErrIdempotencyConflict
	}

	return nil
}

func (r *Repository) CreatePending(
	ctx context.Context,
	customerID uuid.UUID,
	reservationID uuid.UUID,
	provider string,
	idempotencyKey string,
) (Payment, error) {
	provider = strings.TrimSpace(provider)
	idempotencyKey = strings.TrimSpace(idempotencyKey)

	if provider == "" || idempotencyKey == "" || len(idempotencyKey) > 255 {
		return Payment{}, errors.New("invalid payment details")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Payment{}, fmt.Errorf("begin payment transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// An existing key identifies the original checkout request.
	// Return its payment even if the reservation has since changed state.
	existing, err := r.findByIdempotencyKey(
		ctx, tx, customerID, idempotencyKey,
	)
	if err == nil {
		if existing.ReservationID != reservationID ||
			existing.Provider != provider {
			return Payment{}, ErrIdempotencyConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, fmt.Errorf("find existing payment: %w", err)
	}

	// Find the activity associated with this customer's reservation.
	var activityID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT activity_id
		FROM reservations
		WHERE id = $1 AND customer_id = $2
	`, reservationID, customerID).Scan(&activityID)

	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrReservationNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("find reservation activity: %w", err)
	}

	// Lock the activity so its price cannot change during checkout.
	var priceMinor int64
	var currency string

	err = tx.QueryRow(ctx, `
		SELECT price_minor, currency
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, activityID).Scan(&priceMinor, &currency)

	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrReservationNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("load activity pricing: %w", err)
	}
	if priceMinor <= 0 {
		return Payment{}, ErrFreeActivity
	}

	// Lock and validate the reservation after locking its activity.
	var quantity int
	var reservationStatus string
	var active bool

	err = tx.QueryRow(ctx, `
		SELECT quantity, status, expires_at > NOW()
		FROM reservations
		WHERE id = $1 AND customer_id = $2 AND activity_id = $3
		FOR UPDATE
	`, reservationID, customerID, activityID).Scan(
		&quantity,
		&reservationStatus,
		&active,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrReservationNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("lock reservation: %w", err)
	}

	if reservationStatus != "pending" || !active {
		return Payment{}, ErrReservationNotReady
	}
	if quantity < 1 {
		return Payment{}, ErrInvalidQuantity
	}
	if priceMinor > math.MaxInt64/int64(quantity) {
		return Payment{}, errors.New("payment amount overflow")
	}

	amountMinor := priceMinor * int64(quantity)
	currency = strings.ToUpper(strings.TrimSpace(currency))

	if len(currency) != 3 {
		return Payment{}, errors.New("invalid activity currency")
	}

	created, err := scanPayment(tx.QueryRow(ctx, `
		INSERT INTO payments (
			reservation_id,
			customer_id,
			amount_minor,
			currency,
			provider,
			idempotency_key
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (customer_id, idempotency_key) DO NOTHING
		RETURNING `+paymentColumns,
		reservationID,
		customerID,
		amountMinor,
		currency,
		provider,
		idempotencyKey,
	))

	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return Payment{}, fmt.Errorf("commit payment transaction: %w", err)
		}
		return created, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, fmt.Errorf("create pending payment: %w", err)
	}

	// Another request created the payment with this key.
	existing, err = r.findByIdempotencyKey(
		ctx, tx, customerID, idempotencyKey,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, errors.New("concurrent payment was not found")
	}
	if err != nil {
		return Payment{}, fmt.Errorf("load concurrent payment: %w", err)
	}
	if existing.ReservationID != reservationID ||
		existing.Provider != provider {
		return Payment{}, ErrIdempotencyConflict
	}

	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("commit payment retry: %w", err)
	}

	return existing, nil
}

func (r *Repository) SaveProviderOrderID(
	ctx context.Context,
	paymentID uuid.UUID,
	orderID string,
) (Payment, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return Payment{}, errors.New("provider order ID is required")
	}

	payment, err := scanPayment(r.pool.QueryRow(ctx, `
		UPDATE payments
		SET provider_order_id = $2,
		    updated_at = NOW()
		WHERE id = $1
		  AND (provider_order_id IS NULL OR provider_order_id = $2)
		RETURNING `+paymentColumns,
		paymentID,
		orderID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, errors.New("payment already has a different provider order")
	}
	if err != nil {
		return Payment{}, fmt.Errorf("save provider order ID: %w", err)
	}

	return payment, nil
}

func (r *Repository) GetByIDForCustomer(
	ctx context.Context,
	paymentID uuid.UUID,
	customerID uuid.UUID,
) (Payment, error) {
	payment, err := scanPayment(r.pool.QueryRow(ctx, `
		SELECT `+paymentColumns+`
		FROM payments
		WHERE id = $1 AND customer_id = $2
	`, paymentID, customerID))

	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrPaymentNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("load customer payment: %w", err)
	}

	return payment, nil
}

func (r *Repository) AssociateProviderPayment(
	ctx context.Context,
	paymentID uuid.UUID,
	orderID string,
	providerPaymentID string,
	amountMinor int64,
	currency string,
) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin payment association: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var savedOrderID string
	var savedProviderPaymentID string
	var savedAmount int64
	var savedCurrency string
	var savedProvider string

	err = tx.QueryRow(ctx, `
		SELECT
			COALESCE(provider_order_id, ''),
			COALESCE(provider_payment_id, ''),
			amount_minor,
			currency,
			provider
		FROM payments
		WHERE id = $1
		FOR UPDATE
	`, paymentID).Scan(
		&savedOrderID,
		&savedProviderPaymentID,
		&savedAmount,
		&savedCurrency,
		&savedProvider,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPaymentNotFound
	}
	if err != nil {
		return fmt.Errorf("load payment for association: %w", err)
	}

	if savedProvider != "razorpay" ||
		savedOrderID != orderID ||
		savedAmount != amountMinor ||
		!strings.EqualFold(strings.TrimSpace(savedCurrency), strings.TrimSpace(currency)) {
		return errors.New("provider payment does not match local payment")
	}

	if savedProviderPaymentID != "" &&
		savedProviderPaymentID != providerPaymentID {
		return ErrIdempotencyConflict
	}

	_, err = tx.Exec(ctx, `
		UPDATE payments
		SET provider_payment_id = $2,
		    updated_at = NOW()
		WHERE id = $1
		  AND (provider_payment_id IS NULL OR provider_payment_id = $2)
	`, paymentID, providerPaymentID)
	if err != nil {
		return fmt.Errorf("associate provider payment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit payment association: %w", err)
	}

	return nil
}

func (r *Repository) GetByProviderOrderID(
	ctx context.Context,
	provider string,
	orderID string,
) (Payment, error) {
	payment, err := scanPayment(r.pool.QueryRow(ctx, `
		SELECT `+paymentColumns+`
		FROM payments
		WHERE provider = $1 AND provider_order_id = $2
	`, provider, orderID))

	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrPaymentNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("find payment by provider order ID: %w", err)
	}

	return payment, nil
}

func (r *Repository) RecordPaymentAttempt(
	ctx context.Context,
	paymentID uuid.UUID,
	provider string,
	providerOrderID string,
	providerPaymentID string,
	amountMinor int64,
	currency string,
	status string,
) error {
	provider = strings.TrimSpace(provider)
	providerOrderID = strings.TrimSpace(providerOrderID)
	providerPaymentID = strings.TrimSpace(providerPaymentID)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	status = strings.TrimSpace(status)

	if provider == "" ||
		providerOrderID == "" ||
		providerPaymentID == "" ||
		amountMinor <= 0 ||
		len(currency) != 3 ||
		(status != "failed" && status != "captured") {
		return errors.New("invalid payment attempt")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin payment attempt transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

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
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPaymentNotFound
	}
	if err != nil {
		return fmt.Errorf("load payment for attempt: %w", err)
	}

	if savedProvider != provider ||
		savedOrderID != providerOrderID ||
		savedAmount != amountMinor ||
		!strings.EqualFold(strings.TrimSpace(savedCurrency), currency) {
		return errors.New("payment attempt does not match local payment")
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
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider, provider_payment_id)
		DO UPDATE SET
			status = CASE
				WHEN payment_attempts.status = 'captured'
					THEN 'captured'
				WHEN EXCLUDED.status = 'captured'
					THEN 'captured'
				ELSE EXCLUDED.status
			END,
			updated_at = NOW()
		WHERE payment_attempts.payment_id = EXCLUDED.payment_id
			AND payment_attempts.amount_minor = EXCLUDED.amount_minor
			AND payment_attempts.currency = EXCLUDED.currency
	`, paymentID, provider, providerPaymentID,
		amountMinor, currency, status)
	if err != nil {
		return fmt.Errorf("record payment attempt: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return errors.New("payment attempt conflicts with an existing attempt")
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit payment attempt: %w", err)
	}

	return nil
}

func (r *Repository) ListAllAdmin(
	ctx context.Context,
	limit int,
	offset int,
) ([]Payment, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.pool.Query(ctx, `
        SELECT `+paymentColumns+`
        FROM payments
        ORDER BY created_at DESC, id DESC
        LIMIT $1 OFFSET $2
    `, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list payments for admin: %w", err)
	}
	defer rows.Close()

	payments := make([]Payment, 0)

	for rows.Next() {
		payment, err := scanPayment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan admin payment: %w", err)
		}
		payments = append(payments, payment)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin payments: %w", err)
	}

	return payments, nil
}

func (r *Repository) GetAdminByID(
	ctx context.Context,
	paymentID uuid.UUID,
) (Payment, error) {
	payment, err := scanPayment(r.pool.QueryRow(ctx, `
        SELECT `+paymentColumns+`
        FROM payments
        WHERE id = $1
    `, paymentID))

	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrPaymentNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("get payment for admin: %w", err)
	}

	return payment, nil
}
