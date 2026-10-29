package activity

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOperatorListRejectsOversizedPageValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewHandler(nil)
	router := gin.New()
	router.GET("/operator/activities", func(c *gin.Context) {
		c.Set("user_id", "11111111-1111-4111-8111-111111111111")
		handler.OperatorList(c)
	})

	testCases := []struct {
		name string
		page string
	}{
		{name: "above maximum", page: "1000001"},
		{name: "integer overflow", page: "999999999999999999999999999999"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodGet,
				"/operator/activities?page="+tc.page,
				nil,
			)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "INVALID_PAGE")
		})
	}
}
