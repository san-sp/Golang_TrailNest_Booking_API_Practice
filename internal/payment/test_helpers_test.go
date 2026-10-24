package payment

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}

	if config.ConnConfig.Database != "trailnest_test" {
		t.Fatalf(
			"refusing to run integration tests against database %q",
			config.ConnConfig.Database,
		)
	}

	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}

	if err := pool.Ping(t.Context()); err != nil {
		pool.Close()
		t.Fatalf("ping test database: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}
