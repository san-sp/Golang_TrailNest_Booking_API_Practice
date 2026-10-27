package booking

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"trailnest/internal/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBookingHTTPRejectsUnauthenticatedRequests(t *testing.T) {
	tokens, err := auth.NewTokenManager(
		"trailnest-http-ownership-test-secret-32-bytes",
	)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(auth.RequireAuthentication(tokens))

	handler := NewHandler(nil)

	router.GET("/api/v1/bookings/:bookingID", handler.GetByID)
	router.POST("/api/v1/bookings/:bookingID/cancel", handler.Cancel)
	router.GET("/api/v1/bookings", handler.ListMine)

	requests := []struct {
		method string
		path   string
	}{
		{
			method: http.MethodGet,
			path:   "/api/v1/bookings/" + uuid.NewString(),
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/bookings/" + uuid.NewString() + "/cancel",
		},
		{
			method: http.MethodGet,
			path:   "/api/v1/bookings",
		},
	}

	for _, tc := range requests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
	}
}

func TestBookingHTTPPreventsAccessToAnotherCustomersBooking(t *testing.T) {
	pool := openBookingTestPool(t)

	ownerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	repo := NewRepository(pool)
	booking, err := repo.Create(t.Context(), ownerID, activityID, 1)
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

	tokens, err := auth.NewTokenManager(
		"trailnest-http-ownership-test-secret-32-bytes",
	)
	require.NoError(t, err)

	token, err := tokens.CreateAccessToken(otherCustomerID, "customer")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(auth.RequireAuthentication(tokens))

	handler := NewHandler(repo)
	router.GET("/api/v1/bookings/:bookingID", handler.GetByID)
	router.POST("/api/v1/bookings/:bookingID/cancel", handler.Cancel)

	requests := []struct {
		method string
		path   string
	}{
		{
			method: http.MethodGet,
			path:   "/api/v1/bookings/" + booking.ID.String(),
		},
		{
			method: http.MethodPost,
			path:   "/api/v1/bookings/" + booking.ID.String() + "/cancel",
		},
	}

	for _, tc := range requests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(nil))
			req.Header.Set("Authorization", "Bearer "+token)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusNotFound, recorder.Code)
		})
	}

	// Verify the other customer's cancellation attempt did not alter the booking.
	var status string
	err = pool.QueryRow(
		t.Context(),
		`SELECT status FROM bookings WHERE id = $1`,
		booking.ID,
	).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "confirmed", status)
}
