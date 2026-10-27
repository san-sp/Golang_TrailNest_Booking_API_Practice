package activity

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func openActivityTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)

	if config.ConnConfig.Database != "trailnest_test" {
		t.Fatalf(
			"refusing to run integration tests against database %q",
			config.ConnConfig.Database,
		)
	}

	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	require.NoError(t, err)

	require.NoError(t, pool.Ping(t.Context()))
	t.Cleanup(pool.Close)

	return pool
}

func TestListPublishedCalculatesRemainingCapacity(t *testing.T) {
	pool := openActivityTestPool(t)

	ctx := t.Context()
	customerID := uuid.New()
	activityID := uuid.New()
	reservationID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, customerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Capacity test activity', 'Test description',
			'Test location', NOW() + INTERVAL '2 days',
			NOW() + INTERVAL '3 days', 10, 'published', 2500, 'INR'
		)
	`, activityID, customerID)
	require.NoError(t, err)

	// Three confirmed booking spots.
	_, err = pool.Exec(ctx, `
		INSERT INTO bookings (customer_id, activity_id, quantity, status)
		VALUES ($1, $2, 3, 'confirmed')
	`, customerID, activityID)
	require.NoError(t, err)

	// Two unexpired pending reservation spots.
	_, err = pool.Exec(ctx, `
		INSERT INTO reservations (
			id, customer_id, activity_id, quantity, status, expires_at
		)
		VALUES ($1, $2, $3, 2, 'pending', NOW() + INTERVAL '1 hour')
	`, reservationID, customerID, activityID)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		_, err := pool.Exec(cleanupCtx,
			`DELETE FROM reservations WHERE activity_id = $1`, activityID)
		if err != nil {
			t.Errorf("cleanup reservations: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM bookings WHERE activity_id = $1`, activityID)
		if err != nil {
			t.Errorf("cleanup bookings: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM activities WHERE id = $1`, activityID)
		if err != nil {
			t.Errorf("cleanup activity: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM users WHERE id = $1`, customerID)
		if err != nil {
			t.Errorf("cleanup customer: %v", err)
		}
	})

	repo := NewRepository(pool)

	activities, err := repo.ListPublished(ctx, 20, 0, "Capacity test", "Test location")
	require.NoError(t, err)

	var found *Activity
	for i := range activities {
		if activities[i].ID == activityID {
			found = &activities[i]
			break
		}
	}

	require.NotNil(t, found, "expected published activity in search results")
	require.Equal(t, 10, found.Capacity)
	require.Equal(t, 5, found.RemainingCapacity)

	// Expired pending reservations must no longer consume capacity,
	// even if the expiry worker has not updated their status yet.
	_, err = pool.Exec(ctx, `
		UPDATE reservations
		SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, reservationID)
	require.NoError(t, err)

	activities, err = repo.ListPublished(ctx, 20, 0, "Capacity test", "Test location")
	require.NoError(t, err)

	found = nil
	for i := range activities {
		if activities[i].ID == activityID {
			found = &activities[i]
			break
		}
	}

	require.NotNil(t, found, "expected activity after reservation expiry")
	require.Equal(t, 7, found.RemainingCapacity)
}

func TestListPublishedSearchLocationAndPagination(t *testing.T) {
	pool := openActivityTestPool(t)
	ctx := t.Context()

	ownerID := uuid.New()
	ownerEmail := uuid.NewString() + "@example.test"
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, ownerID, ownerEmail)
	require.NoError(t, err)

	activityIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cancel()

		_, err := pool.Exec(cleanupCtx,
			`DELETE FROM activities WHERE owner_id = $1`, ownerID)
		if err != nil {
			t.Errorf("cleanup activities: %v", err)
		}
		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM users WHERE id = $1`, ownerID)
		if err != nil {
			t.Errorf("cleanup owner: %v", err)
		}
	})

	// All activities start in the future, but have different titles and locations.
	fixtures := []struct {
		title       string
		description string
		location    string
		status      string
		startOffset string
	}{
		{"Kerala Backwater Tour", "Boat trip", "Alappuzha, Kerala", "published", "2 days"},
		{"Kerala Mountain Trek", "Hiking adventure", "Munnar, Kerala", "published", "3 days"},
		{"City Food Walk", "Kerala cuisine", "Kochi, Kerala", "published", "4 days"},
		{"Hidden Draft Tour", "Private activity", "Alappuzha, Kerala", "draft", "5 days"},
	}

	for i, fixture := range fixtures {
		_, err := pool.Exec(ctx, `
			INSERT INTO activities (
				id, owner_id, title, description, location,
				starts_at, ends_at, capacity, status, price_minor, currency
			)
			VALUES (
				$1, $2, $3, $4, $5,
				NOW() + $6::interval,
				NOW() + $6::interval + INTERVAL '2 hours',
				10, $7, 2500, 'INR'
			)
		`,
			activityIDs[i], ownerID, fixture.title, fixture.description,
			fixture.location, fixture.startOffset, fixture.status,
		)
		require.NoError(t, err)
	}

	repo := NewRepository(pool)

	t.Run("search matches title", func(t *testing.T) {
		items, err := repo.ListPublished(ctx, 20, 0, "backwater", "")
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, activityIDs[0], items[0].ID)
	})

	t.Run("search matches description", func(t *testing.T) {
		items, err := repo.ListPublished(ctx, 20, 0, "cuisine", "")
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, activityIDs[2], items[0].ID)
	})

	t.Run("location matching is case insensitive and partial", func(t *testing.T) {
		items, err := repo.ListPublished(ctx, 20, 0, "", "kerala")
		require.NoError(t, err)
		require.Len(t, items, 3)
		for _, item := range items {
			require.Contains(t, strings.ToLower(item.Location), "kerala")
		}
	})

	t.Run("search and location filters combine", func(t *testing.T) {
		items, err := repo.ListPublished(ctx, 20, 0, "kerala", "munnar")
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, activityIDs[1], items[0].ID)
	})

	t.Run("pagination returns the requested page", func(t *testing.T) {
		firstPage, err := repo.ListPublished(ctx, 1, 0, "", "Kerala")
		require.NoError(t, err)
		require.Len(t, firstPage, 1)

		secondPage, err := repo.ListPublished(ctx, 1, 1, "", "Kerala")
		require.NoError(t, err)
		require.Len(t, secondPage, 1)

		require.NotEqual(t, firstPage[0].ID, secondPage[0].ID)
		require.True(t, firstPage[0].StartsAt.Before(secondPage[0].StartsAt))
	})

	t.Run("draft activities are not publicly listed", func(t *testing.T) {
		items, err := repo.ListPublished(ctx, 20, 0, "Hidden Draft", "")
		require.NoError(t, err)
		require.Empty(t, items)
	})
}

func TestGetPublishedByIDVisibility(t *testing.T) {
	pool := openActivityTestPool(t)
	ctx := t.Context()

	ownerID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, ownerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cancel()

		_, err := pool.Exec(cleanupCtx,
			`DELETE FROM activities WHERE owner_id = $1`, ownerID)
		if err != nil {
			t.Errorf("cleanup activities: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM users WHERE id = $1`, ownerID)
		if err != nil {
			t.Errorf("cleanup owner: %v", err)
		}
	})

	futurePublishedID := uuid.New()
	futureDraftID := uuid.New()
	pastPublishedID := uuid.New()

	fixtures := []struct {
		id       uuid.UUID
		title    string
		status   string
		startsAt time.Time
	}{
		{
			id:       futurePublishedID,
			title:    "Future Published Activity",
			status:   "published",
			startsAt: time.Now().UTC().Add(48 * time.Hour),
		},
		{
			id:       futureDraftID,
			title:    "Future Draft Activity",
			status:   "draft",
			startsAt: time.Now().UTC().Add(72 * time.Hour),
		},
		{
			id:       pastPublishedID,
			title:    "Past Published Activity",
			status:   "published",
			startsAt: time.Now().UTC().Add(-48 * time.Hour),
		},
	}

	for _, fixture := range fixtures {
		_, err := pool.Exec(ctx, `
			INSERT INTO activities (
				id, owner_id, title, description, location,
				starts_at, ends_at, capacity, status, price_minor, currency
			)
			VALUES (
				$1, $2, $3, 'Visibility test', 'Test location',
				$4::TIMESTAMPTZ, $4::TIMESTAMPTZ + INTERVAL '2 hours',
				10, $5, 2500, 'INR'
			)
		`,
			fixture.id, ownerID, fixture.title, fixture.startsAt, fixture.status,
		)
		require.NoError(t, err)
	}

	repo := NewRepository(pool)

	t.Run("future published activity is visible", func(t *testing.T) {
		item, err := repo.GetPublishedByID(ctx, futurePublishedID)
		require.NoError(t, err)
		require.Equal(t, futurePublishedID, item.ID)
		require.Equal(t, "published", item.Status)
		require.Equal(t, 10, item.RemainingCapacity)
	})

	t.Run("future draft activity is hidden", func(t *testing.T) {
		_, err := repo.GetPublishedByID(ctx, futureDraftID)
		require.ErrorIs(t, err, ErrActivityNotFound)
	})

	t.Run("past published activity is hidden", func(t *testing.T) {
		_, err := repo.GetPublishedByID(ctx, pastPublishedID)
		require.ErrorIs(t, err, ErrActivityNotFound)
	})

	t.Run("unknown activity is not found", func(t *testing.T) {
		_, err := repo.GetPublishedByID(ctx, uuid.New())
		require.ErrorIs(t, err, ErrActivityNotFound)
	})
}

func TestUpdateImageURLByOwner(t *testing.T) {
	pool := openActivityTestPool(t)
	ctx := t.Context()

	ownerID := uuid.New()
	otherOwnerID := uuid.New()
	activityID := uuid.New()

	for _, user := range []struct {
		id    uuid.UUID
		email string
	}{
		{ownerID, uuid.NewString() + "@example.test"},
		{otherOwnerID, uuid.NewString() + "@example.test"},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, role, email_verified)
			VALUES ($1, $2, 'test-hash', 'operator', TRUE)
		`, user.id, user.email)
		require.NoError(t, err)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Image test activity', 'Test description',
			'Test location', NOW() + INTERVAL '2 days',
			NOW() + INTERVAL '3 days', 10, 'published', 2500, 'INR'
		)
	`, activityID, ownerID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM activities WHERE id = $1`, activityID)
		if err != nil {
			t.Errorf("cleanup activity: %v", err)
		}

		_, err = pool.Exec(context.Background(),
			`DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{ownerID, otherOwnerID})
		if err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})

	repo := NewRepository(pool)
	imageURL := "/uploads/activities/test-image.jpg"

	// The owner can update the activity's image URL.
	updated, err := repo.UpdateImageURLByOwner(ctx, ownerID, activityID, imageURL)
	require.NoError(t, err)
	require.NotNil(t, updated.ImageURL)
	require.Equal(t, imageURL, *updated.ImageURL)

	// Another operator cannot overwrite the image URL.
	_, err = repo.UpdateImageURLByOwner(
		ctx, otherOwnerID, activityID, "/uploads/activities/unauthorized.jpg",
	)
	require.ErrorIs(t, err, ErrActivityNotFound)

	// The original image URL remains unchanged.
	current, err := repo.GetByOwner(ctx, ownerID, activityID)
	require.NoError(t, err)
	require.NotNil(t, current.ImageURL)
	require.Equal(t, imageURL, *current.ImageURL)

	// A nonexistent activity is reported as not found.
	_, err = repo.UpdateImageURLByOwner(
		ctx, ownerID, uuid.New(), "/uploads/activities/missing.jpg",
	)
	require.ErrorIs(t, err, ErrActivityNotFound)
}
