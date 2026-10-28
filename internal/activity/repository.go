package activity

import (
	"context"
	"errors"
	"fmt"
	"time"
	"trailnest/internal/cancellation"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrActivityNotFound = errors.New("activity not found")
var ErrActivityNotOwned = errors.New("activity is not owned by this operator")

type Activity struct {
	ID                uuid.UUID `json:"id"`
	OwnerID           uuid.UUID `json:"owner_id"`
	Title             string    `json:"title"`
	Description       string    `json:"description"`
	Location          string    `json:"location"`
	StartsAt          time.Time `json:"starts_at"`
	EndsAt            time.Time `json:"ends_at"`
	Capacity          int       `json:"capacity"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	RemainingCapacity int       `json:"remaining_capacity"`
	PriceMinor        int64     `json:"price_minor"`
	Currency          string    `json:"currency"`
	ImageURL          *string   `json:"image_url"`
}

type Repository struct {
	pool *pgxpool.Pool
}

type CreateActivityInput struct {
	OwnerID     uuid.UUID
	Title       string
	Description string
	Location    string
	StartsAt    time.Time
	EndsAt      time.Time
	Capacity    int
	PriceMinor  int64
	Currency    string
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) ListPublished(
	ctx context.Context,
	limit int,
	offset int,
	search string,
	location string,
) ([]Activity, error) {

	rows, err := r.pool.Query(ctx, `
    SELECT
			a.id,
			a.owner_id,
			a.title,
			a.description,
			a.location,
			a.starts_at,
			a.ends_at,
			a.capacity,
			a.status,
			a.created_at,
			a.price_minor,
			a.currency,
			a.image_url,
			a.capacity
			- COALESCE((
					SELECT SUM(b.quantity)
					FROM bookings b
					WHERE b.activity_id = a.id
						AND b.status = 'confirmed'
			), 0)::INTEGER
			- COALESCE((
					SELECT SUM(r.quantity)
					FROM reservations r
					WHERE r.activity_id = a.id
						AND r.status = 'pending'
						AND r.expires_at > NOW()
			), 0)::INTEGER AS remaining_capacity
		FROM activities a
		WHERE a.status = 'published'
			AND a.starts_at > NOW()
			AND (
					$1 = ''
					OR a.title ILIKE '%' || $1 || '%'
					OR a.description ILIKE '%' || $1 || '%'
			)
			AND (
					$2 = ''
					OR a.location ILIKE '%' || $2 || '%'
			)
		ORDER BY a.starts_at ASC
		LIMIT $3 OFFSET $4
`, search, location, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list published activities: %w", err)
	}

	defer rows.Close()

	activities := make([]Activity, 0)

	for rows.Next() {
		var a Activity

		if err := rows.Scan(
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
		); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}

		activities = append(activities, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activities: %w", err)
	}

	return activities, nil
}

func (r *Repository) GetPublishedByID(
	ctx context.Context,
	id uuid.UUID,
) (Activity, error) {
	var a Activity

	err := r.pool.QueryRow(ctx, `
		SELECT
			a.id,
			a.owner_id,
			a.title,
			a.description,
			a.location,
			a.starts_at,
			a.ends_at,
			a.capacity,
			a.status,
			a.created_at,
			a.price_minor,
			a.currency,
			a.image_url,
			a.capacity
				- COALESCE((
					SELECT SUM(b.quantity)
					FROM bookings b
					WHERE b.activity_id = a.id
					  AND b.status = 'confirmed'
				), 0)::INTEGER
				- COALESCE((
					SELECT SUM(res.quantity)
					FROM reservations res
					WHERE res.activity_id = a.id
					  AND res.status = 'pending'
					  AND res.expires_at > NOW()
				), 0)::INTEGER AS remaining_capacity
		FROM activities a
		WHERE a.id = $1
		  AND a.status = 'published'
		  AND a.starts_at > NOW()
	`, id).Scan(
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

	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("get published activity: %w", err)
	}

	return a, nil
}

func (r *Repository) Create(
	ctx context.Context,
	input CreateActivityInput,
) (Activity, error) {
	var a Activity

	err := r.pool.QueryRow(ctx, `
		INSERT INTO activities (
			owner_id, title, description, location,
			starts_at, ends_at, capacity, status,
			price_minor, currency
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'draft', $8, $9)
		RETURNING
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, created_at,
			price_minor, currency, image_url, capacity
	`,
		input.OwnerID,
		input.Title,
		input.Description,
		input.Location,
		input.StartsAt,
		input.EndsAt,
		input.Capacity,
		input.PriceMinor,
		input.Currency,
	).Scan(
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
		return Activity{}, fmt.Errorf("create activity: %w", err)
	}

	return a, nil
}

func (r *Repository) ListAdmin(
	ctx context.Context,
	limit int,
	offset int,
) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			a.id, a.owner_id, a.title, a.description, a.location,
			a.starts_at, a.ends_at, a.capacity, a.status, a.created_at,
			a.price_minor, a.currency, a.image_url,
			a.capacity
				- COALESCE((
					SELECT SUM(b.quantity)
					FROM bookings b
					WHERE b.activity_id = a.id
						AND b.status = 'confirmed'
				), 0)::INTEGER
				- COALESCE((
					SELECT SUM(res.quantity)
					FROM reservations res
					WHERE res.activity_id = a.id
						AND res.status = 'pending'
						AND res.expires_at > NOW()
				), 0)::INTEGER AS remaining_capacity
		FROM activities a
		ORDER BY a.created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list admin activities: %w", err)
	}
	defer rows.Close()

	activities := make([]Activity, 0)
	for rows.Next() {
		var a Activity
		if err := rows.Scan(
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
		); err != nil {
			return nil, fmt.Errorf("scan admin activity: %w", err)
		}
		activities = append(activities, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin activities: %w", err)
	}

	return activities, nil
}

func (r *Repository) ListByOwner(
	ctx context.Context,
	ownerID uuid.UUID,
	limit int,
	offset int,
) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			a.id, a.owner_id, a.title, a.description, a.location,
			a.starts_at, a.ends_at, a.capacity, a.status, a.created_at,
			a.price_minor, a.currency, a.image_url,
			a.capacity
				- COALESCE((
					SELECT SUM(b.quantity)
					FROM bookings b
					WHERE b.activity_id = a.id
					  AND b.status = 'confirmed'
				), 0)::INTEGER
				- COALESCE((
					SELECT SUM(res.quantity)
					FROM reservations res
					WHERE res.activity_id = a.id
					  AND res.status = 'pending'
					  AND res.expires_at > NOW()
				), 0)::INTEGER AS remaining_capacity
		FROM activities a
		WHERE a.owner_id = $1
		ORDER BY a.created_at DESC
		LIMIT $2 OFFSET $3
	`, ownerID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list operator activities: %w", err)
	}
	defer rows.Close()

	activities := make([]Activity, 0)
	for rows.Next() {
		var a Activity
		if err := rows.Scan(
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
		); err != nil {
			return nil, fmt.Errorf("scan operator activity: %w", err)
		}
		activities = append(activities, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operator activities: %w", err)
	}

	return activities, nil
}

func (r *Repository) GetByOwner(
	ctx context.Context,
	ownerID uuid.UUID,
	id uuid.UUID,
) (Activity, error) {
	var a Activity

	err := r.pool.QueryRow(ctx, `
		SELECT
			a.id, a.owner_id, a.title, a.description, a.location,
			a.starts_at, a.ends_at, a.capacity, a.status, a.created_at,
			a.price_minor, a.currency, a.image_url,
			a.capacity
				- COALESCE((
					SELECT SUM(b.quantity)
					FROM bookings b
					WHERE b.activity_id = a.id
					  AND b.status = 'confirmed'
				), 0)::INTEGER
				- COALESCE((
					SELECT SUM(res.quantity)
					FROM reservations res
					WHERE res.activity_id = a.id
					  AND res.status = 'pending'
					  AND res.expires_at > NOW()
				), 0)::INTEGER AS remaining_capacity
		FROM activities a
		WHERE a.id = $1 AND a.owner_id = $2
	`, id, ownerID).Scan(
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

	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("get operator activity: %w", err)
	}

	return a, nil
}

func (r *Repository) UpdateImageURLByOwner(
	ctx context.Context,
	ownerID uuid.UUID,
	id uuid.UUID,
	imageURL string,
) (Activity, error) {
	var a Activity

	err := r.pool.QueryRow(ctx, `
        UPDATE activities
        SET image_url = $3,
            updated_at = NOW()
        WHERE id = $1
          AND owner_id = $2
          AND status <> 'cancelled'
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
                ), 0)::INTEGER AS remaining_capacity
    `, id, ownerID, imageURL).Scan(
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

	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("update activity image: %w", err)
	}

	return a, nil
}

func (r *Repository) GetAdminByID(
	ctx context.Context,
	id uuid.UUID,
) (Activity, error) {
	var a Activity

	err := r.pool.QueryRow(ctx, `
		SELECT
			a.id, a.owner_id, a.title, a.description, a.location,
			a.starts_at, a.ends_at, a.capacity, a.status, a.created_at,
			a.price_minor, a.currency, a.image_url,
			a.capacity
				- COALESCE((
					SELECT SUM(b.quantity)
					FROM bookings b
					WHERE b.activity_id = a.id
						AND b.status = 'confirmed'
				), 0)::INTEGER
				- COALESCE((
					SELECT SUM(res.quantity)
					FROM reservations res
					WHERE res.activity_id = a.id
						AND res.status = 'pending'
						AND res.expires_at > NOW()
				), 0)::INTEGER AS remaining_capacity
		FROM activities a
		WHERE a.id = $1
	`, id).Scan(
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
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("get admin activity: %w", err)
	}

	return a, nil
}

var ErrCapacityBelowBookings = errors.New(
	"capacity cannot be lower than confirmed bookings and active reservations",
)

var ErrInvalidActivityTransition = errors.New(
	"invalid activity status transition",
)

type UpdateActivityInput struct {
	Title       string
	Description string
	Location    string
	StartsAt    time.Time
	EndsAt      time.Time
	Capacity    int
	PriceMinor  int64
	Currency    string
}

func (r *Repository) UpdateDetails(
	ctx context.Context,
	id uuid.UUID,
	input UpdateActivityInput,
) (Activity, error) {
	return r.updateDetails(ctx, id, nil, input)
}

func (r *Repository) UpdateDetailsByOwner(
	ctx context.Context,
	ownerID uuid.UUID,
	id uuid.UUID,
	input UpdateActivityInput,
) (Activity, error) {
	return r.updateDetails(ctx, id, &ownerID, input)
}

func (r *Repository) updateDetails(
	ctx context.Context,
	id uuid.UUID,
	ownerID *uuid.UUID,
	input UpdateActivityInput,
) (Activity, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Activity{}, fmt.Errorf("begin activity update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatus string
	var currentOwnerID uuid.UUID

	err = tx.QueryRow(ctx, `
		SELECT status, owner_id
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(&currentStatus, &currentOwnerID)

	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("lock activity for update: %w", err)
	}

	if ownerID != nil && currentOwnerID != *ownerID {
		return Activity{}, ErrActivityNotFound
	}

	if currentStatus == "cancelled" {
		return Activity{}, ErrInvalidActivityTransition
	}

	var confirmed, reserved int

	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::INTEGER
		FROM bookings
		WHERE activity_id = $1 AND status = 'confirmed'
	`, id).Scan(&confirmed)
	if err != nil {
		return Activity{}, fmt.Errorf("count confirmed bookings: %w", err)
	}

	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::INTEGER
		FROM reservations
		WHERE activity_id = $1
		  AND status = 'pending'
		  AND expires_at > NOW()
	`, id).Scan(&reserved)
	if err != nil {
		return Activity{}, fmt.Errorf("count active reservations: %w", err)
	}

	if input.Capacity < confirmed+reserved {
		return Activity{}, ErrCapacityBelowBookings
	}

	var a Activity
	err = tx.QueryRow(ctx, `
		UPDATE activities
		SET title = $2,
		    description = $3,
		    location = $4,
		    starts_at = $5,
		    ends_at = $6,
		    capacity = $7,
		    price_minor = $8,
		    currency = $9,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING
		    id, owner_id, title, description, location,
		    starts_at, ends_at, capacity, status, created_at,
		    price_minor, currency, image_url,
				capacity - $10::INTEGER - $11::INTEGER
	`,
		id,
		input.Title,
		input.Description,
		input.Location,
		input.StartsAt,
		input.EndsAt,
		input.Capacity,
		input.PriceMinor,
		input.Currency,
		confirmed,
		reserved,
	).Scan(
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
		return Activity{}, fmt.Errorf("update activity details: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Activity{}, fmt.Errorf("commit activity update: %w", err)
	}

	return a, nil
}

func (r *Repository) ChangeStatus(
	ctx context.Context,
	id uuid.UUID,
	newStatus string,
) (Activity, error) {
	return r.changeStatus(ctx, id, nil, newStatus)
}

func (r *Repository) ChangeStatusByOwner(
	ctx context.Context,
	ownerID uuid.UUID,
	id uuid.UUID,
	newStatus string,
) (Activity, error) {
	return r.changeStatus(ctx, id, &ownerID, newStatus)
}

func (r *Repository) changeStatus(
	ctx context.Context,
	id uuid.UUID,
	ownerID *uuid.UUID,
	newStatus string,
) (Activity, error) {
	if newStatus != "published" && newStatus != "cancelled" {
		return Activity{}, ErrInvalidActivityTransition
	}

	if newStatus == "cancelled" {
		service := cancellation.NewService(r.pool)

		cancelled, err := service.CancelActivity(ctx, id, ownerID)
		if err != nil {
			if errors.Is(err, cancellation.ErrActivityNotFound) {
				return Activity{}, ErrActivityNotFound
			}
			if errors.Is(err, cancellation.ErrInvalidActivityTransition) {
				return Activity{}, ErrInvalidActivityTransition
			}
			return Activity{}, err
		}

		return Activity{
			ID:                cancelled.ID,
			OwnerID:           cancelled.OwnerID,
			Title:             cancelled.Title,
			Description:       cancelled.Description,
			Location:          cancelled.Location,
			StartsAt:          cancelled.StartsAt,
			EndsAt:            cancelled.EndsAt,
			Capacity:          cancelled.Capacity,
			Status:            cancelled.Status,
			CreatedAt:         cancelled.CreatedAt,
			RemainingCapacity: cancelled.RemainingCapacity,
			PriceMinor:        cancelled.PriceMinor,
			Currency:          cancelled.Currency,
			ImageURL:          cancelled.ImageURL,
		}, nil
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Activity{}, fmt.Errorf("begin activity status change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatus string
	var startsAt time.Time
	var currentOwnerID uuid.UUID

	err = tx.QueryRow(ctx, `
		SELECT status, starts_at, owner_id
		FROM activities
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(&currentStatus, &startsAt, &currentOwnerID)

	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("lock activity for status change: %w", err)
	}

	if ownerID != nil && currentOwnerID != *ownerID {
		return Activity{}, ErrActivityNotFound
	}

	if currentStatus == "cancelled" ||
		(currentStatus == "published" && newStatus == "published") {
		return Activity{}, ErrInvalidActivityTransition
	}

	if newStatus == "published" && !startsAt.After(time.Now()) {
		return Activity{}, ErrInvalidActivityTransition
	}

	var a Activity
	err = tx.QueryRow(ctx, `
		UPDATE activities
		SET status = $2, updated_at = NOW()
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
	`, id, newStatus).Scan(
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
		return Activity{}, fmt.Errorf("change activity status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Activity{}, fmt.Errorf("commit activity status change: %w", err)
	}

	return a, nil
}
