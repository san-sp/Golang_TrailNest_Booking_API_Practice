package activity

import (
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newActivityTestRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/activities", handler.List)
	router.GET("/activities/:activityID", handler.GetByID)

	return router
}

func TestGetByIDRejectsInvalidActivityID(t *testing.T) {
	handler := NewHandler(nil)
	router := newActivityTestRouter(handler)

	request := httptest.NewRequest(
		http.MethodGet,
		"/activities/not-a-uuid",
		nil,
	)
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_ACTIVITY_ID")
}

func TestListRejectsInvalidPage(t *testing.T) {
	handler := NewHandler(nil)
	router := newActivityTestRouter(handler)

	testCases := []struct {
		name string
		page string
	}{
		{name: "zero", page: "0"},
		{name: "negative", page: "-1"},
		{name: "not an integer", page: "abc"},
		{name: "integer overflow", page: "999999999999999999999999999999"},
		{name: "above maximum", page: "1000001"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodGet,
				"/activities?page="+tc.page,
				nil,
			)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "INVALID_PAGE")
		})
	}
}

func TestListRejectsInvalidLimit(t *testing.T) {
	handler := NewHandler(nil)
	router := newActivityTestRouter(handler)

	testCases := []struct {
		name  string
		limit string
	}{
		{name: "zero", limit: "0"},
		{name: "negative", limit: "-1"},
		{name: "above maximum", limit: "101"},
		{name: "not an integer", limit: "abc"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodGet,
				"/activities?limit="+tc.limit,
				nil,
			)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "INVALID_LIMIT")
		})
	}
}

type fakeImageStorage struct {
	saveURL    string
	saveErr    error
	deleteErr  error
	saveCalls  int
	deleteURLs []string
}

func (f *fakeImageStorage) Save(
	ctx context.Context,
	file multipart.File,
) (string, error) {
	f.saveCalls++

	if f.saveErr != nil {
		return "", f.saveErr
	}

	return f.saveURL, nil
}

func (f *fakeImageStorage) Delete(
	ctx context.Context,
	imageURL string,
) error {
	f.deleteURLs = append(f.deleteURLs, imageURL)
	return f.deleteErr
}
