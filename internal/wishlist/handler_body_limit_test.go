package wishlist

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAddRejectsOversizedRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.POST("/wishlist", func(c *gin.Context) {
		NewHandler(nil).Add(c)
	})

	body := `{"activity_id":"550e8400-e29b-41d4-a716-446655440000","extra":"` +
		strings.Repeat("x", int(maxWishlistRequestBody)) +
		`"}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/wishlist",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Contains(t, recorder.Body.String(), "REQUEST_TOO_LARGE")
}
