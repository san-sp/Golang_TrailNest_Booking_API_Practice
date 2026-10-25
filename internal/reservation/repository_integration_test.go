package reservation

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

func openReservationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)

	if config.ConnConfig.Database != "trailnest_test" &&
		config.ConnConfig.Database != "trailnest_expiry_test" {
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

type expiryFixture struct {
	customerID    uuid.UUID
	activityID    uuid.UUID
	reservationID uuid.UUID
}

func createExpiryFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	status string,
	expiryOffset time.Duration,
) expiryFixture {
	t.Helper()

	ctx := t.Context()
	f := expiryFixture{
		customerID:    uuid.New(),
		activityID:    uuid.New(),
		reservationID: uuid.New(),
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, f.customerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Expiry test activity', '',
			'Test location', NOW() + INTERVAL '2 days',
			NOW() + INTERVAL '3 days', 10, 'published', 2500, 'INR'
		)
	`, f.activityID, f.customerID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO reservations (
			id, customer_id, activity_id, quantity, status, expires_at
		)
		VALUES ($1, $2, $3, 1, $4, NOW() + $5 * INTERVAL '1 second')
	`, f.reservationID, f.customerID, f.activityID, status, expiryOffset.Seconds())
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		// Delete only records created by this fixture.
		_, err := pool.Exec(
			cleanupCtx,
			`DELETE FROM reservations WHERE id = $1`,
			f.reservationID,
		)
		if err != nil {
			t.Errorf("cleanup reservation: %v", err)
		}

		_, err = pool.Exec(
			cleanupCtx,
			`DELETE FROM activities WHERE id = $1`,
			f.activityID,
		)
		if err != nil {
			t.Errorf("cleanup activity: %v", err)
		}

		_, err = pool.Exec(
			cleanupCtx,
			`DELETE FROM users WHERE id = $1`,
			f.customerID,
		)
		if err != nil {
			t.Errorf("cleanup customer: %v", err)
		}
	})

	return f
}

func reservationStatus(
	t *testing.T,
	pool *pgxpool.Pool,
	reservationID uuid.UUID,
) string {
	t.Helper()

	var status string
	err := pool.QueryRow(
		t.Context(),
		`SELECT status FROM reservations WHERE id = $1`,
		reservationID,
	).Scan(&status)
	require.NoError(t, err)

	return status
}

func TestExpireOverdueExpiresPendingReservation(t *testing.T) {
	pool := openReservationTestPool(t)
	f := createExpiryFixture(t, pool, "pending", -time.Minute)

	repo := NewRepository(pool)

	count, err := repo.ExpireOverdue(t.Context(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, int64(1))
	require.Equal(t, "expired", reservationStatus(t, pool, f.reservationID))
}

func TestExpireOverdueLeavesFutureReservationPending(t *testing.T) {
	pool := openReservationTestPool(t)
	f := createExpiryFixture(t, pool, "pending", time.Hour)

	repo := NewRepository(pool)

	count, err := repo.ExpireOverdue(t.Context(), 10)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, "pending", reservationStatus(t, pool, f.reservationID))
}

func TestExpireOverdueLeavesConfirmedReservationUntouched(t *testing.T) {
	pool := openReservationTestPool(t)
	f := createExpiryFixture(t, pool, "confirmed", -time.Minute)

	repo := NewRepository(pool)

	count, err := repo.ExpireOverdue(t.Context(), 10)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, "confirmed", reservationStatus(t, pool, f.reservationID))
}

func TestExpireOverdueRespectsBatchLimit(t *testing.T) {
	pool := openReservationTestPool(t)

	fixtures := make([]expiryFixture, 3)
	for i := range fixtures {
		fixtures[i] = createExpiryFixture(t, pool, "pending", -time.Minute)
	}

	repo := NewRepository(pool)

	count, err := repo.ExpireOverdue(t.Context(), 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)

	// The third reservation may be either one of the selected records or
	// the remaining pending record; assert the total across all fixtures.
	var expiredCount int
	err = pool.QueryRow(t.Context(), `
		SELECT COUNT(*)
		FROM reservations
		WHERE id = ANY($1::uuid[])
		  AND status = 'expired'
	`, []uuid.UUID{
		fixtures[0].reservationID,
		fixtures[1].reservationID,
		fixtures[2].reservationID,
	}).Scan(&expiredCount)
	require.NoError(t, err)
	require.Equal(t, 2, expiredCount)

}

func TestExpireOverdueRejectsInvalidBatchSize(t *testing.T) {
	pool := openReservationTestPool(t)
	repo := NewRepository(pool)

	_, err := repo.ExpireOverdue(context.Background(), 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "batch size must be positive")
}

func TestExpireOverdueCanProcessAnotherBatch(t *testing.T) {
	pool := openReservationTestPool(t)

	fixtures := make([]expiryFixture, 3)
	for i := range fixtures {
		fixtures[i] = createExpiryFixture(t, pool, "pending", -time.Minute)
	}

	repo := NewRepository(pool)

	firstCount, err := repo.ExpireOverdue(t.Context(), 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, firstCount)

	var expiredAfterFirst int
	for _, f := range fixtures {
		if reservationStatus(t, pool, f.reservationID) == "expired" {
			expiredAfterFirst++
		}
	}
	require.Equal(t, 2, expiredAfterFirst)

	secondCount, err := repo.ExpireOverdue(t.Context(), 2)
	require.NoError(t, err)
	require.EqualValues(t, 1, secondCount)

	var expiredAfterSecond int
	for _, f := range fixtures {
		if reservationStatus(t, pool, f.reservationID) == "expired" {
			expiredAfterSecond++
		}
	}
	require.Equal(t, 3, expiredAfterSecond)
}
