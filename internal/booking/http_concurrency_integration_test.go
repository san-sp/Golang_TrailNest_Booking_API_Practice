package booking

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"trailnest/internal/auth"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConcurrentBookingHTTPRequestsRespectCapacity(t *testing.T) {
	pool := openBookingTestPool(t)

	customerID, activityID := createBookingActivityFixture(
		t,
		pool,
		"published",
	)

	repo := NewRepository(pool)

	// Fill 9 of the activity's 10 spots.
	_, err := repo.Create(t.Context(), customerID, activityID, 9)
	require.NoError(t, err)

	tokens, err := auth.NewTokenManager(
		"trailnest-http-concurrency-test-secret-32-bytes",
	)
	require.NoError(t, err)

	token, err := tokens.CreateAccessToken(customerID, "customer")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(auth.RequireAuthentication(tokens))
	router.POST("/api/v1/bookings", NewHandler(repo).Create)

	const requestCount = 50

	start := make(chan struct{})
	results := make(chan struct {
		statusCode int
		body       string
	}, requestCount)

	var wg sync.WaitGroup

	for i := 0; i < requestCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			body, err := json.Marshal(map[string]any{
				"activity_id": activityID.String(),
				"quantity":    1,
			})
			if err != nil {
				results <- struct {
					statusCode int
					body       string
				}{http.StatusInternalServerError, err.Error()}
				return
			}

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/bookings",
				bytes.NewReader(body),
			)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)

			results <- struct {
				statusCode int
				body       string
			}{recorder.Code, recorder.Body.String()}
		}()
	}

	close(start)

	wg.Wait()
	close(results)

	successes := 0
	conflicts := 0

	for result := range results {
		switch result.statusCode {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			require.Contains(t, result.body, "INSUFFICIENT_CAPACITY")
			conflicts++
		default:
			t.Errorf(
				"unexpected HTTP status %d: %s",
				result.statusCode,
				result.body,
			)
		}
	}

	require.Equal(t, 1, successes, "exactly one request should book the final spot")
	require.Equal(t, requestCount-1, conflicts, "all other requests should conflict")

	var confirmedQuantity int
	err = pool.QueryRow(
		t.Context(),
		`SELECT COALESCE(SUM(quantity), 0)::INTEGER
		 FROM bookings
		 WHERE activity_id = $1 AND status = 'confirmed'`,
		activityID,
	).Scan(&confirmedQuantity)
	require.NoError(t, err)
	require.Equal(t, 10, confirmedQuantity)

	var confirmedBookingCount int
	err = pool.QueryRow(
		t.Context(),
		`SELECT COUNT(*)
		 FROM bookings
		 WHERE activity_id = $1 AND status = 'confirmed'`,
		activityID,
	).Scan(&confirmedBookingCount)
	require.NoError(t, err)
	require.Equal(t, 2, confirmedBookingCount)
}
