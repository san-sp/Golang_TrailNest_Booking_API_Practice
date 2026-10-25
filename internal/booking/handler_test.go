package booking

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newBookingTestRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.POST("/bookings", handler.Create)

	return router
}

func TestCreateRejectsInvalidJSON(t *testing.T) {
	handler := NewHandler(nil)
	router := newBookingTestRouter(handler)

	request := httptest.NewRequest(
		http.MethodPost,
		"/bookings",
		strings.NewReader("{invalid json"),
	)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_REQUEST")
}

func TestCreateRejectsInvalidActivityID(t *testing.T) {
	handler := NewHandler(nil)
	router := newBookingTestRouter(handler)

	request := httptest.NewRequest(
		http.MethodPost,
		"/bookings",
		strings.NewReader(`{"activity_id":"not-a-uuid","quantity":1}`),
	)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_ACTIVITY_ID")
}

func TestCreateRejectsInvalidQuantity(t *testing.T) {
	handler := NewHandler(nil)
	router := newBookingTestRouter(handler)

	request := httptest.NewRequest(
		http.MethodPost,
		"/bookings",
		strings.NewReader(`{"activity_id":"550e8400-e29b-41d4-a716-446655440000","quantity":11}`),
	)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_QUANTITY")
}

func TestCreateRejectsMissingAuthentication(t *testing.T) {
	handler := NewHandler(nil)
	router := newBookingTestRouter(handler)

	request := httptest.NewRequest(
		http.MethodPost,
		"/bookings",
		strings.NewReader(`{"activity_id":"550e8400-e29b-41d4-a716-446655440000","quantity":1}`),
	)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "UNAUTHENTICATED")
}

func TestCancelRejectsInvalidBookingID(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/bookings/:bookingID/cancel", handler.Cancel)

	request := httptest.NewRequest(
		http.MethodPost,
		"/bookings/not-a-uuid/cancel",
		nil,
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_BOOKING_ID")
}

func TestCancelRejectsMissingAuthentication(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/bookings/:bookingID/cancel", handler.Cancel)

	request := httptest.NewRequest(
		http.MethodPost,
		"/bookings/550e8400-e29b-41d4-a716-446655440000/cancel",
		nil,
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "UNAUTHENTICATED")
}

func TestGetByIDRejectsInvalidBookingID(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/bookings/:bookingID", handler.GetByID)

	request := httptest.NewRequest(
		http.MethodGet,
		"/bookings/not-a-uuid",
		nil,
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_BOOKING_ID")
}

func TestGetByIDRejectsMissingAuthentication(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/bookings/:bookingID", handler.GetByID)

	request := httptest.NewRequest(
		http.MethodGet,
		"/bookings/550e8400-e29b-41d4-a716-446655440000",
		nil,
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "UNAUTHENTICATED")
}

func TestAdminListRejectsInvalidPage(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/admin/bookings", handler.AdminList)

	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/bookings?page=abc",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_PAGE")
}

func TestAdminListRejectsZeroPage(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/admin/bookings", handler.AdminList)

	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/bookings?page=0",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_PAGE")
}

func TestAdminListRejectsInvalidLimit(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/admin/bookings", handler.AdminList)

	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/bookings?limit=abc",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_LIMIT")
}

func TestAdminListRejectsLimitAboveMaximum(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/admin/bookings", handler.AdminList)

	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/bookings?limit=101",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_LIMIT")
}

func TestAdminGetByIDRejectsInvalidBookingID(t *testing.T) {
	handler := NewHandler(nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/admin/bookings/:bookingID", handler.AdminGetByID)

	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/bookings/not-a-uuid",
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_BOOKING_ID")
}
