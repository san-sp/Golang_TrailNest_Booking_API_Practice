package reservation

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
	ErrActivityNotFound     = errors.New("activity not found")
	ErrActivityNotBookable  = errors.New("activity is not available for reservation")
	ErrInsufficientCapacity = errors.New("not enough capacity available")
	ErrReservationNotFound  = errors.New("reservation not found")
)

const ReservationLifetime = 15 * time.Minute

type Reservation struct {
	ID         uuid.UUID `json:"id"`
	CustomerID uuid.UUID `json:"customer_id"`
	ActivityID uuid.UUID `json:"activity_id"`
	Quantity   int       `json:"quantity"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
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
) (Reservation, error) {
	if quantity < 1 || quantity > 10 {
		return Reservation{}, ErrInsufficientCapacity
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Reservation{}, fmt.Errorf("begin reservation transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var capacity int
	var activityStatus string
	var startsAt time.Time

	err = tx.QueryRow(ctx, `
		SELECT capacity, status, starts_at
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, activityID).Scan(&capacity, &activityStatus, &startsAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, ErrActivityNotFound
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("load activity: %w", err)
	}

	if activityStatus != "published" || !time.Now().Before(startsAt) {
		return Reservation{}, ErrActivityNotBookable
	}

	var bookedQuantity int
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::INTEGER
		FROM bookings
		WHERE activity_id = $1
		  AND status = 'confirmed'
	`, activityID).Scan(&bookedQuantity)
	if err != nil {
		return Reservation{}, fmt.Errorf("count confirmed bookings: %w", err)
	}

	var reservedQuantity int
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::INTEGER
		FROM reservations
		WHERE activity_id = $1
		  AND status = 'pending'
		  AND expires_at > NOW()
	`, activityID).Scan(&reservedQuantity)
	if err != nil {
		return Reservation{}, fmt.Errorf("count active reservations: %w", err)
	}

	available := capacity - bookedQuantity - reservedQuantity
	if quantity > available {
		return Reservation{}, ErrInsufficientCapacity
	}

	var created Reservation
	err = tx.QueryRow(ctx, `
		INSERT INTO reservations (
			customer_id,
			activity_id,
			quantity,
			expires_at
		)
		VALUES ($1, $2, $3, NOW() + INTERVAL '15 minutes')
		RETURNING id, customer_id, activity_id, quantity,
		          status, expires_at, created_at
	`, customerID, activityID, quantity).Scan(
		&created.ID,
		&created.CustomerID,
		&created.ActivityID,
		&created.Quantity,
		&created.Status,
		&created.ExpiresAt,
		&created.CreatedAt,
	)
	if err != nil {
		return Reservation{}, fmt.Errorf("create reservation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("commit reservation: %w", err)
	}

	return created, nil
}

func (r *Repository) ListAllAdmin(
	ctx context.Context,
	limit int,
	offset int,
) ([]Reservation, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id, customer_id, activity_id, quantity,
			status, expires_at, created_at
		FROM reservations
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list all reservations for admin: %w", err)
	}
	defer rows.Close()

	items := make([]Reservation, 0)

	for rows.Next() {
		var item Reservation
		if err := rows.Scan(
			&item.ID,
			&item.CustomerID,
			&item.ActivityID,
			&item.Quantity,
			&item.Status,
			&item.ExpiresAt,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin reservation: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin reservations: %w", err)
	}

	return items, nil
}

func (r *Repository) GetAdminByID(
	ctx context.Context,
	reservationID uuid.UUID,
) (Reservation, error) {
	var item Reservation

	err := r.pool.QueryRow(ctx, `
		SELECT
			id, customer_id, activity_id, quantity,
			status, expires_at, created_at
		FROM reservations
		WHERE id = $1
	`, reservationID).Scan(
		&item.ID,
		&item.CustomerID,
		&item.ActivityID,
		&item.Quantity,
		&item.Status,
		&item.ExpiresAt,
		&item.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, ErrReservationNotFound
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("get admin reservation: %w", err)
	}

	return item, nil
}
