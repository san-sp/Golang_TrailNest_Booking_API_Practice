package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSecurityHeadersAreSet(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(
		t,
		"nosniff",
		recorder.Header().Get("X-Content-Type-Options"),
	)
	require.Equal(
		t,
		"DENY",
		recorder.Header().Get("X-Frame-Options"),
	)
	require.Equal(
		t,
		"strict-origin-when-cross-origin",
		recorder.Header().Get("Referrer-Policy"),
	)
}

func TestSecurityHeadersAreSetOnErrorResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/", func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "bad request",
		})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(
		t,
		"nosniff",
		recorder.Header().Get("X-Content-Type-Options"),
	)
	require.Equal(
		t,
		"DENY",
		recorder.Header().Get("X-Frame-Options"),
	)
	require.Equal(
		t,
		"strict-origin-when-cross-origin",
		recorder.Header().Get("Referrer-Policy"),
	)
}
