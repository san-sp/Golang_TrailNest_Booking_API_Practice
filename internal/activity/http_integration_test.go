package activity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGetByIDHTTPVisibility(t *testing.T) {
	pool := openActivityTestPool(t)
	ctx := t.Context()

	ownerID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, ownerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	publishedID := uuid.New()
	draftID := uuid.New()

	for _, fixture := range []struct {
		id     uuid.UUID
		title  string
		status string
	}{
		{publishedID, "Public HTTP Activity", "published"},
		{draftID, "Hidden Draft HTTP Activity", "draft"},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO activities (
				id, owner_id, title, description, location,
				starts_at, ends_at, capacity, status, price_minor, currency
			)
			VALUES (
				$1, $2, $3, 'HTTP integration test', 'Test location',
				NOW() + INTERVAL '2 days',
				NOW() + INTERVAL '3 days',
				10, $4, 2500, 'INR'
			)
		`, fixture.id, ownerID, fixture.title, fixture.status)
		require.NoError(t, err)
	}

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

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/activities/:activityID",
		NewHandler(NewRepository(pool)).GetByID)

	t.Run("published activity returns 200", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/activities/"+publishedID.String(),
			nil,
		)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), publishedID.String())
		require.Contains(t, recorder.Body.String(), "Public HTTP Activity")
	})

	t.Run("draft activity returns 404", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/activities/"+draftID.String(),
			nil,
		)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNotFound, recorder.Code)
		require.Contains(t, recorder.Body.String(), "ACTIVITY_NOT_FOUND")
	})

	t.Run("invalid UUID returns 400", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/activities/not-a-uuid",
			nil,
		)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "INVALID_ACTIVITY_ID")
	})

	t.Run("unknown UUID returns 404", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/activities/"+uuid.New().String(),
			nil,
		)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNotFound, recorder.Code)
		require.Contains(t, recorder.Body.String(), "ACTIVITY_NOT_FOUND")
	})
}

func TestListHTTPReturnsActivitiesAndPagination(t *testing.T) {
	pool := openActivityTestPool(t)
	ctx := t.Context()

	ownerID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'customer', TRUE)
	`, ownerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	activityID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'HTTP List Test Activity', 'Public list test',
			'Thiruvananthapuram', NOW() + INTERVAL '2 days',
			NOW() + INTERVAL '3 days', 10, 'published', 2500, 'INR'
		)
	`, activityID, ownerID)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer cancel()

		_, err := pool.Exec(cleanupCtx,
			`DELETE FROM activities WHERE id = $1`, activityID)
		if err != nil {
			t.Errorf("cleanup activity: %v", err)
		}

		_, err = pool.Exec(cleanupCtx,
			`DELETE FROM users WHERE id = $1`, ownerID)
		if err != nil {
			t.Errorf("cleanup owner: %v", err)
		}
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/activities", NewHandler(NewRepository(pool)).List)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/activities?search=HTTP+List+Test&location=Thiruvananthapuram&page=1&limit=10",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), activityID.String())
	require.Contains(t, recorder.Body.String(), "HTTP List Test Activity")
	require.Contains(t, recorder.Body.String(), `"pagination"`)
	require.Contains(t, recorder.Body.String(), `"page":1`)
	require.Contains(t, recorder.Body.String(), `"limit":10`)
}
