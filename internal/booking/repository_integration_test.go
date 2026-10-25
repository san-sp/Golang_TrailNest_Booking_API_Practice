package booking

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func openBookingTestPool(t *testing.T) *pgxpool.Pool {
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

func createBookingActivityFixture(t *testing.T, pool *pgxpool.Pool, status string) (uuid.UUID, uuid.UUID) {
	t.Helper()

	ctx := t.Context()
	customerID := uuid.New()
	activityID := uuid.New()

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
			$1, $2, 'Booking test activity', '',
			'Test location', NOW() + INTERVAL '2 days',
			NOW() + INTERVAL '3 days', 10, $3, 2500, 'INR'
		)
	`, activityID, customerID, status)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		// Delete child records before their referenced activity.
		_, err := pool.Exec(
			cleanupCtx,
			`DELETE FROM bookings WHERE activity_id = $1`,
			activityID,
		)
		require.NoError(t, err)

		_, err = pool.Exec(
			cleanupCtx,
			`DELETE FROM activities WHERE id = $1`,
			activityID,
		)
		require.NoError(t, err)

		_, err = pool.Exec(
			cleanupCtx,
			`DELETE FROM users WHERE id = $1`,
			customerID,
		)
		require.NoError(t, err)
	})

	return customerID, activityID
}

func TestCreateReturnsActivityNotFound(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	_, err := repo.Create(
		t.Context(),
		uuid.New(),
		uuid.New(),
		1,
	)

	require.Error(t, err)
	require.True(
		t,
		errors.Is(err, ErrActivityNotFound),
		"expected ErrActivityNotFound, got: %v",
		err,
	)
}

func TestCreateRejectsDraftActivity(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(t, pool, "draft")

	_, err := repo.Create(t.Context(), customerID, activityID, 1)

	require.ErrorIs(t, err, ErrActivityNotBookable)
}

func TestCreateRejectsStartedActivity(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	_, err := pool.Exec(
		t.Context(),
		`UPDATE activities
         SET starts_at = NOW() - INTERVAL '1 hour',
             ends_at = NOW() + INTERVAL '1 hour'
         WHERE id = $1`,
		activityID,
	)
	require.NoError(t, err)

	_, err = repo.Create(
		t.Context(),
		customerID,
		activityID,
		1,
	)

	require.ErrorIs(t, err, ErrActivityNotBookable)
}

func TestCreateRejectsBookingOverCapacity(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	// The fixture creates an activity with capacity 10.
	// Fill 8 seats with an existing confirmed booking.
	_, err := pool.Exec(
		t.Context(),
		`INSERT INTO bookings (
			customer_id, activity_id, quantity, status
		)
		VALUES ($1, $2, 8, 'confirmed')`,
		customerID,
		activityID,
	)
	require.NoError(t, err)

	// Only 2 seats remain, so a request for 3 must fail.
	_, err = repo.Create(
		t.Context(),
		customerID,
		activityID,
		3,
	)

	require.ErrorIs(t, err, ErrInsufficientCapacity)
}

func TestCreateBookingSuccessfully(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	created, err := repo.Create(
		t.Context(),
		customerID,
		activityID,
		2,
	)

	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, created.ID)
	require.Equal(t, customerID, created.CustomerID)
	require.Equal(t, activityID, created.ActivityID)
	require.Equal(t, 2, created.Quantity)
	require.Equal(t, "confirmed", created.Status)
	require.False(t, created.CreatedAt.IsZero())
}

func TestCreateConcurrentBookingsRespectCapacity(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	ownerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	customerID1 := uuid.New()
	customerID2 := uuid.New()

	for _, customerID := range []uuid.UUID{customerID1, customerID2} {
		_, err := pool.Exec(
			t.Context(),
			`INSERT INTO users (id, email, password_hash, role, email_verified)
			 VALUES ($1, $2, 'test-hash', 'customer', TRUE)`,
			customerID,
			uuid.NewString()+"@example.test",
		)
		require.NoError(t, err)

	}

	// The activity has capacity 10. Reserve 9 seats first.
	_, err := pool.Exec(
		t.Context(),
		`INSERT INTO bookings (customer_id, activity_id, quantity, status)
		 VALUES ($1, $2, 9, 'confirmed')`,
		ownerID,
		activityID,
	)
	require.NoError(t, err)

	type result struct {
		err error
	}

	start := make(chan struct{})
	results := make(chan result, 2)

	var wg sync.WaitGroup

	for _, customerID := range []uuid.UUID{customerID1, customerID2} {
		wg.Add(1)

		go func(customerID uuid.UUID) {
			defer wg.Done()
			<-start

			_, err := repo.Create(
				t.Context(),
				customerID,
				activityID,
				1,
			)
			results <- result{err: err}
		}(customerID)
	}

	close(start)
	wg.Wait()
	close(results)

	successes := 0
	capacityErrors := 0

	for result := range results {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrInsufficientCapacity):
			capacityErrors++
		default:
			t.Fatalf("unexpected booking error: %v", result.err)
		}
	}

	require.Equal(t, 1, successes)
	require.Equal(t, 1, capacityErrors)

	var bookedQuantity int
	err = pool.QueryRow(
		t.Context(),
		`SELECT COALESCE(SUM(quantity), 0)::INTEGER
		 FROM bookings
		 WHERE activity_id = $1 AND status = 'confirmed'`,
		activityID,
	).Scan(&bookedQuantity)

	require.NoError(t, err)
	require.Equal(t, 10, bookedQuantity)
	cleanupCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_, err = pool.Exec(
		cleanupCtx,
		`DELETE FROM bookings WHERE activity_id = $1`,
		activityID,
	)
	require.NoError(t, err)

	_, err = pool.Exec(
		cleanupCtx,
		`DELETE FROM users WHERE id = ANY($1)`,
		[]uuid.UUID{customerID1, customerID2},
	)
	require.NoError(t, err)
}

func TestCancelBookingSuccessfully(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	created, err := repo.Create(
		t.Context(),
		customerID,
		activityID,
		2,
	)
	require.NoError(t, err)
	require.Equal(t, "confirmed", created.Status)

	err = repo.Cancel(t.Context(), customerID, created.ID)
	require.NoError(t, err)

	var status string
	err = pool.QueryRow(
		t.Context(),
		`SELECT status FROM bookings WHERE id = $1`,
		created.ID,
	).Scan(&status)

	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
}

func TestCancelBookingTwiceReturnsError(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	created, err := repo.Create(
		t.Context(),
		customerID,
		activityID,
		1,
	)
	require.NoError(t, err)

	err = repo.Cancel(t.Context(), customerID, created.ID)
	require.NoError(t, err)

	err = repo.Cancel(t.Context(), customerID, created.ID)
	require.ErrorIs(t, err, ErrBookingAlreadyCancelled)
}

func TestCancelledBookingReleasesCapacity(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	firstBooking, err := repo.Create(
		t.Context(),
		customerID,
		activityID,
		8,
	)
	require.NoError(t, err)

	err = repo.Cancel(t.Context(), customerID, firstBooking.ID)
	require.NoError(t, err)

	secondBooking, err := repo.Create(
		t.Context(),
		customerID,
		activityID,
		10,
	)
	require.NoError(t, err)

	require.Equal(t, 10, secondBooking.Quantity)
	require.Equal(t, "confirmed", secondBooking.Status)
}

func TestGetByIDRejectsAnotherCustomersBooking(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	ownerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	booking, err := repo.Create(
		t.Context(),
		ownerID,
		activityID,
		1,
	)
	require.NoError(t, err)

	otherCustomerID := uuid.New()

	_, err = pool.Exec(
		t.Context(),
		`INSERT INTO users (id, email, password_hash, role, email_verified)
		 VALUES ($1, $2, 'test-hash', 'customer', TRUE)`,
		otherCustomerID,
		uuid.NewString()+"@example.test",
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		_, err := pool.Exec(
			cleanupCtx,
			`DELETE FROM users WHERE id = $1`,
			otherCustomerID,
		)
		require.NoError(t, err)
	})

	_, err = repo.GetByID(
		t.Context(),
		otherCustomerID,
		booking.ID,
	)

	require.ErrorIs(t, err, ErrBookingNotFound)
}

func TestGetByIDReturnsOwnBooking(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	created, err := repo.Create(
		t.Context(),
		customerID,
		activityID,
		2,
	)
	require.NoError(t, err)

	got, err := repo.GetByID(
		t.Context(),
		customerID,
		created.ID,
	)

	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, customerID, got.CustomerID)
	require.Equal(t, activityID, got.ActivityID)
	require.Equal(t, 2, got.Quantity)
	require.Equal(t, "confirmed", got.Status)
	require.Equal(t, "Booking test activity", got.ActivityTitle)
	require.Equal(t, "Test location", got.ActivityLocation)
	require.False(t, got.ActivityStartsAt.IsZero())
}

func TestListByCustomerReturnsOnlyOwnBookings(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerA, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	booking1, err := repo.Create(
		t.Context(),
		customerA,
		activityID,
		1,
	)
	require.NoError(t, err)

	booking2, err := repo.Create(
		t.Context(),
		customerA,
		activityID,
		2,
	)
	require.NoError(t, err)

	customerB := uuid.New()

	_, err = pool.Exec(
		t.Context(),
		`INSERT INTO users (id, email, password_hash, role, email_verified)
		 VALUES ($1, $2, 'test-hash', 'customer', TRUE)`,
		customerB,
		uuid.NewString()+"@example.test",
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		_, err := pool.Exec(
			cleanupCtx,
			`DELETE FROM users WHERE id = $1`,
			customerB,
		)
		require.NoError(t, err)
	})

	bookings, err := repo.ListByCustomer(t.Context(), customerA)

	require.NoError(t, err)
	require.Len(t, bookings, 2)

	found := map[uuid.UUID]bool{}
	for _, booking := range bookings {
		require.Equal(t, customerA, booking.CustomerID)
		require.Equal(t, activityID, booking.ActivityID)
		found[booking.ID] = true
	}

	require.True(t, found[booking1.ID])
	require.True(t, found[booking2.ID])

	customerBBookings, err := repo.ListByCustomer(t.Context(), customerB)
	require.NoError(t, err)
	require.Empty(t, customerBBookings)
}

func TestGetAdminByIDReturnsBooking(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	created, err := repo.Create(
		t.Context(),
		customerID,
		activityID,
		2,
	)
	require.NoError(t, err)

	got, err := repo.GetAdminByID(t.Context(), created.ID)

	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, customerID, got.CustomerID)
	require.Equal(t, activityID, got.ActivityID)
	require.Equal(t, 2, got.Quantity)
	require.Equal(t, "confirmed", got.Status)
	require.Equal(t, "Booking test activity", got.ActivityTitle)
	require.Equal(t, "Test location", got.ActivityLocation)
}

func TestGetAdminByIDReturnsNotFound(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	_, err := repo.GetAdminByID(
		t.Context(),
		uuid.New(),
	)

	require.ErrorIs(t, err, ErrBookingNotFound)
}

func TestListAllAdminRespectsLimitAndOffset(t *testing.T) {
	pool := openBookingTestPool(t)
	repo := NewRepository(pool)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	createdIDs := make(map[uuid.UUID]bool, 3)

	for i := 0; i < 3; i++ {
		created, err := repo.Create(
			t.Context(),
			customerID,
			activityID,
			1,
		)
		require.NoError(t, err)
		createdIDs[created.ID] = true
	}

	firstPage, err := repo.ListAllAdmin(t.Context(), 2, 0)
	require.NoError(t, err)
	require.Len(t, firstPage, 2)

	secondPage, err := repo.ListAllAdmin(t.Context(), 2, 2)
	require.NoError(t, err)
	require.NotEmpty(t, secondPage)

	// Every page should contain distinct booking IDs.
	seen := make(map[uuid.UUID]bool)

	for _, booking := range firstPage {
		require.False(t, seen[booking.ID], "duplicate booking across pages")
		seen[booking.ID] = true
	}

	for _, booking := range secondPage {
		require.False(t, seen[booking.ID], "duplicate booking across pages")
		seen[booking.ID] = true
	}

	// Verify all three bookings created by this test appear
	// somewhere across the two pages.
	for id := range createdIDs {
		require.True(
			t,
			seen[id],
			"test-created booking %s was not found in the first two pages",
			id,
		)
	}
}
