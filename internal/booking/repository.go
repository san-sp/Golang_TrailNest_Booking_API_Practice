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
	ErrActivityNotFound        = errors.New("activity not found")
	ErrActivityNotBookable     = errors.New("activity is not available for booking")
	ErrInsufficientCapacity    = errors.New("not enough capacity available")
	ErrBookingNotFound         = errors.New("booking not found")
	ErrBookingAlreadyCancelled = errors.New("booking already cancelled")
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

	err = tx.QueryRow(ctx, `
		SELECT capacity, status, starts_at
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, activityID).Scan(&capacity, &status, &startsAt)

	if !time.Now().Before(startsAt) {
		return Booking{}, ErrActivityNotBookable
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrActivityNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("load activity: %w", err)
	}

	if status != "published" {
		return Booking{}, ErrActivityNotBookable
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

	// Find the activity without locking the booking row first.
	var activityID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT activity_id
		FROM bookings
		WHERE id = $1 AND customer_id = $2
	`, bookingID, customerID).Scan(&activityID)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBookingNotFound
	}
	if err != nil {
		return fmt.Errorf("find booking activity: %w", err)
	}

	// Use the same lock order as booking creation.
	var lockedActivityID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, activityID).Scan(&lockedActivityID)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrActivityNotFound
	}
	if err != nil {
		return fmt.Errorf("lock activity: %w", err)
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
