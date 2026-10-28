package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"trailnest/internal/user"
)

func openAdminUserTestPool(t *testing.T) *pgxpool.Pool {
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

func TestAdminPromotionHTTPIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pool := openAdminUserTestPool(t)
	ctx := t.Context()

	customerID := uuid.New()
	customerEmail := uuid.NewString() + "@example.test"
	passwordHash := "integration-test-password-hash"

	_, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, role, email_verified
		)
		VALUES ($1, $2, $3, 'customer', TRUE)
	`, customerID, customerEmail, passwordHash)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM users WHERE id = $1`, customerID)
		if err != nil {
			t.Errorf("cleanup integration test user: %v", err)
		}
	})

	tokens, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	adminToken, err := tokens.CreateAccessToken(uuid.New(), "admin")
	require.NoError(t, err)

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	admin.Use(
		RequireAuthentication(tokens),
		RequireRole("admin"),
	)

	handler := NewAdminUserHandler(user.NewRepository(pool))
	admin.PATCH("/users/:userID/role", handler.PromoteToOperator)

	sendPromotion := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPatch,
			"/api/v1/admin/users/"+customerID.String()+"/role",
			nil,
		)
		req.Header.Set("Authorization", "Bearer "+token)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	t.Run("admin promotes customer and receives safe response", func(t *testing.T) {
		rec := sendPromotion(adminToken)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NotContains(t, rec.Body.String(), passwordHash)
		require.NotContains(t, rec.Body.String(), "password_hash")

		var body struct {
			Data user.User `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, customerID, body.Data.ID)
		require.Equal(t, customerEmail, body.Data.Email)
		require.Equal(t, "operator", body.Data.Role)

		var actualRole string
		err := pool.QueryRow(ctx, `
			SELECT role FROM users WHERE id = $1
		`, customerID).Scan(&actualRole)
		require.NoError(t, err)
		require.Equal(t, "operator", actualRole)
	})

	t.Run("second HTTP promotion returns not found", func(t *testing.T) {
		rec := sendPromotion(adminToken)

		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Contains(t, rec.Body.String(), "customer not found or is not eligible")
	})
}
