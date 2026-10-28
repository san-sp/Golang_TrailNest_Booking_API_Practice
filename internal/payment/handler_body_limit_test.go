package payment

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestPaymentCreateRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	handler := NewHandler(nil, nil)
	router.POST("/payments", func(c *gin.Context) {
		c.Set("user_id", uuid.NewString())
		handler.Create(c)
	})

	body := `{"reservation_id":"` +
		uuid.NewString() + `","padding":"` +
		strings.Repeat("a", maxPaymentRequestBody) + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments",
		strings.NewReader(body),
	)
	req.Header.Set("Idempotency-Key", "test-key")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func TestPaymentVerifyRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	handler := NewHandler(nil, nil)
	router.POST("/payments/verify", func(c *gin.Context) {
		c.Set("user_id", uuid.NewString())
		handler.Verify(c)
	})

	body := `{"payment_id":"` +
		uuid.NewString() + `","padding":"` +
		strings.Repeat("a", maxPaymentRequestBody) + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/verify",
		strings.NewReader(body),
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func TestAdminReconcileRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	handler := NewHandler(nil, nil)
	router.POST("/payments/:paymentID/reconcile", handler.AdminReconcileCapturedPayment)

	body := `{"razorpay_payment_id":"pay_test","padding":"` +
		strings.Repeat("a", maxPaymentRequestBody) + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/payments/"+uuid.NewString()+"/reconcile",
		strings.NewReader(body),
	)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected status %d, got %d; response: %s",
			http.StatusRequestEntityTooLarge,
			recorder.Code,
			recorder.Body.String(),
		)
	}
}
