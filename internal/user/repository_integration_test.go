package user

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func openUserTestPool(t *testing.T) *pgxpool.Pool {
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

func TestPromoteCustomerToOperatorLifecycle(t *testing.T) {
	pool := openUserTestPool(t)
	ctx := t.Context()
	repo := NewRepository(pool)

	customerID := uuid.New()
	operatorID := uuid.New()
	adminID := uuid.New()

	// Unique emails keep this test independent of existing test data.
	fixtures := []struct {
		id           uuid.UUID
		role         string
		passwordHash string
	}{
		{customerID, "customer", "customer-test-hash"},
		{operatorID, "operator", "operator-test-hash"},
		{adminID, "admin", "admin-test-hash"},
	}

	for _, fixture := range fixtures {
		_, err := pool.Exec(ctx, `
			INSERT INTO users (
				id, email, password_hash, role, email_verified
			)
			VALUES ($1, $2, $3, $4, TRUE)
		`,
			fixture.id,
			uuid.NewString()+"@example.test",
			fixture.passwordHash,
			fixture.role,
		)
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()

		_, err := pool.Exec(
			cleanupCtx,
			`DELETE FROM users WHERE id = ANY($1)`,
			[]uuid.UUID{customerID, operatorID, adminID},
		)
		if err != nil {
			t.Errorf("cleanup test users: %v", err)
		}
	})

	t.Run("customer is promoted successfully", func(t *testing.T) {
		promoted, err := repo.PromoteCustomerToOperator(ctx, customerID)

		require.NoError(t, err)
		require.Equal(t, customerID, promoted.ID)
		require.Equal(t, "operator", promoted.Role)

		// The API-facing User value must not expose credentials.
		encoded, err := json.Marshal(promoted)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "password_hash")
		require.NotContains(t, string(encoded), "customer-test-hash")

		var actualRole string
		err = pool.QueryRow(ctx, `
			SELECT role FROM users WHERE id = $1
		`, customerID).Scan(&actualRole)
		require.NoError(t, err)
		require.Equal(t, "operator", actualRole)
	})

	t.Run("repeated promotion is rejected", func(t *testing.T) {
		_, err := repo.PromoteCustomerToOperator(ctx, customerID)
		require.ErrorIs(t, err, ErrUserNotFound)

		var actualRole string
		err = pool.QueryRow(ctx, `
			SELECT role FROM users WHERE id = $1
		`, customerID).Scan(&actualRole)
		require.NoError(t, err)
		require.Equal(t, "operator", actualRole)
	})

	t.Run("existing operator is not promoted again", func(t *testing.T) {
		_, err := repo.PromoteCustomerToOperator(ctx, operatorID)
		require.ErrorIs(t, err, ErrUserNotFound)

		var actualRole string
		err = pool.QueryRow(ctx, `
			SELECT role FROM users WHERE id = $1
		`, operatorID).Scan(&actualRole)
		require.NoError(t, err)
		require.Equal(t, "operator", actualRole)
	})

	t.Run("admin role is preserved", func(t *testing.T) {
		_, err := repo.PromoteCustomerToOperator(ctx, adminID)
		require.ErrorIs(t, err, ErrUserNotFound)

		var actualRole string
		err = pool.QueryRow(ctx, `
			SELECT role FROM users WHERE id = $1
		`, adminID).Scan(&actualRole)
		require.NoError(t, err)
		require.Equal(t, "admin", actualRole)
	})

	t.Run("nonexistent user is rejected", func(t *testing.T) {
		_, err := repo.PromoteCustomerToOperator(ctx, uuid.New())
		require.ErrorIs(t, err, ErrUserNotFound)
	})
}
