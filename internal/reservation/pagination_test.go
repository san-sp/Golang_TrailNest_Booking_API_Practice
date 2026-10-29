package reservation

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminListRejectsOversizedPageValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/admin/reservations", NewHandler(nil).AdminList)

	for _, page := range []string{
		"1000001",
		"999999999999999999999999999999",
	} {
		t.Run(page, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodGet,
				"/admin/reservations?page="+page,
				nil,
			)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "INVALID_PAGE")
		})
	}
}
