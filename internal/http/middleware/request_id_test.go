package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRequestIDSetsResponseHeaderAndContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) {
		requestID, exists := c.Get("request_id")
		require.True(t, exists)
		require.NotEmpty(t, requestID)
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)

	headerID := recorder.Header().Get(RequestIDHeader)
	require.NotEmpty(t, headerID)

	parsedID, err := uuid.Parse(headerID)
	require.NoError(t, err)
	require.Equal(t, parsedID.String(), headerID)
}

func TestRequestIDIsUniqueAcrossRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	makeRequest := func() string {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code)
		return recorder.Header().Get(RequestIDHeader)
	}

	firstID := makeRequest()
	secondID := makeRequest()

	require.NotEmpty(t, firstID)
	require.NotEmpty(t, secondID)
	require.NotEqual(t, firstID, secondID)
}
