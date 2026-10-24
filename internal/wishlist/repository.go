package wishlist

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrActivityNotFound = errors.New("activity not found")
	ErrAlreadySaved     = errors.New("activity already saved")
	ErrItemNotFound     = errors.New("wishlist item not found")
)

type Item struct {
	ID         uuid.UUID `json:"id"`
	CustomerID uuid.UUID `json:"customer_id"`
	ActivityID uuid.UUID `json:"activity_id"`
	CreatedAt  string    `json:"created_at"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Add(
	ctx context.Context,
	customerID uuid.UUID,
	activityID uuid.UUID,
) (Item, error) {
	var item Item

	err := r.pool.QueryRow(ctx, `
		INSERT INTO wishlist_items (customer_id, activity_id)
		SELECT $1, a.id
		FROM activities a
		WHERE a.id = $2
		ON CONFLICT (customer_id, activity_id) DO NOTHING
		RETURNING id, customer_id, activity_id, created_at
	`, customerID, activityID).Scan(
		&item.ID,
		&item.CustomerID,
		&item.ActivityID,
		&item.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		checkErr := r.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM activities WHERE id = $1
			)
		`, activityID).Scan(&exists)
		if checkErr != nil {
			return Item{}, fmt.Errorf("check activity: %w", checkErr)
		}
		if !exists {
			return Item{}, ErrActivityNotFound
		}
		return Item{}, ErrAlreadySaved
	}

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return Item{}, ErrActivityNotFound
		}
		return Item{}, fmt.Errorf("add wishlist item: %w", err)
	}

	return item, nil
}

func (r *Repository) List(
	ctx context.Context,
	customerID uuid.UUID,
) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, customer_id, activity_id, created_at
		FROM wishlist_items
		WHERE customer_id = $1
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, fmt.Errorf("list wishlist items: %w", err)
	}
	defer rows.Close()

	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(
			&item.ID,
			&item.CustomerID,
			&item.ActivityID,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan wishlist item: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate wishlist items: %w", err)
	}

	return items, nil
}

func (r *Repository) Remove(
	ctx context.Context,
	customerID uuid.UUID,
	activityID uuid.UUID,
) error {
	result, err := r.pool.Exec(ctx, `
		DELETE FROM wishlist_items
		WHERE customer_id = $1 AND activity_id = $2
	`, customerID, activityID)
	if err != nil {
		return fmt.Errorf("remove wishlist item: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrItemNotFound
	}

	return nil
}
