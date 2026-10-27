package activity

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
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

func makeMultipartImageRequest(
	t *testing.T,
	activityID uuid.UUID,
) (*http.Request, *bytes.Buffer) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("image", "cover.png")
	require.NoError(t, err)

	// A valid, tiny PNG image.
	pngData := []byte{
		137, 80, 78, 71, 13, 10, 26, 10,
		0, 0, 0, 13, 73, 72, 68, 82,
		0, 0, 0, 1, 0, 0, 0, 1,
		8, 6, 0, 0, 0, 31, 21, 196, 137,
		0, 0, 0, 13, 73, 68, 65, 84,
		8, 215, 99, 248, 207, 192, 240, 31,
		0, 5, 0, 1, 255, 137, 153, 61, 29,
		0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130,
	}

	_, err = part.Write(pngData)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	request := httptest.NewRequest(
		http.MethodPost,
		"/activities/"+activityID.String()+"/image",
		&body,
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	return request, &body
}

func TestOperatorUploadImageRejectsAnotherOwnersActivity(t *testing.T) {
	pool := openActivityTestPool(t)
	ctx := t.Context()

	ownerID := uuid.New()
	otherOperatorID := uuid.New()
	activityID := uuid.New()

	for _, id := range []uuid.UUID{ownerID, otherOperatorID} {
		_, err := pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, role, email_verified)
			VALUES ($1, $2, 'test-hash', 'operator', TRUE)
		`, id, uuid.NewString()+"@example.test")
		require.NoError(t, err)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Upload ownership test', 'Test', 'Test location',
			NOW() + INTERVAL '2 days', NOW() + INTERVAL '3 days',
			10, 'published', 2500, 'INR'
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
			`DELETE FROM users WHERE id = ANY($1)`,
			[]uuid.UUID{ownerID, otherOperatorID})
		if err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})

	fakeStorage := &fakeImageStorage{
		saveURL: "/uploads/activities/test.webp",
	}

	handler := NewHandlerWithImageStorage(
		NewRepository(pool),
		fakeStorage,
	)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/activities/:activityID/image", func(c *gin.Context) {
		c.Set("user_id", otherOperatorID.String())
		c.Next()
	}, handler.OperatorUploadImage)

	request, _ := makeMultipartImageRequest(t, activityID)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, 0, fakeStorage.saveCalls)
	require.Empty(t, fakeStorage.deleteURLs)
}

func TestOperatorUploadImageSuccess(t *testing.T) {
	pool := openActivityTestPool(t)
	ctx := t.Context()

	ownerID := uuid.New()
	activityID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'operator', TRUE)
	`, ownerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO activities (
			id, owner_id, title, description, location,
			starts_at, ends_at, capacity, status, price_minor, currency
		)
		VALUES (
			$1, $2, 'Upload success test', 'Test', 'Test location',
			NOW() + INTERVAL '2 days', NOW() + INTERVAL '3 days',
			10, 'published', 2500, 'INR'
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
			`DELETE FROM users WHERE id = $1`, ownerID)
		if err != nil {
			t.Errorf("cleanup owner: %v", err)
		}
	})

	imageURL := "/uploads/activities/test-upload.webp"
	fakeStorage := &fakeImageStorage{saveURL: imageURL}

	handler := NewHandlerWithImageStorage(
		NewRepository(pool),
		fakeStorage,
	)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/activities/:activityID/image", func(c *gin.Context) {
		c.Set("user_id", ownerID.String())
		c.Next()
	}, handler.OperatorUploadImage)

	request, _ := makeMultipartImageRequest(t, activityID)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, fakeStorage.saveCalls)
	require.Empty(t, fakeStorage.deleteURLs)
	require.Contains(t, recorder.Body.String(), imageURL)

	updated, err := NewRepository(pool).GetByOwner(ctx, ownerID, activityID)
	require.NoError(t, err)
	require.NotNil(t, updated.ImageURL)
	require.Equal(t, imageURL, *updated.ImageURL)
}

func TestOperatorUploadImageCleansUpWhenDatabaseUpdateFails(t *testing.T) {
	pool := openActivityTestPool(t)
	ownerID := uuid.New()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO users (id, email, password_hash, role, email_verified)
		VALUES ($1, $2, 'test-hash', 'operator', TRUE)
	`, ownerID, uuid.NewString()+"@example.test")
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(),
			`DELETE FROM users WHERE id = $1`, ownerID)
		if err != nil {
			t.Errorf("cleanup owner: %v", err)
		}
	})

	// The activity doesn't exist, so the database update must fail.
	activityID := uuid.New()
	imageURL := "/uploads/activities/orphan.webp"

	fakeStorage := &fakeImageStorage{
		saveURL: "/uploads/activities/orphan.webp",
	}

	fakeRepository := &failingImageRepository{
		activity: Activity{
			ID:      activityID,
			OwnerID: ownerID,
		},
	}

	handler := NewHandlerWithImageDependencies(
		nil,
		fakeRepository,
		fakeStorage,
	)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/activities/:activityID/image", func(c *gin.Context) {
		c.Set("user_id", ownerID.String())
		c.Next()
	}, handler.OperatorUploadImage)

	request, _ := makeMultipartImageRequest(t, activityID)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, 1, fakeStorage.saveCalls)
	require.Equal(t, []string{imageURL}, fakeStorage.deleteURLs)
}

type failingImageRepository struct {
	activity Activity
}

func (f *failingImageRepository) GetByOwner(
	ctx context.Context,
	ownerID uuid.UUID,
	id uuid.UUID,
) (Activity, error) {
	return f.activity, nil
}

func (f *failingImageRepository) UpdateImageURLByOwner(
	ctx context.Context,
	ownerID uuid.UUID,
	id uuid.UUID,
	imageURL string,
) (Activity, error) {
	return Activity{}, errors.New("simulated database failure")
}

func TestOperatorUploadImageRejectsMissingImage(t *testing.T) {
	ownerID := uuid.New()
	activityID := uuid.New()

	fakeStorage := &fakeImageStorage{
		saveURL: "/uploads/activities/test.webp",
	}

	fakeRepository := &failingImageRepository{
		activity: Activity{
			ID:      activityID,
			OwnerID: ownerID,
		},
	}

	handler := NewHandlerWithImageDependencies(
		nil,
		fakeRepository,
		fakeStorage,
	)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/activities/:activityID/image", func(c *gin.Context) {
		c.Set("user_id", ownerID.String())
		c.Next()
	}, handler.OperatorUploadImage)

	request := httptest.NewRequest(
		http.MethodPost,
		"/activities/"+activityID.String()+"/image",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, 0, fakeStorage.saveCalls)
	require.Empty(t, fakeStorage.deleteURLs)
}
